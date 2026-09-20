// DFLT-00103 / SEC-09: an artifact is agent-authored, so a remote image URL
// inside one must not be fetched merely because the artifact was rendered --
// that fetch tells a third party the reader's IP and that they are looking at
// this artifact right now. Local sources (data:, same-origin paths) reach
// nobody and are rendered as usual.
//
// What these tests can and cannot see. jsdom does not evaluate Content
// Security Policy and never fetches an image, so every assertion below is
// about DOM structure and about events this test fires by hand. The first
// attempt at this ticket was sent back for exactly that blind spot: the SPA's
// CSP forbade external images while this component rendered one on click, and
// a test of this shape stayed green while the button did nothing in a real
// browser. Two things guard against a repeat:
//
//   - 'the CSP and this component agree about external images' below reads
//     index.html and asserts the directive this component's behaviour depends
//     on, so the two cannot drift apart silently;
//   - a src the CSP would refuse never becomes a button here at all (see
//     remoteImageInfo), which is a DOM difference, and therefore is testable.
//
// Whether the browser then actually fetches an allowed image is not, and has
// to be confirmed against a real browser -- see the implementation note.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { MarkdownViewer, isRemoteImageSrc, remoteImageInfo } from './MarkdownViewer';

const REMOTE = 'https://tracker.example.com/pixel.png';
const REMOTE_CLEARTEXT = 'http://tracker.example.com/pixel.png';

describe('MarkdownViewer remote images', () => {
  it('renders no <img> for a remote source until it is asked for', () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE})`} />);

    expect(document.querySelector('img')).toBeNull();
    // The placeholder is a real button, so it is reachable from the keyboard,
    // and names both the image and where it would come from.
    const button = screen.getByRole('button');
    expect(button.textContent).toContain('tracker.example.com');
    expect(button.textContent).toContain('図の説明');
  });

  it('loads the image once the reader clicks, without a Referer', async () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE})`} />);

    await userEvent.click(screen.getByRole('button'));

    const img = document.querySelector('img');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toBe(REMOTE);
    expect(img?.getAttribute('alt')).toBe('図の説明');
    // The artifact-preview URL names the artifact being read; the host of an
    // image inside it has no business learning that.
    expect(img?.getAttribute('referrerpolicy')).toBe('no-referrer');
  });

  // The button unmounts when it is activated. Without this, focus would fall
  // to <body> and the next Tab would restart at the top of the document --
  // the same failure settings/LabelsEditor.tsx handles with pendingFocus.
  it('keeps focus where the reader left it after clicking', async () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE})`} />);

    await userEvent.click(screen.getByRole('button'));

    expect(document.activeElement).toBe(document.querySelector('img'));
    expect(document.activeElement).not.toBe(document.body);
  });

  // WCAG 2.2 SC 4.1.3: the outcome of a control the reader operated has to
  // reach assistive technology, and nothing here moves focus to a message.
  it('announces the outcome through a live region', async () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE})`} />);

    // Mounted and empty before anything happens, so an announcement is a text
    // change inside an existing region rather than a newly inserted element.
    const status = screen.getByRole('status');
    expect(status.textContent).toBe('');

    await userEvent.click(screen.getByRole('button'));
    fireEvent.load(document.querySelector('img')!);
    expect(status.textContent).toContain('図の説明');
  });

  it('offers a new tab, announces the failure and keeps focus when loading fails', async () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE})`} />);

    await userEvent.click(screen.getByRole('button'));
    fireEvent.error(document.querySelector('img')!);

    expect(document.querySelector('img')).toBeNull();
    const link = screen.getByRole('link');
    expect(link.getAttribute('href')).toBe(REMOTE);
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noreferrer');
    expect(document.activeElement).toBe(link);
    expect(screen.getByRole('status').textContent).toContain('tracker.example.com');
  });

  // The counterpart to the CSP assertion below: img-src does not allow
  // cleartext, so an <img> for one would be refused by the browser. A button
  // promising to load it would be a control that does nothing, which is the
  // defect this iteration exists to remove -- so it is a link from the start.
  it('offers a link, not a load button, for a source the CSP would refuse', () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE_CLEARTEXT})`} />);

    expect(screen.queryByRole('button')).toBeNull();
    expect(document.querySelector('img')).toBeNull();
    const link = screen.getByRole('link');
    expect(link.getAttribute('href')).toBe(REMOTE_CLEARTEXT);
    expect(link.textContent).toContain('tracker.example.com');
  });

  it('renders a same-origin path immediately, with no click needed', () => {
    render(<MarkdownViewer content={'![b](/api/artifacts/art-1/content)'} />);

    const srcs = Array.from(document.querySelectorAll('img')).map(i => i.getAttribute('src'));
    expect(srcs).toEqual(['/api/artifacts/art-1/content']);
    expect(screen.queryByRole('button')).toBeNull();
  });

  // react-markdown's own URL sanitization (defaultUrlTransform) drops a
  // data:/blob: src before any of this component's code sees it, so such an
  // image renders as nothing at all rather than as a broken <img src="">.
  // isRemoteImageSrc still classifies those schemes correctly -- it is the
  // rule, not a description of what react-markdown happens to let through.
  it('renders nothing for a source react-markdown strips', () => {
    render(<MarkdownViewer content={'![a](data:image/gif;base64,R0lGODlhAQABAAAAACw=)'} />);

    expect(document.querySelector('img')).toBeNull();
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('classifies sources by whether loading them leaves this origin', () => {
    expect(isRemoteImageSrc('https://example.com/a.png')).toBe(true);
    expect(isRemoteImageSrc('http://example.com/a.png')).toBe(true);
    // Protocol-relative: inherits the scheme but not the host.
    expect(isRemoteImageSrc('//example.com/a.png')).toBe(true);
    expect(isRemoteImageSrc('/api/artifacts/x/content')).toBe(false);
    expect(isRemoteImageSrc('relative/path.png')).toBe(false);
    expect(isRemoteImageSrc('data:image/png;base64,AAAA')).toBe(false);
    expect(isRemoteImageSrc('blob:http://localhost/abc')).toBe(false);
    // Same origin as the test environment's own location.
    expect(isRemoteImageSrc(`${window.location.origin}/a.png`)).toBe(false);
  });

  it('classifies remote sources by whether an <img> for them can load', () => {
    expect(remoteImageInfo('https://example.com/a.png')).toEqual({ host: 'example.com', loadable: true });
    expect(remoteImageInfo('http://example.com/a.png')).toEqual({ host: 'example.com', loadable: false });
    // Protocol-relative inherits this page's scheme, which on a loopback
    // server is http.
    expect(remoteImageInfo('//example.com/a.png').loadable).toBe(false);
    // Unparseable: no host to show, and nothing to be confident about.
    expect(remoteImageInfo('http://[nonsense').loadable).toBe(false);
  });

  // The two halves of SEC-09 have to be read together: this component decides
  // that a remote image is fetched only on request, and the page's CSP decides
  // whether that fetch is permitted at all. A CSP with no external img-src
  // source would not reinforce the click gate, it would cancel it. This is
  // here because jsdom cannot notice that by running the component.
  it('agrees with the SPA CSP about external images', () => {
    // process.cwd() is the Vitest root, i.e. packages/web -- import.meta.url
    // is not a file: URL under the jsdom environment.
    const html = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8');
    const csp = /content="(default-src[^"]*)"/.exec(html)?.[1];
    expect(csp).toBeDefined();
    const imgSrc = /img-src ([^;]*)/.exec(csp!)?.[1].trim().split(/\s+/);

    // https: is what makes the click actually load something.
    expect(imgSrc).toContain('https:');
    // http: is deliberately absent, which is what remoteImageInfo encodes.
    expect(imgSrc).not.toContain('http:');
    expect(imgSrc).not.toContain('*');
  });
});

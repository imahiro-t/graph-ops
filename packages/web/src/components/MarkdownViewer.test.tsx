// DFLT-00103 / SEC-09: an artifact is agent-authored, so a remote image URL
// inside one must not be fetched merely because the artifact was rendered --
// that fetch tells a third party the reader's IP and that they are looking at
// this artifact right now. Local sources (data:, same-origin paths) reach
// nobody and are rendered as usual.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { MarkdownViewer, isRemoteImageSrc } from './MarkdownViewer';

const REMOTE = 'https://tracker.example.com/pixel.png';

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

  it('loads the image once the reader clicks', async () => {
    render(<MarkdownViewer content={`![図の説明](${REMOTE})`} />);

    await userEvent.click(screen.getByRole('button'));

    const img = document.querySelector('img');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toBe(REMOTE);
    expect(img?.getAttribute('alt')).toBe('図の説明');
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
});

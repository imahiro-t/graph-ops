import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Image as ImageIcon } from 'lucide-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { StatusLiveRegion } from './StatusLiveRegion';

interface Props {
  content: string;
  // DFLT-00085: when true this bordered box itself becomes a 16rem-capped
  // scroll container, so inline artifact previews stay skimmable in a list.
  // The box that owns the border is the scroller on purpose (rather than an
  // outer wrapper) so the border stays put and only the content moves --
  // that fixed frame is what makes it read as "there is more inside".
  // Default false keeps the full-height render used by the ticket
  // description and by the open-in-a-new-tab preview page.
  scrollable?: boolean;
  // Accessible name for that scroll region. A keyboard user has to be able
  // to reach and scroll the box (WCAG 2.1.1), which means it takes focus --
  // and a focusable region needs a name saying which artifact it holds.
  label?: string;
}

// isRemoteImageSrc reports whether loading src would reach an origin other
// than the app's own.
//
// data: and blob: URLs carry their bytes with them, and a relative path
// resolves against this origin, so none of those tells anyone anything. An
// absolute http(s) URL does -- and so does a protocol-relative "//host/x",
// which is why this parses rather than string-matching "http". An
// unparseable src is treated as remote: the safe answer to "I don't know
// where this points" is not to fetch it.
export function isRemoteImageSrc(src: string): boolean {
  if (!src) return false;
  if (/^(data|blob):/i.test(src)) return false;
  try {
    return new URL(src, window.location.href).origin !== window.location.origin;
  } catch {
    return true;
  }
}

// remoteImageInfo describes a remote src: the host to show the reader, and
// whether an <img> for it can actually load under the SPA's CSP.
//
// index.html's img-src is `'self' data: blob: https:`. A cross-origin https
// URL is allowed (the reader's click is what gates it -- see RemoteImage);
// anything else remote -- plain http:, a protocol-relative "//host/x" that
// resolves to http: on this loopback origin, an unparseable src -- is not,
// and rendering an <img> for one would produce a control that silently does
// nothing. Knowing that here is what lets RemoteImage offer a link instead.
export function remoteImageInfo(src: string): { host: string; loadable: boolean } {
  try {
    const u = new URL(src, window.location.href);
    return { host: u.host || src, loadable: u.protocol === 'https:' };
  } catch {
    // Keep the raw src: an unparseable URL has no host to show, and the
    // reader is better served seeing exactly what is written than nothing.
    return { host: src, loadable: false };
  }
}

// RemoteImage never fetches a remote image on its own: it shows where the
// image would come from and waits for the reader to ask (DFLT-00103 /
// SEC-09).
//
// An artifact is written by an agent, and until DFLT-00103 merely opening
// one was enough to make the browser fetch every image URL in it. That is a
// request to someone else's server, carrying the reader's IP and the fact
// that they are looking at this artifact right now, with nobody having
// chosen to make it. Showing the host and waiting for a click puts that
// choice back where it belongs. This component -- not the page's CSP -- is
// where that rule lives; see index.html's img-src note for why the two have
// to agree rather than each try to enforce it.
//
// Three things the reviews of the first attempt asked for, all of which come
// from this being a control the reader deliberately operates:
//
//   - it must actually do something. An https image loads inline; anything
//     the CSP would refuse (remoteImageInfo's `loadable`) is offered as a
//     new-tab link from the start, never as a button that does nothing.
//   - focus must not be lost. Activating the button unmounts it, which would
//     drop focus to <body> and send the next Tab back to the top of the
//     document -- in the middle of what the reader was reading. The
//     pendingFocus + useEffect shape here is the same one
//     settings/LabelsEditor.tsx uses for the same reason.
//   - the outcome must reach assistive technology (WCAG 2.2 SC 4.1.3).
//     Loaded and failed are both announced through StatusLiveRegion, which
//     is mounted from the first render (empty) so the announcement is a text
//     change inside an existing live region rather than a new element.
//
// It is a real <button>, not a clickable div, so it is reachable and
// activatable from the keyboard, and its accessible name says both what the
// image is and where it would come from.
const RemoteImage: React.FC<{ src: string; alt?: string; title?: string }> = ({ src, alt, title }) => {
  const { t } = useTranslation();
  const { host, loadable } = remoteImageInfo(src);
  // 'asked' means the reader clicked and the <img> is mounted; it stays
  // 'asked' once the image has loaded. 'failed' is the onError landing.
  const [phase, setPhase] = useState<'idle' | 'asked' | 'failed'>('idle');
  const [announcement, setAnnouncement] = useState('');
  // What to focus once the next render has settled -- see the doc comment.
  const [pendingFocus, setPendingFocus] = useState<'image' | 'link' | null>(null);
  const imageRef = useRef<HTMLImageElement>(null);
  const linkRef = useRef<HTMLAnchorElement>(null);

  useEffect(() => {
    if (pendingFocus === null) return;
    (pendingFocus === 'image' ? imageRef.current : linkRef.current)?.focus();
    setPendingFocus(null);
    // phase is what mounts and unmounts the two targets, so the effect has
    // to re-run on it.
  }, [pendingFocus, phase]);

  const description = alt?.trim() || t('markdownViewer.untitledImage');

  // The link the reader is offered when an <img> is not an option: either
  // the CSP would refuse this src, or it was tried and failed. A top-level
  // navigation is not an img-src fetch, so this works in both cases -- and
  // it is still the reader's explicit action that leaves this origin.
  const openInNewTab = (
    <a
      ref={linkRef}
      href={src}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1.5 max-w-full text-left px-2 py-1 rounded border border-dashed border-slate-500 dark:border-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
    >
      <ImageIcon className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
      {/* The host is never truncated: it is the part the reader needs in
          order to decide, and a title tooltip would not reach a
          keyboard-only or touch user. The description gives way instead. */}
      <span className="shrink-0 text-indigo-600 dark:text-indigo-400">
        {t(phase === 'failed' ? 'markdownViewer.remoteImageFailedLink' : 'markdownViewer.openRemoteImage', { host })}
      </span>
      <span className="truncate min-w-0 text-slate-600 dark:text-slate-400">{description}</span>
    </a>
  );

  return (
    // One wrapper for every phase, so the live region below is mounted
    // before anything it has to announce happens.
    <span className="inline-flex max-w-full align-middle">
      <StatusLiveRegion message={announcement} />
      {phase === 'asked' ? (
        <img
          ref={imageRef}
          src={src}
          alt={alt || ''}
          title={title}
          // referrerPolicy as well as the page-level policy in index.html:
          // this one travels with the element, so it holds even if the
          // document is ever served without that header.
          referrerPolicy="no-referrer"
          // Not in the tab order (-1), but focusable, so the focus the
          // button held can land here instead of on <body>.
          tabIndex={-1}
          onLoad={() => setAnnouncement(t('markdownViewer.remoteImageLoaded', { description }))}
          onError={() => {
            setAnnouncement(t('markdownViewer.remoteImageFailed', { description, host }));
            setPhase('failed');
            setPendingFocus('link');
          }}
          className="max-w-full h-auto rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        />
      ) : phase === 'failed' || !loadable ? (
        openInNewTab
      ) : (
        <button
          type="button"
          onClick={() => {
            setPhase('asked');
            setPendingFocus('image');
          }}
          // border-slate-500/400 rather than 300/600: the dashed border is
          // the only thing marking this as operable, and SC 1.4.11 wants 3:1
          // against the box behind it (bg-white / dark:bg-slate-900). The old
          // pair measured 1.5:1 and 2.4:1; these measure 4.8:1 and 7.0:1. The
          // indigo label is the second signal, matching how a link reads in
          // this same viewer.
          className="inline-flex items-center gap-1.5 max-w-full text-left px-2 py-1 rounded border border-dashed border-slate-500 dark:border-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          <ImageIcon className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
          <span className="shrink-0 text-indigo-600 dark:text-indigo-400">
            {t('markdownViewer.loadRemoteImage', { host })}
          </span>
          <span className="truncate min-w-0 text-slate-600 dark:text-slate-400">{description}</span>
        </button>
      )}
    </span>
  );
};

// Tailwind styling per element (no @tailwindcss/typography plugin in this
// project -- see tailwind.config.js) so rendered markdown matches the app's
// existing slate/indigo look rather than browser defaults.
const components: Components = {
  // Only a remote image is deferred. A data:/blob: URI or a same-origin path
  // reaches nobody, so making the reader click those would be friction with
  // nothing bought for it.
  //
  // An empty src renders nothing rather than `<img src="">`, which some
  // browsers resolve to the page's own URL and request again. It happens for
  // any scheme react-markdown's own sanitization strips (data: among them),
  // so it is the normal path, not a corner case.
  img: ({ src, alt, title }) => {
    const source = typeof src === 'string' ? src : '';
    if (!source) return null;
    return isRemoteImageSrc(source) ? (
      <RemoteImage src={source} alt={alt} title={title} />
    ) : (
      <img src={source} alt={alt || ''} title={title} className="max-w-full h-auto rounded" />
    );
  },
  h1: ({ children }) => <h1 className="text-base font-bold text-slate-900 dark:text-slate-100 mt-3 mb-1.5 first:mt-0">{children}</h1>,
  h2: ({ children }) => <h2 className="text-sm font-bold text-slate-900 dark:text-slate-100 mt-3 mb-1.5 first:mt-0">{children}</h2>,
  h3: ({ children }) => <h3 className="text-xs font-bold text-slate-800 dark:text-slate-200 mt-2.5 mb-1 first:mt-0">{children}</h3>,
  h4: ({ children }) => <h4 className="text-xs font-bold text-slate-800 dark:text-slate-200 mt-2 mb-1 first:mt-0">{children}</h4>,
  p: ({ children }) => <p className="text-slate-700 dark:text-slate-300 leading-relaxed mb-2 last:mb-0">{children}</p>,
  ul: ({ children }) => <ul className="list-disc list-outside pl-5 space-y-0.5 mb-2">{children}</ul>,
  ol: ({ children }) => <ol className="list-decimal list-outside pl-5 space-y-0.5 mb-2">{children}</ol>,
  li: ({ children }) => <li className="text-slate-700 dark:text-slate-300">{children}</li>,
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer" className="text-indigo-600 hover:underline">
      {children}
    </a>
  ),
  // Fenced code blocks get a `language-*` className from remark/rehype;
  // inline `code` never does -- that's the only way to tell them apart
  // since react-markdown dropped the `inline` prop.
  code: ({ className, children, ...props }) => {
    const isBlock = /language-/.test(className || '');
    return isBlock ? (
      <code className={`block font-mono text-[11px] whitespace-pre ${className || ''}`} {...props}>
        {children}
      </code>
    ) : (
      <code className="font-mono text-[11px] bg-slate-100 dark:bg-slate-800 rounded px-1 py-0.5" {...props}>
        {children}
      </code>
    );
  },
  pre: ({ children }) => (
    <pre className="bg-slate-100 dark:bg-slate-800 rounded-lg p-2 overflow-x-auto mb-2">{children}</pre>
  ),
  blockquote: ({ children }) => (
    <blockquote className="border-l-4 border-indigo-200 dark:border-indigo-800 pl-3 text-slate-500 dark:text-slate-400 italic mb-2">{children}</blockquote>
  ),
  hr: () => <hr className="border-slate-200 dark:border-slate-800 my-3" />,
  table: ({ children }) => (
    <div className="overflow-x-auto mb-2">
      <table className="min-w-full border-collapse text-[11px]">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="bg-slate-100 dark:bg-slate-800">{children}</thead>,
  th: ({ children }) => (
    <th className="border border-slate-200 dark:border-slate-700 px-2 py-1 text-left font-bold text-slate-700 dark:text-slate-300">{children}</th>
  ),
  td: ({ children }) => <td className="border border-slate-200 dark:border-slate-700 px-2 py-1 text-slate-700 dark:text-slate-300">{children}</td>,
  strong: ({ children }) => <strong className="font-bold text-slate-900 dark:text-slate-100">{children}</strong>
};

// Markdown preview for `text`-type artifacts (plans, investigation reports,
// etc.), the same role GherkinViewer plays for `gherkin`-type artifacts.
// react-markdown renders straight to React elements (no
// dangerouslySetInnerHTML/raw HTML pass-through), so arbitrary
// Claude-authored markdown can't smuggle in a stored-XSS payload the way a
// naive marked()+innerHTML render could.
export const MarkdownViewer: React.FC<Props> = ({ content, scrollable = false, label }) => {
  return (
    <div
      data-testid="markdown-viewer"
      // max-h-64 (not h-64) so a short artifact still renders at its own
      // height instead of being stretched to 16rem with dead space below.
      // blue-500 focus ring rather than indigo-400 -- see GherkinViewer for
      // the contrast numbers; outline-none leaves the ring as the only focus
      // indicator, so it has to clear 3:1 on the light theme too.
      className={`bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-3 text-xs overflow-x-auto shadow-inner${
        scrollable ? ' max-h-64 overflow-y-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500' : ''
      }`}
      tabIndex={scrollable ? 0 : undefined}
      role={scrollable && label ? 'region' : undefined}
      aria-label={scrollable ? label : undefined}
    >
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </ReactMarkdown>
    </div>
  );
};

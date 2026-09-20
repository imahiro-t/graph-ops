import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Image as ImageIcon } from 'lucide-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';

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

// RemoteImage renders a remote <img> only after the reader asks for it
// (DFLT-00103 / SEC-09).
//
// An artifact is written by an agent, and until DFLT-00103 merely opening
// one was enough to make the browser fetch every image URL in it. That is a
// request to someone else's server, carrying the reader's IP and the fact
// that they are looking at this artifact right now, with nobody having
// chosen to make it. Showing the host and waiting for a click puts that
// choice back where it belongs.
//
// It is a real <button>, not a clickable div: this has to be reachable and
// activatable from the keyboard, and its accessible name has to say both
// what the image is and where it would come from.
const RemoteImage: React.FC<{ src: string; alt?: string; title?: string }> = ({ src, alt, title }) => {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState(false);

  if (loaded) {
    return <img src={src} alt={alt || ''} title={title} className="max-w-full h-auto rounded" />;
  }

  let host = src;
  try {
    host = new URL(src, window.location.href).host || src;
  } catch {
    // Keep the raw src: an unparseable URL has no host to show, and the
    // reader is better served seeing exactly what is written than nothing.
  }
  const description = alt?.trim() || t('markdownViewer.untitledImage');

  return (
    <button
      type="button"
      onClick={() => setLoaded(true)}
      title={src}
      className="inline-flex items-center gap-1.5 max-w-full text-left px-2 py-1 rounded border border-dashed border-slate-300 dark:border-slate-600 text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
    >
      <ImageIcon className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
      <span className="truncate">{t('markdownViewer.loadRemoteImage', { description, host })}</span>
    </button>
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

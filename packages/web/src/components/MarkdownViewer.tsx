import React from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';

interface Props {
  content: string;
}

// Tailwind styling per element (no @tailwindcss/typography plugin in this
// project -- see tailwind.config.js) so rendered markdown matches the app's
// existing slate/indigo look rather than browser defaults.
const components: Components = {
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
export const MarkdownViewer: React.FC<Props> = ({ content }) => {
  return (
    <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-3 text-xs overflow-x-auto shadow-inner">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </ReactMarkdown>
    </div>
  );
};

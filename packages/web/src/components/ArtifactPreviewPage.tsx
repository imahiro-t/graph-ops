import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { FileCode, FileText, Code2, Loader2, AlertTriangle } from 'lucide-react';
import { GherkinViewer } from './GherkinViewer';
import { MarkdownViewer } from './MarkdownViewer';
import { errorMessage } from '../lib/apiError';

// Standalone page for "open in new tab" on a gherkin/text/html artifact (see
// TicketItem.tsx's openInNewTabLink): a plain GET /api/artifacts/{id}/content
// link would only ever show raw, unformatted text in the new tab for
// gherkin/text, since that endpoint deliberately never serves text/gherkin
// content as HTML (multiple past security reviews hardened GET
// /api/artifacts/{id}/content against exactly that -- see
// internal/artifactcontent/content.go's contentTypeFor). Rendering the same
// formatted preview client-side, with the same GherkinViewer/MarkdownViewer
// components the main screen uses, gets the requested "same as the original
// screen" formatting without touching that hardened endpoint at all.
//
// For html it's the opposite problem: opening the content URL directly used
// to render agent-authored HTML as a same-origin, unauthenticated-yet-
// privileged top-level document (DFLT-00053). Instead this page itself is
// what a new tab loads first -- an authenticated, same-origin SPA route --
// and it embeds the actual artifact in a sandboxed <iframe>, exactly like the
// inline preview on the main ticket screen already does. That gives it two
// independent layers of isolation: the iframe's own sandbox="allow-scripts"
// attribute, plus the Content-Security-Policy: sandbox allow-scripts header
// the content endpoint now always sends (see writeArtifactContent).
//
// Reached via main.tsx's tiny path-based switch, at /artifacts/{id}/preview
// ?type=<gherkin|text|html>&name=<artifact name>. type/name are passed as
// query params (rather than fetched) because the artifact-list response the
// link is built from already has them on hand -- no extra request needed.
export const ArtifactPreviewPage: React.FC = () => {
  const { t } = useTranslation();
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState('');

  const segments = window.location.pathname.split('/').filter(Boolean);
  // pathname is "/artifacts/{id}/preview"
  const artifactId = segments[1] || '';
  const params = new URLSearchParams(window.location.search);
  const type = params.get('type') || 'text';
  const name = params.get('name') || artifactId;
  const isHtml = type === 'html';

  useEffect(() => {
    // html never needs its bytes fetched here -- the sandboxed <iframe>
    // below points straight at the content endpoint and loads them itself.
    if (isHtml) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch(`/api/artifacts/${artifactId}/content`);
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }
        const text = await res.text();
        if (!cancelled) setContent(text);
      } catch (e) {
        if (!cancelled) setError(errorMessage(e, String(e)));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [artifactId, isHtml]);

  return (
    <div
      className={
        isHtml
          ? 'h-screen flex flex-col bg-slate-50 dark:bg-slate-950 text-slate-900 dark:text-slate-100 font-sans'
          : 'min-h-screen bg-slate-50 dark:bg-slate-950 text-slate-900 dark:text-slate-100 font-sans'
      }
    >
      <header className="bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 px-6 py-3.5 shadow-xs shrink-0">
        <div className={isHtml ? 'flex items-center gap-2' : 'max-w-4xl mx-auto flex items-center gap-2'}>
          {isHtml ? (
            <Code2 className="w-4 h-4 text-emerald-500 shrink-0" />
          ) : type === 'gherkin' ? (
            <FileCode className="w-4 h-4 text-amber-500 shrink-0" />
          ) : (
            <FileText className="w-4 h-4 text-indigo-500 shrink-0" />
          )}
          <span className="font-bold text-sm text-slate-900 dark:text-slate-100 truncate">{name}</span>
        </div>
      </header>

      {isHtml ? (
        // sandbox="allow-scripts" (no allow-same-origin) keeps this iframe's
        // document on an opaque origin, same as the inline preview on the
        // main ticket screen -- it can run its own scripts but can't reach
        // this SPA's origin, cookies, or APIs.
        <iframe
          src={`/api/artifacts/${artifactId}/content`}
          sandbox="allow-scripts"
          title={name}
          className="flex-1 w-full border-0 bg-white"
        />
      ) : (
        <main className="max-w-4xl mx-auto px-6 py-6">
          {error ? (
            <div className="flex items-center gap-2 text-red-600 dark:text-red-300 text-sm bg-red-50 dark:bg-red-950 border border-red-200 dark:border-red-900 rounded-lg p-4">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              {t('artifactPreview.loadError', { message: error })}
            </div>
          ) : content === null ? (
            <div className="flex items-center gap-2 text-slate-400 dark:text-slate-500 text-sm p-4">
              <Loader2 className="w-4 h-4 animate-spin" />
              {t('artifactPreview.loading')}
            </div>
          ) : type === 'gherkin' ? (
            <GherkinViewer content={content} />
          ) : (
            <MarkdownViewer content={content} />
          )}
        </main>
      )}
    </div>
  );
};

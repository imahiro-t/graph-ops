import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import { ArtifactPreviewPage } from './components/ArtifactPreviewPage';
import './index.css';
import './i18n';

// The app has no general-purpose client-side router -- this is the one
// deep-linkable exception: /artifacts/{id}/preview, opened in a new tab by
// TicketItem.tsx's openInNewTabLink for gherkin, text and html artifacts, so
// they get the same formatted preview the main screen shows instead of raw
// content (see ArtifactPreviewPage's doc comment for why that's a separate page
// rather than reusing GET /api/artifacts/{id}/content directly). The Go
// server's staticWebHandler serves index.html for this path too (SPA
// fallback), so a fresh navigation/new-tab open reaches this same check.
const isArtifactPreviewRoute = /^\/artifacts\/[^/]+\/preview\/?$/.test(window.location.pathname);

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {isArtifactPreviewRoute ? <ArtifactPreviewPage /> : <App />}
  </React.StrictMode>
);

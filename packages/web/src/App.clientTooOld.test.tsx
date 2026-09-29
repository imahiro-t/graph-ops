// DFLT-00331: a Web UI server whose graph-engine the database has moved past
// answers its API with 503 CLIENT_TOO_OLD. The ticket list's own failures
// only go to the console, so the app says so in a banner -- the localized
// errors.CLIENT_TOO_OLD, telling the viewer to update graph-engine and
// restart the UI server -- and takes it down again once the list loads.
import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const project: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

let backend: FakeBackend;
let tooOld: boolean;

function tooOldResponse() {
  return new Response(
    JSON.stringify({
      error: {
        code: 'CLIENT_TOO_OLD',
        message: 'CLIENT_TOO_OLD: update graph-engine',
        details: { db_schema_version: 2, min_client_schema_version: 2, client_schema_version: 1 }
      }
    }),
    { status: 503 }
  );
}

beforeEach(() => {
  backend = createFakeBackend({
    projects: [project],
    currentProjectId: project.id,
    labels: [],
    tickets: [{ id: 'ALP-00001', project_id: project.id, title: 'Alpha のチケット', status: 'TODO', priority: 'HIGH', labelIds: [] }]
  });
  installFakeBackend(backend);
  tooOld = true;
  // The server is "too old" for the ticket list while tooOld holds; the
  // rest of the fake backend answers as usual.
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (tooOld && url.startsWith('/api/tickets?')) return Promise.resolve(tooOldResponse());
      return backend.fetch(input, init);
    })
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('CLIENT_TOO_OLD banner', () => {
  it.each(['en', 'ja'])('explains in %s why nothing loads', async lang => {
    await i18n.changeLanguage(lang);
    render(<App />);

    const alert = await screen.findByText(i18n.t('errors.CLIENT_TOO_OLD'));
    expect(alert).toHaveAttribute('role', 'alert');
    expect(alert.textContent).toContain('graph-engine');
    expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();
  });

  it('goes away once the list loads again', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    render(<App />);
    await screen.findByText(i18n.t('errors.CLIENT_TOO_OLD'));

    // Updated and restarted: the next poll gets the list.
    tooOld = false;
    await vi.advanceTimersByTimeAsync(15_000);
    await screen.findByText('ALP-00001');
    await waitFor(() => expect(screen.queryByText(i18n.t('errors.CLIENT_TOO_OLD'))).not.toBeInTheDocument());
  });

  it('is not shown for other failures of the list', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.startsWith('/api/tickets?')) {
          return Promise.resolve(new Response(JSON.stringify({ error: { code: 'INTERNAL_ERROR', message: 'boom' } }), { status: 500 }));
        }
        return backend.fetch(input, init);
      })
    );
    render(<App />);
    await waitFor(() => expect(screen.queryByText(i18n.t('emptyState.loadingTickets'))).not.toBeInTheDocument());
    expect(screen.queryByText(i18n.t('errors.CLIENT_TOO_OLD'))).not.toBeInTheDocument();
  });
});

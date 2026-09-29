// Ways for App tests to wait for an answer from the fake backend that
// nothing on screen always shows has arrived (DFLT-00296).
//
// App fetches several things independently at startup (the project list,
// the autopilot runs, the page size, ...), so seeing the first ticket does
// not imply that any of the others has been answered. A test that relies on
// one of them waits for it explicitly, through one of these helpers, rather
// than keeping its own copy of the waiting logic.
import { act, screen, waitFor } from '@testing-library/react';
import { expect } from 'vitest';
import i18n from '../i18n';
import { FakeBackend } from './fakeBackend';

// Picks the requests to track, by path and HTTP method.
export type RequestMatch = (url: string, method: string) => boolean;

// Counts the requests `match` picks and the answers whose body the app has
// finished reading (by wrapping each matching Response's json()). Call it
// after the backend is created and before the requests are made: it wraps
// `backend.fetch`, and several trackers on one backend stack.
//
// The returned function waits until at least one matching request has been
// made and every one made so far has had its body read, then lets the last
// answer reach the state and the render.
export function trackBodyReads(backend: FakeBackend, match: RequestMatch): () => Promise<void> {
  let made = 0;
  let read = 0;
  const inner = backend.fetch.bind(backend);
  backend.fetch = async (input, init) => {
    if (!match(String(input), init?.method ?? 'GET')) return inner(input, init);
    made++;
    let res: Response;
    try {
      res = await inner(input, init);
    } catch (err) {
      // A request that fails before answering leaves no body to read.
      read++;
      throw err;
    }
    const json = res.json.bind(res);
    res.json = async () => {
      try {
        return await json();
      } finally {
        read++;
      }
    };
    return res;
  };
  return async function bodiesRead() {
    await waitFor(() => {
      expect(made).toBeGreaterThan(0);
      expect(read).toBe(made);
    });
    await act(async () => {
      await new Promise(r => setTimeout(r, 0));
    });
  };
}

// The pager is drawn only once the page size has arrived, which is its own
// request (GET /api/settings/app): seeing a ticket does not imply it has
// been answered.
export const findPreviousPage = () => screen.findByRole('button', { name: i18n.t('pagination.previous') });

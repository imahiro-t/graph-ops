// Shared helpers for App tests to wait until an answer from the fake backend
// has arrived (DFLT-00296).
//
// App fetches several things independently at startup (the project list,
// the autopilot runs, the page size, ...), so seeing the first ticket does
// not imply that any of the others has been answered. A test that relies on
// one of them waits for it explicitly, through one of these helpers, rather
// than keeping its own copy of the waiting logic. There are two ways to wait:
//
// - For an answer that nothing on screen always shows has arrived, wait
//   until the app has read its body, with trackBodyReads.
// - For an answer that draws something only once it has arrived, wait for
//   that element to appear, with a finder such as findPreviousPage (the
//   pager, drawn once the page size has arrived).
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
//
// Only answers the app always reads with json() may be tracked, since the
// count of bodies read moves only when json() finishes. If the app leaves
// the body of even one matching answer unread (for example a non-2xx answer
// it turns into an error without calling json(), or one it reads some other
// way), the returned function never sees every body read and keeps waiting
// until waitFor times out. So `match` must pick only answers whose body the
// app always reads; keep error answers out of it. (A request that fails
// before answering is a different case: it is counted as read, see below.)
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

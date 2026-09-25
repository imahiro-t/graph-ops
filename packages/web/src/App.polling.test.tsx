// DFLT-00112: what the 15s poll actually costs, and when it runs at all.
//
// A round used to be "GET /api/tickets plus one GET /api/tickets/{id} per
// listed ticket", so 100 tickets meant 101 requests every 15 seconds -- per
// open tab, for every viewer of a shared backend -- and each detail response
// re-sent the full text of every stored plan and review verdict. It is now a
// single list request (the list carries nodes/edges) plus at most one detail
// request per *expanded* ticket, and nothing at all while the tab is hidden.
//
// The request-count assertions below deliberately count *every* fetch of a
// round, not just the ones aimed at /api/tickets: a round also refreshes the
// current project's labels (App.tsx's lastFetchedAt effect), and counting
// only ticket requests would let a per-ticket fan-out reappear elsewhere
// without any test noticing. That label request is one fixed request no
// matter how many tickets exist, which is what "the count does not depend on
// the number of tickets" means here. Since DFLT-00142 a round also refreshes
// the project's autopilot runs (the badges), another single fixed request.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App, { POLL_INTERVAL_MS, mergeTicketSummaries } from './App';
import { Artifact, GraphNode, Project, TicketDetail, TicketGraph } from './types';
import { FakeBackend, FakeTicket, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const project: Project = {
  id: 'p-default',
  name: 'Default',
  prefix: 'DFLT',
  local_path: '/work/default',
  created_at: '',
  updated_at: ''
};

const LABELS_URL = `/api/projects/${project.id}/labels`;
// Since DFLT-00106 the list request always names the header's project; a
// bare GET /api/tickets returns an empty list.
const LIST_URL = `/api/tickets?project_id=${project.id}`;
const RUNS_URL = `/api/autopilot/runs?project_id=${project.id}`;

// A ticket with a three-node graph (one of them done) and two artifacts,
// i.e. everything a collapsed card and an expanded panel each read.
function ticketWithGraph(n: number): FakeTicket {
  const id = `DFLT-0000${n}`;
  return {
    id,
    project_id: project.id,
    title: `チケット${n}`,
    status: 'IN PROGRESS',
    priority: 'MEDIUM',
    labelIds: [],
    nodes: [
      { id: `${id}-01`, name: '計画作成', type: 'plan', status: 'DONE' },
      { id: `${id}-02`, name: '実装', type: 'implementation', status: 'IN PROGRESS' },
      { id: `${id}-03`, name: 'リリース承認', type: 'approval_gate', status: 'TODO' }
    ],
    edges: [
      { id: `${id}-e1`, from: `${id}-01`, to: `${id}-02` },
      { id: `${id}-e2`, from: `${id}-02`, to: `${id}-03` }
    ],
    artifacts: [
      { id: `art-${id}-1`, node_id: `${id}-01`, name: '実行計画', type: 'text', content: '計画の本文' },
      { id: `art-${id}-2`, node_id: `${id}-02`, name: '実装メモ', type: 'text', content: '実装メモの本文' }
    ]
  };
}

let backend: FakeBackend;
let fetchMock: ReturnType<typeof vi.fn>;
let visibility: DocumentVisibilityState;

function seed(ticketCount: number) {
  backend = createFakeBackend({
    projects: [project],
    currentProjectId: project.id,
    labels: [{ id: 'label-ui', project_id: project.id, name: 'UI改善', color: 'green' }],
    tickets: Array.from({ length: ticketCount }, (_, i) => ticketWithGraph(i + 1))
  });
  fetchMock = installFakeBackend(backend);
}

// These tests own the clock (the whole point is what happens at 15-second
// boundaries), so they settle pending work by draining timers/microtasks
// explicitly rather than with waitFor, which polls on its own timers and
// would never resolve against a faked clock.
async function flush(times = 12) {
  for (let i = 0; i < times; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
  }
}

async function renderApp() {
  render(<App />);
  // Startup: the ticket fetch, and the label refresh that trails it by a
  // state update. Settling both here keeps them out of the per-round counts.
  await flush();
  expect(screen.getByText('DFLT-00001')).toBeInTheDocument();
  expect(requestedUrls()).toContain(LABELS_URL);
}

// Expands (or collapses) a ticket card by clicking its header.
async function toggleTicket(id: string) {
  await act(async () => {
    fireEvent.click(screen.getByText(id));
  });
  await flush();
}

const requestedUrls = () => fetchMock.mock.calls.map(c => String(c[0]));
const urlsSince = (mark: number) => requestedUrls().slice(mark);
const countOf = (urls: string[], url: string) => urls.filter(u => u === url).length;
const detailRequests = (urls: string[]) => urls.filter(u => u.startsWith('/api/tickets/'));

// Runs exactly one polling interval and lets everything it triggers settle.
async function advanceOneRound() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(POLL_INTERVAL_MS);
  });
}

async function setVisibility(state: DocumentVisibilityState) {
  visibility = state;
  await act(async () => {
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  // jsdom has no ResizeObserver, and an expanded ticket panel measures its
  // node list with one (TicketItem.tsx). Undone by unstubAllGlobals below.
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  );
  visibility = 'visible';
  // document.visibilityState is read-only in jsdom; shadow it with an own
  // property and remove that again in afterEach so nothing leaks into the
  // next test.
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
});

afterEach(() => {
  Reflect.deleteProperty(document, 'visibilityState');
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('polling cost', () => {
  // Completion criterion 1: the number of requests per round does not depend
  // on how many tickets there are. Three tickets and twelve cost the same.
  it.each([3, 12])('makes the same number of requests per round with %i tickets', async ticketCount => {
    seed(ticketCount);
    await renderApp();

    const mark = fetchMock.mock.calls.length;
    await advanceOneRound();
    const urls = urlsSince(mark);

    expect(urls).toHaveLength(3);
    expect(countOf(urls, LIST_URL)).toBe(1);
    expect(countOf(urls, LABELS_URL)).toBe(1);
    expect(countOf(urls, RUNS_URL)).toBe(1);
    expect(detailRequests(urls)).toEqual([]);
  });

  // ...and one expanded ticket adds exactly one detail request, not one per
  // ticket.
  it('adds one detail request per expanded ticket and no more', async () => {
    seed(5);
    await renderApp();

    await toggleTicket('DFLT-00002');
    expect(requestedUrls()).toContain('/api/tickets/DFLT-00002');

    const mark = fetchMock.mock.calls.length;
    await advanceOneRound();
    const urls = urlsSince(mark);

    expect(urls).toHaveLength(4);
    expect(countOf(urls, LIST_URL)).toBe(1);
    expect(countOf(urls, LABELS_URL)).toBe(1);
    expect(countOf(urls, RUNS_URL)).toBe(1);
    expect(detailRequests(urls)).toEqual(['/api/tickets/DFLT-00002']);
  });

  // Expanding must not wait for the next round: the artifact tabs would be
  // empty for up to 15 seconds.
  it('fetches the detail immediately when a ticket is expanded', async () => {
    seed(3);
    await renderApp();

    const mark = fetchMock.mock.calls.length;
    await toggleTicket('DFLT-00001');

    expect(detailRequests(urlsSince(mark))).toEqual(['/api/tickets/DFLT-00001']);
  });
});

describe('artifacts', () => {
  // Completion criterion 2 + 4: the polled list carries no artifact bodies,
  // yet an expanded ticket's artifacts stay on screen across a round (the
  // merge in mergeTicketSummaries) instead of blinking out every 15s.
  it('keeps an expanded ticket’s artifacts across a polling round', async () => {
    seed(3);
    await renderApp();
    const artifactsTab = () => screen.getByRole('button', { name: i18n.t('ticketItem.tabs.artifacts', { count: 2 }) });

    await toggleTicket('DFLT-00001');
    expect(artifactsTab()).toBeInTheDocument();

    // Let the round's detail request hang, so the assertion lands in the
    // window between the (artifact-free) list response and the detail
    // response -- exactly where a missing merge would blank the panel, and
    // where a quickly-resolved detail request would paper over it.
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) =>
      String(input).startsWith('/api/tickets/') ? new Promise<Response>(() => {}) : backend.fetch(input, init)
    );

    await advanceOneRound();
    expect(detailRequests(requestedUrls())).toContain('/api/tickets/DFLT-00001');
    expect(artifactsTab()).toBeInTheDocument();

    // The list response itself never carried them: the round's ticket
    // request must not have returned any artifact body. (It must have
    // returned the tickets, though, or this would pass on an empty list.)
    const listResponse = await backend.fetch(LIST_URL);
    const listBody = await listResponse.text();
    expect(listBody).toContain('DFLT-00001');
    expect(listBody).not.toContain('実装メモの本文');
  });

  it('mergeTicketSummaries carries artifacts over by id and drops unlisted tickets', () => {
    const artifact = { id: 'art-1', ticket_id: 'DFLT-00001', node_id: 'n1', name: 'a', type: 'text' } as Artifact;
    const prev = [
      { id: 'DFLT-00001', nodes: [], edges: [], artifacts: [artifact] },
      { id: 'DFLT-00009', nodes: [], edges: [], artifacts: [artifact] }
    ] as unknown as TicketDetail[];
    const summaries = [
      { id: 'DFLT-00001', nodes: [{ id: 'n1' } as GraphNode], edges: [] },
      { id: 'DFLT-00002', nodes: [], edges: [] }
    ] as unknown as TicketGraph[];

    const merged = mergeTicketSummaries(prev, summaries);

    expect(merged.map(t => t.id)).toEqual(['DFLT-00001', 'DFLT-00002']);
    expect(merged[0].artifacts).toEqual([artifact]);
    expect(merged[0].nodes).toHaveLength(1);
    // A ticket nobody has expanded yet simply has none.
    expect(merged[1].artifacts).toEqual([]);
  });
});

describe('background tabs', () => {
  // Completion criterion 3: a hidden tab polls nothing at all...
  it('makes no request at all while the tab is hidden', async () => {
    seed(3);
    await renderApp();

    await setVisibility('hidden');
    const mark = fetchMock.mock.calls.length;
    await advanceOneRound();
    await advanceOneRound();

    expect(urlsSince(mark)).toEqual([]);
  });

  // ...and coming back fetches once right away rather than leaving a
  // 15-second-stale view on screen, then resumes the interval from there.
  it('fetches once immediately when the tab becomes visible again, then resumes', async () => {
    seed(3);
    await renderApp();

    await setVisibility('hidden');
    await advanceOneRound();

    const mark = fetchMock.mock.calls.length;
    await setVisibility('visible');
    await flush();
    expect(countOf(urlsSince(mark), LIST_URL)).toBe(1);
    expect(countOf(urlsSince(mark), LABELS_URL)).toBe(1);

    const resumeMark = fetchMock.mock.calls.length;
    await advanceOneRound();
    expect(countOf(urlsSince(resumeMark), LIST_URL)).toBe(1);
  });
});

describe('collapsed cards', () => {
  // Completion criterion 4: everything a collapsed card shows comes from the
  // list response alone, now that no detail is fetched for it -- the node
  // progress count, the per-node status ticks, and the dashboard total
  // across every ticket (not just the visible page).
  it('renders progress and node ticks from the list response alone', async () => {
    seed(3);
    await renderApp();

    expect(detailRequests(requestedUrls())).toEqual([]);

    // One of three nodes done, on each card...
    expect(screen.getAllByText('1/3')).toHaveLength(3);
    // ...and the dashboard's total over every ticket, which is computed from
    // the same nodes.
    expect(screen.getByText('3/9')).toBeInTheDocument();
    expect(screen.getAllByTitle(/^計画作成 \(/)).toHaveLength(3);
    expect(screen.getAllByTitle(/^実装 \(/)).toHaveLength(3);
    // The approval gate the ticket is not at yet is shown as a plain TODO
    // tick, not as the pending (blinking) one -- that judgment needs the
    // edges, which now come with the list.
    expect(screen.getAllByTitle(`リリース承認 (${i18n.t('status.todo')})`)).toHaveLength(3);
  });
});

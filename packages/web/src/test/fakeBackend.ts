// A small in-memory stand-in for the backend, for the tests that need App's
// own state rather than a single component's (App.labels.test.tsx,
// App.filters.test.tsx).
//
// It answers the endpoints App calls on startup and resolves labels onto
// tickets by id at read time, as the real store does, so a change made
// through the UI is visible in the next read. It was extracted here in
// DFLT-00086 when a second App test needed it: keeping a copy per test file
// would have meant fixing every API change twice.
//
// Mutating `backend.tickets` / `backend.labels` / `backend.currentProjectId`
// between renders is the intended way to simulate a change on the server.
import { vi } from 'vitest';
import { ArtifactType, LabelColor, NodeStatus, NodeType, Project, TicketPriority, TicketStatus } from '../types';

export interface FakeLabel {
  id: string;
  project_id: string;
  name: string;
  color: LabelColor;
}

// A ticket's execution graph and artifacts, seeded per ticket. Only the
// fields the UI actually reads have to be given; the rest are filled in with
// plausible defaults by the *JSON helpers below.
export interface FakeNode {
  id: string;
  name?: string;
  type?: NodeType;
  status: NodeStatus;
}

export interface FakeEdge {
  id: string;
  from: string;
  to: string;
  condition?: string;
}

export interface FakeArtifact {
  id: string;
  node_id: string;
  name: string;
  type: ArtifactType;
  content?: string;
}

export interface FakeTicket {
  id: string;
  project_id: string;
  title: string;
  status: TicketStatus;
  priority: TicketPriority;
  // Absent/empty is "unassigned", the same three shapes the API can return.
  assignee?: string | null;
  labelIds: string[];
  // Default to empty, so the tests that predate DFLT-00112 keep seeding
  // graph-less tickets without saying so.
  nodes?: FakeNode[];
  edges?: FakeEdge[];
  artifacts?: FakeArtifact[];
}

export interface FakeBackendSeed {
  projects: Project[];
  labels: FakeLabel[];
  tickets: FakeTicket[];
  currentProjectId: string;
  // GET /api/settings/app's effective paginationPageSize, i.e. how many
  // tickets a page of the list holds. Defaults to the app's own default.
  paginationPageSize?: number;
}

export interface FakeBackend extends Required<FakeBackendSeed> {
  fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>;
}

function respond(status: number, body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status }));
}

export function createFakeBackend(seed: FakeBackendSeed): FakeBackend {
  const labelJSON = (l: FakeLabel) => ({ ...l, created_at: '', updated_at: '' });

  const nodeJSON = (tk: FakeTicket, n: FakeNode) => ({
    id: n.id,
    ticket_id: tk.id,
    name: n.name ?? n.id,
    type: n.type ?? 'implementation',
    status: n.status,
    iteration_count: 0,
    max_iterations: 3,
    is_manual: (n.type ?? 'implementation') === 'approval_gate',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z'
  });

  const edgeJSON = (tk: FakeTicket, e: FakeEdge) => ({
    id: e.id,
    ticket_id: tk.id,
    from_node_id: e.from,
    to_node_id: e.to,
    condition: e.condition ?? 'always',
    created_at: '2026-01-01T00:00:00Z'
  });

  const artifactJSON = (tk: FakeTicket, a: FakeArtifact) => ({
    id: a.id,
    ticket_id: tk.id,
    node_id: a.node_id,
    name: a.name,
    type: a.type,
    content: a.content ?? null,
    has_content: a.content != null,
    created_at: '2026-01-01T00:00:00Z'
  });

  // GET /api/tickets' element (domain.TicketGraph): the ticket plus its
  // graph, with no "artifacts" key at all -- DFLT-00112 moved the artifacts
  // out of the polled list and into the per-ticket detail below, so a fake
  // that still answered with them would let a regression pass unnoticed.
  const ticketListJSON = (tk: FakeTicket) => ({
    id: tk.id,
    project_id: tk.project_id,
    title: tk.title,
    description: '',
    status: tk.status,
    assignee: tk.assignee ?? null,
    auto_executable: true,
    blocked: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    priority: tk.priority,
    labels: backend.labels
      .filter(l => tk.labelIds.includes(l.id))
      .map(labelJSON)
      .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())),
    nodes: (tk.nodes ?? []).map(n => nodeJSON(tk, n)),
    edges: (tk.edges ?? []).map(e => edgeJSON(tk, e))
  });

  // GET /api/tickets/{id}'s response (domain.TicketDetail): the same thing
  // plus the artifacts, bodies included.
  const ticketDetailJSON = (tk: FakeTicket) => ({
    ...ticketListJSON(tk),
    artifacts: (tk.artifacts ?? []).map(a => artifactJSON(tk, a))
  });

  const backend: FakeBackend = {
    projects: seed.projects,
    labels: seed.labels,
    tickets: seed.tickets,
    currentProjectId: seed.currentProjectId,
    paginationPageSize: seed.paginationPageSize ?? 10,

    async fetch(input, init) {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      let m: RegExpMatchArray | null;

      // GET /api/tickets is scoped by the CALLER's query, never by the
      // backend's current project (DFLT-00106): ?project_id=<id> filters,
      // ?all=true returns everything and wins over project_id, and neither
      // returns an empty list. Filtering by backend.currentProjectId here --
      // which is what this used to do -- would hide the very bug the app's
      // tests now guard against, since an unscoped request would still come
      // back with the right project's tickets.
      if (url.split('?')[0] === '/api/tickets' && method === 'GET') {
        const query = new URLSearchParams(url.split('?')[1] ?? '');
        if (query.get('all') === 'true') return respond(200, backend.tickets.map(ticketListJSON));
        const projectId = query.get('project_id') ?? '';
        if (!projectId) return respond(200, []);
        return respond(200, backend.tickets.filter(tk => tk.project_id === projectId).map(ticketListJSON));
      }
      if ((m = url.match(/^\/api\/tickets\/([^/?]+)$/)) && method === 'GET') {
        const id = decodeURIComponent(m[1]);
        const tk = backend.tickets.find(x => x.id === id);
        return tk ? respond(200, ticketDetailJSON(tk)) : respond(404, { error: { code: 'TICKET_NOT_FOUND', message: '' } });
      }
      if (url === '/api/projects' && method === 'GET') return respond(200, backend.projects);
      if (url === '/api/current-project' && method === 'GET') {
        return respond(200, backend.projects.find(p => p.id === backend.currentProjectId) ?? null);
      }
      if (url === '/api/current-project' && method === 'PUT') {
        backend.currentProjectId = body.project_id;
        return respond(200, backend.projects.find(p => p.id === backend.currentProjectId));
      }
      if (url === '/api/settings/app') {
        return respond(200, {
          file: { paginationPageSize: backend.paginationPageSize, myName: '' },
          effective: { paginationPageSize: backend.paginationPageSize },
          config_path: ''
        });
      }
      if ((m = url.match(/^\/api\/projects\/([^/]+)\/labels$/)) && method === 'GET') {
        const projectId = decodeURIComponent(m[1]);
        return respond(
          200,
          backend.labels
            .filter(l => l.project_id === projectId)
            .map(l => ({
              ...labelJSON(l),
              ticket_count: backend.tickets.filter(tk => tk.labelIds.includes(l.id)).length
            }))
        );
      }
      if ((m = url.match(/^\/api\/labels\/([^/]+)$/))) {
        const id = decodeURIComponent(m[1]);
        const label = backend.labels.find(l => l.id === id);
        if (!label) return respond(404, { error: { code: 'LABEL_NOT_FOUND', message: '' } });
        if (method === 'PATCH') {
          Object.assign(label, body);
          return respond(200, labelJSON(label));
        }
        if (method === 'DELETE') {
          const removed = backend.tickets.filter(tk => tk.labelIds.includes(id)).length;
          backend.tickets.forEach(tk => (tk.labelIds = tk.labelIds.filter(x => x !== id)));
          backend.labels = backend.labels.filter(l => l.id !== id);
          return respond(200, { success: true, removed_ticket_count: removed });
        }
      }
      if (url.startsWith('/api/settings/node-types')) return respond(200, { types: [] });
      // The create-ticket flow launches an external terminal and returns
      // immediately; nothing is created here. It is answered so a test can
      // drive that flow and watch the refetch it triggers afterwards.
      if (url === '/api/claude/launch' && method === 'POST') return respond(200, { success: true });
      return respond(404, { error: { code: 'NOT_FOUND', message: url } });
    }
  };

  return backend;
}

// Points global fetch at the backend and stubs matchMedia (useTheme reads it
// and jsdom doesn't implement it). Call from beforeEach, with
// vi.unstubAllGlobals() in afterEach. Returns the fetch spy, for tests that
// assert on which requests were made.
export function installFakeBackend(backend: FakeBackend) {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => backend.fetch(input, init));
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn()
    }))
  );
  window.matchMedia = globalThis.matchMedia;
  return fetchMock;
}

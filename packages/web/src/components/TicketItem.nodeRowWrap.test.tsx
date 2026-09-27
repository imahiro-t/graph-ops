// DFLT-00242: a node row in the "Nodes & Artifacts" list wraps below lg
// instead of piling its parts on top of each other. At 320-480px the left
// group (chevron, sequence number, id, type badge, name, retry/manual/
// artifact badges) was flex-1 -- a 0% basis -- with min-w-0, while its
// children were all shrink-0/nowrap, so it spilled under the shrink-0 right
// group (approve/reject, status badge, update time) and the panel clipped
// the rest. flex-wrap on the row alone does not fix it: with a 0% basis the
// left group counts as 0px when the lines are formed, so the right group
// never moves to the next line (measured: the overlaps remain). So below lg
// the row and both groups wrap, the left group is sized by its content
// (max-lg:basis-auto), the right group may shrink and wrap and stays
// right-aligned on its own line, and the name (and an id wider than the
// whole row) wraps rather than truncating. From lg up every class the row
// had before is still there and nothing without the max-lg: prefix was added
// except whitespace-nowrap on the status badge and the time, which are
// already one line there. jsdom does no layout (nor media queries), so this
// checks the classes and structure; the geometry at 320/375/480px (ja/en)
// and at 1024-1440px was measured in a real browser (see the ticket's
// implementation notes).
//
// DFLT-00253 moved that boundary from lg (1024px) to 80rem, a rem media
// query (`[@media_not_all_and_(min-width:80rem)]:`, the rem form of max-xl:).
// In English at 1024-1279px the one-line layout let the artifact badge run
// over the status badge and the type badge over the approve button, and a
// px boundary does not follow the browser's default font size, so a 20-32px
// default font brought the same overlaps back at 1280px and wider. At the
// default 16px, 80rem is 1280px, so the row from 1280px up is unchanged.
// The right group now aligns right with justify-content: safe flex-end
// instead of justify-end, so with a 200% default font on a 320-375px screen
// the approve/reject buttons no longer run out past the row's left edge;
// the buttons, their wrapper and the type badge may shrink there, and under
// 15rem the row and the buttons pad less. The geometry (no two parts of a
// row intersecting at 16px x 1024-1440px and 20/24/32px x 1024/1280px, the
// buttons inside the card and hit by elementFromPoint at 200% x 320/375px)
// was measured in a real browser (see the ticket's implementation notes).
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, GraphEdge, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const T = 'TEST-00242';
const LONG_NAME = '実装（ノード一覧行の狭幅折り返し・テスト・ブラウザ実測・リリースノート）';

const node = (id: string, type: GraphNode['type'], status: GraphNode['status'], extra: Partial<GraphNode> = {}): GraphNode => ({
  id,
  ticket_id: T,
  name: `${type} ${id}`,
  type,
  status,
  iteration_count: 0,
  max_iterations: 3,
  is_manual: type === 'approval_gate',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...extra
});

const edge = (from: string, to: string): GraphEdge => ({
  id: `${from}-${to}`,
  ticket_id: T,
  from_node_id: from,
  to_node_id: to,
  condition: 'success',
  created_at: '2026-01-01T00:00:00Z'
});

const artifact = (id: string, nodeId: string): Artifact => ({
  id,
  ticket_id: T,
  node_id: nodeId,
  name: `artifact ${id}`,
  type: 'text',
  content: '',
  created_at: '2026-01-01T00:00:00Z'
});

// A node with every optional badge (retry, manual, artifacts), a node with a
// long name, and an approval gate whose only predecessor is DONE, so its
// approve/reject buttons show in the row.
const NODES = [
  node(`${T}-01`, 'release', 'DONE', { iteration_count: 2, is_manual: true }),
  node(`${T}-02`, 'implementation', 'IN PROGRESS', { name: LONG_NAME }),
  node(`${T}-03`, 'approval_gate', 'TODO')
];
const EDGES = [edge(`${T}-01`, `${T}-03`)];
const ARTIFACTS = [artifact('a1', `${T}-01`), artifact('a2', `${T}-01`)];

const makeTicket = (): TicketDetail => ({
  id: T,
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: NODES,
  edges: EDGES,
  artifacts: ARTIFACTS
});

const renderTicket = () =>
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );

// The row's parts, found from the node's named toggle button.
const rowParts = (n: GraphNode) => {
  const toggle = screen.getByTestId(`node-toggle-expand-${n.id}`);
  const left = toggle.parentElement!;
  const row = left.parentElement!;
  const right = row.children[1] as HTMLElement;
  const id = within(left).getByText(n.id);
  const name = within(left).getByText(n.name);
  const time = right.lastElementChild as HTMLElement;
  // The status badge is the right group's rounded-full span (see getNodeBadge).
  const status = Array.from(right.children).find(el => el.tagName === 'SPAN' && el.classList.contains('rounded-full')) as HTMLElement;
  return { toggle, left, row, right, id, name, time, status };
};

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
// DFLT-00253: the wrapping layout's boundary, and the query for a large
// default font on a narrow screen (DFLT-00227).
const UNDER_80REM = '[@media_not_all_and_(min-width:80rem)]:';
const NARROW_LARGE_TEXT = '[@media(max-width:15rem)]:';

const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

const classList = (el: Element) => (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean);

// The classes each part had before DFLT-00242 (what lays the row out from
// 80rem up), and the classes added under 80rem (DFLT-00242, moved from
// max-lg: by DFLT-00253) or under 15rem.
const BEFORE = {
  row: 'flex items-center justify-between p-3 cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-700 select-none',
  left: 'flex items-center gap-2.5 flex-1 min-w-0',
  right: 'flex items-center gap-3 shrink-0',
  id: 'font-mono font-bold text-slate-600 dark:text-slate-300 shrink-0 whitespace-nowrap',
  name: 'font-semibold text-slate-800 dark:text-slate-200 truncate min-w-0',
  time: 'text-[11px] text-slate-600 dark:text-slate-300 font-mono',
  approvalButtons: 'flex items-center gap-1.5'
};
const ADDED = {
  row: [`${UNDER_80REM}flex-wrap`, `${UNDER_80REM}gap-x-3`, `${UNDER_80REM}gap-y-2`, `${NARROW_LARGE_TEXT}p-2`],
  // Without basis-auto the left group keeps flex-1's 0% basis and the right
  // group never wraps to the next line, so this one is essential.
  left: [`${UNDER_80REM}basis-auto`, `${UNDER_80REM}flex-wrap`, `${UNDER_80REM}gap-y-1.5`],
  right: [
    `${UNDER_80REM}shrink`,
    `${UNDER_80REM}min-w-0`,
    `${UNDER_80REM}flex-wrap`,
    `${UNDER_80REM}gap-y-1.5`,
    `${UNDER_80REM}ml-auto`,
    `${UNDER_80REM}[justify-content:safe_flex-end]`
  ],
  id: [`${UNDER_80REM}shrink`, `${UNDER_80REM}min-w-0`, `${UNDER_80REM}whitespace-normal`, `${UNDER_80REM}[overflow-wrap:anywhere]`],
  name: [`${UNDER_80REM}whitespace-normal`, `${UNDER_80REM}[overflow-wrap:anywhere]`],
  time: ['whitespace-nowrap'],
  approvalButtons: [`${UNDER_80REM}flex-wrap`, `${UNDER_80REM}min-w-0`, `${UNDER_80REM}max-w-full`],
  // DFLT-00253: each approve/reject button and the type badge may shrink
  // under 80rem, and the buttons pad less under 15rem.
  approvalButton: [
    `${UNDER_80REM}min-w-0`,
    `${UNDER_80REM}max-w-full`,
    `${UNDER_80REM}flex-wrap`,
    `${UNDER_80REM}[overflow-wrap:anywhere]`,
    `${NARROW_LARGE_TEXT}px-1`
  ],
  typeBadge: [`${UNDER_80REM}shrink`, `${UNDER_80REM}min-w-0`, `${UNDER_80REM}max-w-full`]
};
// A prefix is allowed for an added class only if it is one of these.
const ALLOWED_PREFIXES = [UNDER_80REM, NARROW_LARGE_TEXT];
// px breakpoints, which do not follow the browser's default font size; none
// of them may come back on a node row (DFLT-00253).
const PX_PREFIXES = ['max-lg:', 'max-xl:', 'lg:', 'xl:', 'max-2xl:', '2xl:'];
const hasPxPrefix = (cls: string) => PX_PREFIXES.some(p => cls.startsWith(p));
// The only unprefixed classes DFLT-00242 added: the status badge and the time
// are one line from lg up already, so nowrap changes nothing there.
const UNPREFIXED_ALLOWED = new Set(['whitespace-nowrap']);
// Unprefixed wrap/size classes that would change the row from 80rem up.
const FORBIDDEN_UNPREFIXED = [
  'flex-wrap',
  'basis-auto',
  'basis-full',
  'whitespace-normal',
  'shrink',
  'ml-auto',
  'justify-end',
  'gap-y-2',
  'gap-y-1.5',
  'max-w-full',
  '[justify-content:safe_flex-end]'
];

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('TicketItem node rows on a narrow screen (%s)', lang => {
  beforeEach(async () => {
    await i18n.changeLanguage(lang);
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: the row and both groups wrap under 80rem, the left group sized by its content', (_id, n) => {
    renderTicket();
    const { row, left, right } = rowParts(n);
    expect(row).toHaveClass(...ADDED.row);
    expect(left).toHaveClass(...ADDED.left);
    expect(right).toHaveClass(...ADDED.right);
    // The plain justify-end let an over-wide right group run out past the
    // row's left edge (DFLT-00253); safe flex-end replaced it.
    expectNoneOf(right, ['justify-end', 'max-lg:justify-end', `${UNDER_80REM}justify-end`]);
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: the id and name may wrap under 80rem; the status badge and time never break', (_id, n) => {
    renderTicket();
    const { id, name, status, time } = rowParts(n);
    expect(id).toHaveClass(...ADDED.id);
    expect(name).toHaveClass(...ADDED.name);
    expect(status).toHaveClass('whitespace-nowrap');
    expect(time).toHaveClass('whitespace-nowrap');
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: keeps every class it had before, so the row from 80rem up is unchanged', (_id, n) => {
    renderTicket();
    const parts = rowParts(n);
    for (const key of ['row', 'left', 'right', 'id', 'name', 'time'] as const) {
      expect(parts[key]).toHaveClass(...BEFORE[key].split(' '));
    }
    // The badges that must not wrap from lg up are still shrink-0 whitespace-nowrap.
    for (const badge of parts.left.querySelectorAll('span')) {
      if (badge === parts.id || badge === parts.name || badge.textContent === String(NODES.indexOf(n) + 1)) continue;
      if (!badge.classList.contains('whitespace-nowrap')) continue;
      expect(badge).toHaveClass('shrink-0');
    }
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: everything added is 80rem/15rem prefixed, apart from the allowed whitespace-nowrap', (_id, n) => {
    renderTicket();
    const parts = rowParts(n);
    for (const key of ['row', 'left', 'right', 'id', 'name', 'time'] as const) {
      const before = new Set(BEFORE[key].split(' '));
      const added = classList(parts[key]).filter(c => !before.has(c));
      expect(added.sort()).toEqual([...ADDED[key]].sort());
      for (const cls of added) {
        if (!UNPREFIXED_ALLOWED.has(cls)) expect(ALLOWED_PREFIXES.some(p => cls.startsWith(p))).toBe(true);
      }
      expectNoneOf(parts[key], FORBIDDEN_UNPREFIXED);
    }
    expectNoneOf(parts.status, FORBIDDEN_UNPREFIXED);
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: no part of the row keeps a px breakpoint (they ignore the default font size)', (_id, n) => {
    renderTicket();
    const { row } = rowParts(n);
    for (const el of [row, ...row.querySelectorAll('*')]) {
      const px = classList(el).filter(hasPxPrefix);
      expect(px).toEqual([]);
    }
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: the type badge may shrink under 80rem and keeps its full label in its title', (_id, n) => {
    renderTicket();
    const { left } = rowParts(n);
    const badge = left.querySelector('span[title]') as HTMLElement;
    expect(badge).toHaveClass('shrink-0', ...ADDED.typeBadge);
    expectNoneOf(badge, ['shrink', 'min-w-0', 'max-w-full']);
    expect(badge.getAttribute('title')).not.toBe('');
    // Its label truncates inside the badge (NodeTypeBadge), so a shrunk badge
    // shows an ellipsis rather than running past the row.
    const label = badge.querySelector('span') as HTMLElement;
    expect(label).toHaveClass('truncate');
    expect(label).toHaveTextContent(badge.getAttribute('title')!);
  });

  it('the approve/reject buttons wrap and may shrink under 80rem, and still do not toggle the row', () => {
    renderTicket();
    const gate = NODES[2];
    const approve = screen.getByTestId(`node-approve-${gate.id}`);
    const buttons = approve.parentElement!;
    expect(buttons).toHaveClass(...BEFORE.approvalButtons.split(' '), ...ADDED.approvalButtons);
    expectNoneOf(buttons, ['flex-wrap', 'min-w-0', 'max-w-full']);
    for (const button of [approve, screen.getByTestId(`node-reject-${gate.id}`)]) {
      // px-2 stays the padding from 15rem up; under 15rem it is px-1.
      expect(button).toHaveClass('px-2', 'flex', ...ADDED.approvalButton);
      expectNoneOf(button, ['min-w-0', 'max-w-full', 'flex-wrap', 'px-1', '[overflow-wrap:anywhere]']);
      expect(classList(button).filter(hasPxPrefix)).toEqual([]);
    }
    // They stay inside the right group, which moves to its own line as a whole.
    const { right, toggle, row } = rowParts(gate);
    expect(right).toContainElement(buttons);

    // Clicking the reject button opens the reason prompt (no window.confirm)
    // but, like approve, stops at the buttons' wrapper: the row stays closed.
    fireEvent.click(buttons);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(screen.getByTestId(`node-reject-${gate.id}`));
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    // The row itself still toggles.
    fireEvent.click(row);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });
});

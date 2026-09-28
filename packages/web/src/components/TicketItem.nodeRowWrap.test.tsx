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
// query (the rem form of max-xl:). DFLT-00260 named it `below-80rem:`
// (`@media not all and (min-width: 80rem)`, tailwind.config.js); `upto-15rem:`
// is `@media (max-width: 15rem)`.
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
//
// DFLT-00253 round 2 (review findings):
// - From 80rem up (the one-line layout), in English a row carrying the
//   retry, manual and artifact badges still ran its artifact badge under the
//   approve button or the status badge at 1280px+ (the panel's width is
//   capped, so a wider window does not help). The row now wraps there too,
//   but only when it has to: the left group keeps flex-1 min-w-0 and adds
//   min-w-min from 80rem, and the name (from 80rem) is width 0 with a content
//   flex-basis, so it is laid out and truncated as before but adds nothing to
//   that minimum. When the id and badges plus the right group do not fit on
//   one line, the right group moves to the next line, right-aligned by its
//   (now unprefixed) ml-auto. A row that fits is unchanged: its left group
//   takes the free space, so ml-auto and the (now unprefixed) row-gap do
//   nothing. Measured: row heights and tab positions at 16px x 1280/1440px
//   (ja/en) match the pre-DFLT-00253 values, and no two parts of any row
//   intersect with the reviewers' data.
// - Under 80rem the type badge's label wraps instead of truncating, so a
//   200% default font on a 320px screen shows the whole label (WCAG 1.4.4;
//   the title tooltip reaches neither keyboard nor touch users).
//
// DFLT-00260:
// - From 80rem up the row keeps a 0.25rem column gap (gap-x-1), so a row
//   whose name is truncated no longer runs its last badge right up to the
//   status badge (they touched with 0px between them). This is the one
//   intended change to the row from 80rem up: a row with 0.25rem or more of
//   free space looks the same (the left group gives up space it did not
//   need), one with less truncates its name 0.25rem earlier. Under 80rem
//   below-80rem:gap-x-3 still overrides it.
// - The retry, manual and artifact badges shrink and wrap under 80rem like
//   the type badge, so in a 160px window they no longer run 3-26px past the
//   card. From 80rem up they stay shrink-0 whitespace-nowrap.
// - The badges' font sizes are rem (0.625rem / 0.6875rem, the same 10px /
//   11px at the default 16px), so they follow the browser's default font
//   size (WCAG 1.4.4). The status badge grows with it, and its one-line
//   label ran up to 41px past the card with a 200% default font on a
//   320-336px screen, so under 80rem it may shrink and wrap too; from 80rem
//   up it stays one line.
//
// DFLT-00280: in English with a 200% default font at 320-375px (and at 16px
// in a 160px window) the status badge wrapped inside its words ("IN / PROGR
// / ESS", "AWAITIN / G FIX") and, still rounded-full, looked like a tall
// oval. `anywhere` makes a single character the badge's min-content width,
// so the shrinking right group squeezed it that narrow. Under 80rem it is
// now break-words (overflow-wrap: break-word), which keeps the longest word
// as the min-content width, so it wraps at the spaces and breaks inside a
// word only when that word cannot fit on a line of its own; and it is
// rounded-xl there, a rounded rectangle when wrapped and still a pill on one
// line (0.75rem is at least half the one-line height).
// That alone did not fit: at 200% x 320-336px and 16px x 160px the badge
// already took the row's whole width and still had no room for "PROGRESS"
// (next to the dot) or "AWAITING" on a line of their own (measured: 138px
// available for 116px + 32px padding + the dot; 66px for 62px + 16px). So
// under 80rem the IN PROGRESS badge is a block with the dot inline in front
// of the label ("• IN" / "PROGRESS"), and under 15rem the badge and the
// row pad 0.25rem at the sides instead of 0.5rem. The label stays in a span
// of its own, with min-w-0; that has no effect today (under 80rem the span
// is inline in a block, and from 80rem up the badge is nowrap and does not
// shrink) and only guards the label should the badge become a shrinking
// flex box again. Measured in a real browser
// (en, Chrome's default font size set to 32px / 16px): no word is broken at
// 32px x 320/336/375px or 16px x 160px, the badges stay inside the row with
// no horizontal scroll, one-line badges are still pills, 80rem up is
// unchanged (rounded-full), and in Japanese every badge is one line.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { getStatusMeta } from '../statusMeta';
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
// default font on a narrow screen (DFLT-00227). DFLT-00260 turned both into
// named variants (tailwind.config.js) with the same media conditions.
const UNDER_80REM = 'below-80rem:';
const FROM_80REM = '[@media(min-width:80rem)]:';
const NARROW_LARGE_TEXT = 'upto-15rem:';

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
  // flex-wrap and gap-y-2 hold at every width (DFLT-00253 round 2): a row
  // that fits never wraps, so they only act when it does not. gap-x-1 keeps
  // the left and right groups 0.25rem apart from 80rem up (DFLT-00260);
  // under 80rem gap-x-3 overrides it.
  // Under 15rem the row pads 0.5rem above and below and 0.25rem at the
  // sides (DFLT-00280; 0.5rem all round before), so the status badge has
  // room for one word per line at 200% x 320px and 16px x 160px.
  row: ['flex-wrap', 'gap-x-1', 'gap-y-2', `${UNDER_80REM}gap-x-3`, `${NARROW_LARGE_TEXT}px-1`, `${NARROW_LARGE_TEXT}py-2`],
  // Without basis-auto the left group keeps flex-1's 0% basis and the right
  // group never wraps to the next line, so this one is essential.
  // From 80rem the left group is at least as wide as its parts other than
  // the name, so a row they do not fit in wraps (DFLT-00253 round 2).
  left: [`${FROM_80REM}min-w-min`, `${UNDER_80REM}basis-auto`, `${UNDER_80REM}flex-wrap`, `${UNDER_80REM}gap-y-1.5`],
  right: [
    'ml-auto',
    `${UNDER_80REM}shrink`,
    `${UNDER_80REM}min-w-0`,
    `${UNDER_80REM}flex-wrap`,
    `${UNDER_80REM}gap-y-1.5`,
    `${UNDER_80REM}[justify-content:safe_flex-end]`
  ],
  id: [`${UNDER_80REM}shrink`, `${UNDER_80REM}min-w-0`, `${UNDER_80REM}whitespace-normal`, `${UNDER_80REM}[overflow-wrap:anywhere]`],
  // From 80rem the name adds nothing to the left group's min-content width
  // (width 0) but is still laid out at its content width (content basis).
  name: [`${FROM_80REM}w-0`, `${FROM_80REM}[flex-basis:content]`, `${UNDER_80REM}whitespace-normal`, `${UNDER_80REM}[overflow-wrap:anywhere]`],
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
  typeBadge: [`${UNDER_80REM}shrink`, `${UNDER_80REM}min-w-0`, `${UNDER_80REM}max-w-full`],
  // DFLT-00253 round 2: the badge's label wraps under 80rem.
  typeBadgeLabel: [`${UNDER_80REM}whitespace-normal`, `${UNDER_80REM}[overflow-wrap:anywhere]`],
  // DFLT-00260: the retry, manual and artifact badges shrink and wrap under
  // 80rem too, so a 160px window no longer pushes them past the card.
  sideBadge: [
    `${UNDER_80REM}shrink`,
    `${UNDER_80REM}min-w-0`,
    `${UNDER_80REM}max-w-full`,
    `${UNDER_80REM}whitespace-normal`,
    `${UNDER_80REM}[overflow-wrap:anywhere]`
  ],
  // DFLT-00260: the status badge may wrap under 80rem (it sits in the right
  // group, whose items shrink by default). DFLT-00280: at the spaces
  // (break-words, not anywhere), and as a rounded rectangle (rounded-xl);
  // under 15rem it pads 0.25rem at the sides.
  statusBadge: [
    `${UNDER_80REM}min-w-0`,
    `${UNDER_80REM}max-w-full`,
    `${UNDER_80REM}whitespace-normal`,
    `${UNDER_80REM}break-words`,
    `${UNDER_80REM}rounded-xl`,
    `${NARROW_LARGE_TEXT}px-1`
  ]
};
// A prefix is allowed for an added class only if it is one of these.
const ALLOWED_PREFIXES = [UNDER_80REM, FROM_80REM, NARROW_LARGE_TEXT];
// px breakpoints, which do not follow the browser's default font size; none
// of them may come back on a node row (DFLT-00253).
const PX_PREFIXES = ['max-lg:', 'max-xl:', 'lg:', 'xl:', 'max-2xl:', '2xl:'];
const hasPxPrefix = (cls: string) => PX_PREFIXES.some(p => cls.startsWith(p));
// The only unprefixed classes added: whitespace-nowrap (DFLT-00242; the
// status badge and the time are one line from lg up already, so nowrap
// changes nothing there), the row's flex-wrap / gap-y-2 and the right
// group's ml-auto (DFLT-00253 round 2), which act only on a row that does not
// fit on one line, and the row's gap-x-1 (DFLT-00260), the intended 0.25rem
// between the two groups from 80rem up.
const UNPREFIXED_ALLOWED: Record<string, Set<string>> = {
  row: new Set(['flex-wrap', 'gap-x-1', 'gap-y-2']),
  right: new Set(['ml-auto']),
  time: new Set(['whitespace-nowrap'])
};
const unprefixedAllowed = (key: string, cls: string) => UNPREFIXED_ALLOWED[key]?.has(cls) ?? false;
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

  it.each(NODES.map(n => [n.id, n] as const))('%s: the id, name and status badge may wrap under 80rem; the time never breaks', (_id, n) => {
    renderTicket();
    const { id, name, status, time } = rowParts(n);
    expect(id).toHaveClass(...ADDED.id);
    expect(name).toHaveClass(...ADDED.name);
    // One line from 80rem up; under 80rem it may wrap (DFLT-00260).
    expect(status).toHaveClass('rounded-full', 'whitespace-nowrap', 'px-2', ...ADDED.statusBadge);
    expectNoneOf(status, ['min-w-0', 'max-w-full', 'whitespace-normal', '[overflow-wrap:anywhere]', 'break-words', 'rounded-xl', 'px-1']);
    // DFLT-00280: `anywhere` broke "IN PROGRESS" inside its words.
    expectNoneOf(status, [`${UNDER_80REM}[overflow-wrap:anywhere]`]);
    expect(time).toHaveClass('whitespace-nowrap');
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: keeps every class it had before, so from 80rem up only the 0.25rem gap is new', (_id, n) => {
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

  it.each(NODES.map(n => [n.id, n] as const))('%s: everything added is 80rem/15rem prefixed, apart from the allowed unprefixed classes (gap-x-1 among them)', (_id, n) => {
    renderTicket();
    const parts = rowParts(n);
    for (const key of ['row', 'left', 'right', 'id', 'name', 'time'] as const) {
      const before = new Set(BEFORE[key].split(' '));
      const added = classList(parts[key]).filter(c => !before.has(c));
      expect(added.sort()).toEqual([...ADDED[key]].sort());
      for (const cls of added) {
        if (!unprefixedAllowed(key, cls)) expect(ALLOWED_PREFIXES.some(p => cls.startsWith(p))).toBe(true);
      }
      expectNoneOf(parts[key], FORBIDDEN_UNPREFIXED.filter(cls => !unprefixedAllowed(key, cls)));
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

  it.each(NODES.map(n => [n.id, n] as const))('%s: the type badge may shrink under 80rem and its label then wraps, showing the whole label', (_id, n) => {
    renderTicket();
    const { left } = rowParts(n);
    const badge = left.querySelector('span[title]') as HTMLElement;
    expect(badge).toHaveClass('shrink-0', ...ADDED.typeBadge);
    expectNoneOf(badge, ['shrink', 'min-w-0', 'max-w-full']);
    expect(badge.getAttribute('title')).not.toBe('');
    // DFLT-00253 round 2: under 80rem the label wraps (anywhere, so even one
    // long word fits a shrunk badge) rather than truncating -- the ellipsis
    // hid part of the label at 200% x 320px, and the title tooltip reaches
    // neither keyboard nor touch users. From 80rem up it keeps truncate, one
    // line as before.
    const label = badge.querySelector('span') as HTMLElement;
    expect(label).toHaveClass('truncate', ...ADDED.typeBadgeLabel);
    expectNoneOf(label, ['whitespace-normal', '[overflow-wrap:anywhere]', 'break-words']);
    expect(classList(label).filter(c => c !== 'truncate' && !c.startsWith(UNDER_80REM))).toEqual([]);
    expect(label).toHaveTextContent(badge.getAttribute('title')!);
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: from 80rem a row whose parts do not fit on one line moves its right group to the next line', (_id, n) => {
    renderTicket();
    const { row, left, name, right } = rowParts(n);
    // The row may wrap at any width; the right group is pushed right on its own line.
    expect(row).toHaveClass('flex-wrap', 'gap-y-2', 'justify-between');
    expect(right).toHaveClass('ml-auto', 'shrink-0');
    // The left group still takes the free space and may shrink to its
    // min-content width, which (the name adding nothing) is the id and badges.
    expect(left).toHaveClass('flex-1', 'min-w-0', `${FROM_80REM}min-w-min`);
    expect(left).not.toHaveClass('min-w-min');
    expect(name).toHaveClass('truncate', 'min-w-0', `${FROM_80REM}w-0`, `${FROM_80REM}[flex-basis:content]`);
    expectNoneOf(name, ['w-0', '[flex-basis:content]', 'basis-0', 'grow', 'flex-1']);
    // The one-line layout's badges still never wrap or shrink.
    for (const badge of left.querySelectorAll('span.whitespace-nowrap')) expect(badge).toHaveClass('shrink-0');
  });

  // DFLT-00260: the retry, manual and artifact badges of the node that has all three.
  const sideBadges = () => {
    const n = NODES[0];
    const { left } = rowParts(n);
    const byText = (text: string) => within(left).getByText(text, { exact: false }).closest('span') as HTMLElement;
    const retry = byText(i18n.t('ticketItem.retryCount', { count: n.iteration_count }));
    const manual = byText(i18n.t('ticketItem.manualApproval'));
    const artifacts = within(left).getByText(i18n.t('ticketItem.artifactsCount', { count: 2 }), { exact: false }).closest('span.rounded-full') as HTMLElement;
    return { retry, manual, artifacts };
  };

  it.each(NODES.map(n => [n.id, n] as const))('%s: the row keeps 0.25rem between its groups from 80rem up, and gap-x-3 under 80rem', (_id, n) => {
    renderTicket();
    const { row } = rowParts(n);
    expect(row).toHaveClass('gap-x-1', `${UNDER_80REM}gap-x-3`);
    expectNoneOf(row, ['gap-x-3', 'gap-2', 'gap-3']);
  });

  // DFLT-00280: from 80rem up the IN PROGRESS badge is the flex box it was
  // (dot, then label). Under 80rem it is a block and the dot flows inline in
  // front of the label, so the badge wraps as "• IN" / "PROGRESS" instead
  // of giving the dot a column of its own beside a squeezed label.
  it('the IN PROGRESS badge keeps its label in a min-w-0 span next to the pulsing dot', () => {
    renderTicket();
    const n = NODES.find(x => x.status === 'IN PROGRESS')!;
    const { status } = rowParts(n);
    expect(status).toHaveClass('flex', 'items-center', 'gap-1', `${UNDER_80REM}block`);
    expectNoneOf(status, ['block', 'inline', 'inline-block']);
    const [dot, label] = Array.from(status.children) as HTMLElement[];
    expect(dot).toHaveClass(
      'w-1.5',
      'h-1.5',
      'shrink-0',
      'rounded-full',
      'animate-pulse',
      `${UNDER_80REM}inline-block`,
      `${UNDER_80REM}mr-1`,
      `${UNDER_80REM}align-middle`
    );
    expectNoneOf(dot, ['inline-block', 'mr-1', 'align-middle']);
    expect(label.tagName).toBe('SPAN');
    expect(label).toHaveClass('min-w-0');
    expect(label).toHaveTextContent(i18n.t(getStatusMeta('IN PROGRESS').labelKey));
    expect(status.children).toHaveLength(2);
  });

  it('the other status badges keep their label as plain text', () => {
    renderTicket();
    for (const n of NODES.filter(x => x.status !== 'IN PROGRESS')) {
      const { status } = rowParts(n);
      expect(status.children).toHaveLength(0);
      expectNoneOf(status, ['flex', `${UNDER_80REM}block`]);
    }
  });

  it('the retry, manual and artifact badges may shrink and wrap under 80rem and stay one line from 80rem up', () => {
    renderTicket();
    const { retry, manual, artifacts } = sideBadges();
    for (const badge of [retry, manual, artifacts]) {
      expect(badge).toHaveClass('shrink-0', 'whitespace-nowrap', ...ADDED.sideBadge);
      expectNoneOf(badge, ['shrink', 'min-w-0', 'max-w-full', 'whitespace-normal', '[overflow-wrap:anywhere]']);
      expect(classList(badge).filter(hasPxPrefix)).toEqual([]);
    }
    // The artifact badge's icon keeps its size when the badge shrinks.
    expect(artifacts.querySelector('svg')).toHaveClass('w-3', 'h-3', 'shrink-0');
  });

  it.each(NODES.map(n => [n.id, n] as const))('%s: the badges size their text in rem, following the default font size', (_id, n) => {
    renderTicket();
    const { left, status } = rowParts(n);
    const typeBadge = left.querySelector('span[title]') as HTMLElement;
    expect(typeBadge).toHaveClass('text-[0.625rem]');
    expect(status).toHaveClass('text-[0.6875rem]');
    for (const el of [typeBadge, status]) expectNoneOf(el, ['text-[10px]', 'text-[11px]']);
  });

  it('the retry, manual and artifact badges size their text in rem', () => {
    renderTicket();
    const { retry, manual, artifacts } = sideBadges();
    expect(retry).toHaveClass('text-[0.6875rem]');
    expect(manual).toHaveClass('text-[0.625rem]');
    expect(artifacts).toHaveClass('text-[0.625rem]');
    for (const el of [retry, manual, artifacts]) expectNoneOf(el, ['text-[10px]', 'text-[11px]']);
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

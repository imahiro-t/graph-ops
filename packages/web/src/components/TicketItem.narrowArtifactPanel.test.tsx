// DFLT-00368: two leftovers of the artifact panel at 320px with a 200%
// default font (the Web UI's support floor).
//
// 1. In the node tab, an inline artifact's "Open in new tab" link sits in a
//    flex justify-end row only ~64px wide, while the link (icon + gap +
//    "Open", never wrapping) needed ~80px, so it ran 20.3px past the row's
//    left edge in English. The node tab's three call sites (gherkin / html /
//    text) now add upto-15rem:flex-wrap upto-15rem:justify-end, so under
//    15rem the icon may take a line of its own. min-w-0 on the link and
//    shrink-0 on the icon stay out (they left the Japanese label one
//    character a line, DFLT-00366), and the other tabs' links keep exactly
//    the classes they had.
// 2. The panel's card was pinned to the graph panel's height at every width,
//    so in one column the stacked tab row took most of it and the scroll
//    area (role="tabpanel") was ~64px tall. The measured height is now a CSS
//    variable used by lg:h-(--node-list-card-height) only; below lg the
//    card's height is auto above the same min-h-128 floor.
//
// jsdom does no layout and ignores media queries, so these tests pin the
// classes and the inline style; the geometry was measured in a real browser
// and is recorded in the implementation notes:
//   - Node tab link, Chrome (Playwright), 200% default font: at 304px (320px
//     less a 16px classic scrollbar; the row is 64px, as in the ticket) the
//     English link ran 20.3px past the row's left edge before and 0px
//     after (icon on its own line, then "Open / in new / tab"); at 320px
//     (row 80px) 4.3px before, 0px after. Japanese: 0px before and after,
//     「別タ / ブで / 開く」 at 64px and 「別タブ / で開く」 at 80px -- never
//     one character a line. Light and dark, gherkin / html / text alike.
//     At 375px and 1280px (100% font) the link stays one line, unchanged.
//   - Panel height at 320px / 200%: the card was pinned to the graph's
//     1024-1176px, the stacked tab row took 707-739px of it, and the scroll
//     area was 315-435px, too short for a whole artifact header row in the
//     Gherkin / HTML tabs (338-792px tall at that size). After, the card is
//     as tall as its content (never under min-h-128 = 1024px there) and the
//     header rows are fully inside the scroll area; no horizontal scroll.
//   - From lg up (1024px, 1280px, 1457px at 100%) the card's height still
//     equals the graph panel's (689.5px / 653px / 653px, as before), and the
//     HTML / Artifacts tabs' links are where they were (the Gherkin tab's
//     link keeps its classes, pinned below). At 1023px the card's height is
//     auto.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, ArtifactType, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const NODE_ID = 'TEST-00368-01';

const art = (id: string, name: string, type: ArtifactType, content: string | null): Artifact => ({
  id,
  ticket_id: 'TEST-00368',
  node_id: NODE_ID,
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const ARTIFACTS: Artifact[] = [
  art('a-gherkin', 'Gherkin 仕様', 'gherkin', 'Feature: 仕様\n'),
  art('a-html', 'テストレポート', 'html', '<p>report</p>'),
  art('a-text', '実装メモ', 'text', '# メモ\n本文\n')
];

const NODE: GraphNode = {
  id: NODE_ID,
  ticket_id: 'TEST-00368',
  name: '実装',
  type: 'implementation',
  status: 'DONE',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
};

const ticket: TicketDetail = {
  id: 'TEST-00368',
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
  nodes: [NODE],
  edges: [],
  artifacts: ARTIFACTS
};

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

function renderTicket() {
  return render(
    <TicketItem
      ticket={ticket}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );
}

// The node's name appears in the graph panel and in the node list; the list
// row (the last one) is the accordion header that expands the artifacts.
const openNodeDetail = () => {
  const occurrences = screen.getAllByText(NODE.name);
  fireEvent.click(occurrences[occurrences.length - 1]);
};
const openTab = (tab: 'gherkin' | 'html' | 'artifacts', count: number) =>
  fireEvent.click(screen.getByRole('tab', { name: i18n.t(`ticketItem.tabs.${tab}`, { count }) }));

const classes = (el: Element) => el.getAttribute('class')?.split(/\s+/).filter(Boolean) ?? [];
const expectClasses = (el: Element, expected: string[]) => {
  const has = classes(el);
  for (const c of expected) expect(has, `expected "${c}" on <${el.tagName.toLowerCase()} class="${has.join(' ')}">`).toContain(c);
};
const expectNoClasses = (el: Element, forbidden: string[]) => {
  const has = classes(el);
  for (const c of forbidden) expect(has, `unexpected "${c}" on <${el.tagName.toLowerCase()} class="${has.join(' ')}">`).not.toContain(c);
};

const newTabLinkFor = (id: string) =>
  within(screen.getByRole('tabpanel'))
    .getAllByRole('link', { name: i18n.t('ticketItem.openInNewTab') })
    .find(l => l.getAttribute('href')?.startsWith(`/artifacts/${id}/`))!;

const NARROW_WRAP = ['upto-15rem:flex-wrap', 'upto-15rem:justify-end'];
const BASE_LINK = [
  'text-indigo-600',
  'dark:text-indigo-400',
  'hover:underline',
  'flex',
  'items-center',
  'gap-1',
  'text-[0.6875rem]',
  'rounded-sm',
  'focus:outline-hidden',
  'focus-visible:ring-2',
  'focus-visible:ring-blue-500',
  'dark:focus-visible:ring-blue-400'
];
// The default class string every tab used before DFLT-00368.
const BASE_LINK_CLASS = BASE_LINK.join(' ');
// These left the Japanese label one character a line in the ~64px row.
const LINK_FORBIDDEN = ['min-w-0', 'below-80rem:min-w-0', 'shrink-0', 'whitespace-nowrap'];

describe('DFLT-00368 node tab: "Open in new tab" may put its icon on its own line under 15rem', () => {
  it.each(ARTIFACTS.map(a => [a.type, a.id] as const))('the %s inline artifact link carries the narrow wrap classes', (_type, id) => {
    renderTicket();
    openNodeDetail();
    const link = newTabLinkFor(id);
    expectClasses(link, [...BASE_LINK, ...NARROW_WRAP]);
    expectNoClasses(link, LINK_FORBIDDEN);
    // Still the icon followed directly by the label, the icon not shrink-0.
    expect(link.children).toHaveLength(1);
    const icon = link.children[0];
    expect(icon.tagName.toLowerCase()).toBe('svg');
    expectClasses(icon, ['w-3', 'h-3']);
    expectNoClasses(icon, ['shrink-0']);
    expect(link.textContent).toBe(i18n.t('ticketItem.openInNewTab'));
    // The row the link sits in is unchanged.
    expect(classes(link.parentElement!)).toEqual(['flex', 'justify-end']);
  });

  it('keeps the accessible name in Japanese and English', async () => {
    renderTicket();
    openNodeDetail();
    expect(within(screen.getByRole('tabpanel')).getAllByRole('link', { name: '別タブで開く' })).toHaveLength(3);
    await i18n.changeLanguage('en');
    expect(await within(screen.getByRole('tabpanel')).findAllByRole('link', { name: 'Open in new tab' })).toHaveLength(3);
  });
});

describe('DFLT-00368 the other tabs keep the default link classes', () => {
  it.each([
    ['gherkin', 1, 'a-gherkin'],
    ['html', 1, 'a-html']
  ] as const)('%s tab', (tab, count, id) => {
    renderTicket();
    openTab(tab, count);
    const link = newTabLinkFor(id);
    expect(link.getAttribute('class')).toBe(BASE_LINK_CLASS);
  });

  it('Artifacts tab', () => {
    renderTicket();
    openTab('artifacts', ARTIFACTS.length);
    const links = within(screen.getByRole('tabpanel')).getAllByRole('link', { name: i18n.t('ticketItem.openInNewTab') });
    expect(links.length).toBeGreaterThan(0);
    for (const l of links) expect(l.getAttribute('class')).toBe(BASE_LINK_CLASS);
  });
});

// The node/artifact panel's card: the element holding the tab panel.
const panelCard = () => screen.getByRole('tabpanel').closest('.lg\\:col-span-8') as HTMLElement;

describe('DFLT-00368 the panel is pinned to the graph height from lg up only', () => {
  it('hands the measured height over as a CSS variable used by lg:h-(...) only', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
      () => ({ x: 0, y: 0, top: 0, left: 0, right: 400, bottom: 640, width: 400, height: 640, toJSON: () => ({}) }) as DOMRect
    );
    renderTicket();
    const card = panelCard();
    expect(card).not.toBeNull();
    expectClasses(card, ['lg:h-(--node-list-card-height)', 'min-h-128', 'flex', 'flex-col', 'overflow-hidden', 'lg:sticky', 'lg:top-20']);
    // No unprefixed height class: below lg the height stays auto.
    for (const c of classes(card)) expect(c, `unexpected unprefixed height class "${c}"`).not.toMatch(/^h-/);
    expect(card.style.height).toBe('');
    expect(card.style.getPropertyValue('--node-list-card-height')).toBe('640px');
  });

  it('sets neither the height nor the variable before the graph panel is measured', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
      () => ({ x: 0, y: 0, top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0, toJSON: () => ({}) }) as DOMRect
    );
    renderTicket();
    const card = panelCard();
    expectClasses(card, ['min-h-128', 'lg:h-(--node-list-card-height)']);
    expect(card.style.height).toBe('');
    expect(card.style.getPropertyValue('--node-list-card-height')).toBe('');
    expect(card.getAttribute('style')).toBeNull();
  });

  it('keeps the scroll area flex-1 min-h-0 overflow-y-auto inside the card', () => {
    renderTicket();
    const tabpanel = screen.getByRole('tabpanel');
    expectClasses(tabpanel, ['flex-1', 'min-h-0', 'overflow-y-auto']);
    expect(panelCard().contains(tabpanel)).toBe(true);
  });
});

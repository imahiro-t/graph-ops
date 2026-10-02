// DFLT-00365: in the Gherkin and HTML tabs the artifact header row (name on
// the left, "open in new tab" -- plus the date in the Gherkin tab -- on the
// right) was a plain flex justify-between row with no gap, and the name had
// no min-w-0. With a long name the two sides met, and the blue focus ring
// DFLT-00363 gave the link (ring-2, a 2px box-shadow outside the link)
// overlapped the end of the name by ~2px; a name with no break opportunity
// could also push the link out of the card. The rows now take the classes
// the other artifact header rows use (ARTIFACT_HEADER_*), except that the
// row's vertical gap is gap-y-2: gap-x-2 (8px) between the sides, and 8px
// (gap-y-2) when the row wraps under 80rem and the link moves under the
// name (gap-y-1 left only ~5px there, ~3px outside the ring); a name that
// shrinks and breaks (min-w-0 wrap-break-word, never truncate or
// wrap-anywhere), and a right group that moves to the next line right-aligned
// under 80rem and does not shrink from 80rem up.
//
// jsdom does no layout and ignores media queries, so the geometry (the 8px
// gap, the ring clear of the name, nothing sticking out at 320px / 200%) was
// measured in a real browser in light and dark mode (see the implementation
// notes). These tests pin the classes.
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, ArtifactType, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const art = (id: string, name: string, type: ArtifactType, content: string | null): Artifact => ({
  id,
  ticket_id: 'TEST-00365',
  node_id: 'TEST-00365-01',
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const LONG_GHERKIN_NAME = 'gherkinspecificationforthefocusringgapbetweenthenameandtheopeninnewtablink0123456789';
const LONG_HTML_NAME = 'テストレポート（Gherkin・HTML タブの見出し行でフォーカスリングがタイトルに重ならないことの確認結果）';

const ARTIFACTS: Artifact[] = [
  art('a-gherkin-long', LONG_GHERKIN_NAME, 'gherkin', 'Feature: 仕様\n'),
  art('a-gherkin-short', '短い仕様', 'gherkin', 'Feature: 短い\n'),
  art('a-html-long', LONG_HTML_NAME, 'html', '<p>report</p>'),
  art('a-html-short', 'レポート', 'html', '<p>short</p>'),
  // No stored bytes: no "open in new tab" link, so the right group is empty.
  art('a-html-empty', '内容のないレポート', 'html', null)
];

const ticket: TicketDetail = {
  id: 'TEST-00365',
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
  nodes: [],
  edges: [],
  artifacts: ARTIFACTS
};

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(() => {
  vi.unstubAllGlobals();
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

const openTab = (tab: 'gherkin' | 'html', count: number) =>
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

// gap-y-2, not the gap-y-1 of the other artifact header rows: once the row
// wraps, the link sits under the name and must keep 8px from it too.
const ROW = ['flex', 'items-center', 'justify-between', 'gap-x-2', 'below-80rem:flex-wrap', 'below-80rem:gap-y-2'];
const ROW_FORBIDDEN = ['below-80rem:gap-y-1', 'gap-y-1', 'gap-0'];
const NAME = ['min-w-0', 'wrap-break-word'];
const NAME_FORBIDDEN = ['truncate', 'wrap-anywhere', 'basis-0', 'whitespace-nowrap', 'overflow-hidden'];
const RIGHT_GROUP = [
  'flex',
  'items-center',
  'below-80rem:flex-wrap',
  'below-80rem:ml-auto',
  'below-80rem:min-w-0',
  'below-80rem:max-w-full',
  'below-80rem:justify-end-safe',
  'from-80rem:shrink-0'
];
const RING = ['focus-visible:ring-2', 'focus-visible:ring-blue-500', 'dark:focus-visible:ring-blue-400'];

const newTabLinkFor = (id: string) =>
  screen
    .getAllByRole('link', { name: i18n.t('ticketItem.openInNewTab') })
    .find(l => l.getAttribute('href')?.startsWith(`/artifacts/${id}/`));

// The header row is the name's parent; it has exactly the name and the right
// group as children.
const headerParts = (name: string) => {
  const nameEl = screen.getByText(name);
  const row = nameEl.parentElement!;
  expect(row.children).toHaveLength(2);
  expect(row.children[0]).toBe(nameEl);
  return { row, nameEl, right: row.children[1] as HTMLElement };
};

describe('DFLT-00365 Gherkin tab header row keeps the link clear of the name', () => {
  it.each([LONG_GHERKIN_NAME, '短い仕様'])('row, name and right group carry the gap / wrap classes (%s)', name => {
    renderTicket();
    openTab('gherkin', 2);
    const { row, nameEl, right } = headerParts(name);
    expectClasses(row, ROW);
    expectNoClasses(row, ROW_FORBIDDEN);
    // The existing look is kept.
    expectClasses(row, ['font-bold', 'text-xs', 'mb-2']);
    expectClasses(nameEl, NAME);
    expectNoClasses(nameEl, NAME_FORBIDDEN);
    expectClasses(right, [...RIGHT_GROUP, 'gap-3', 'below-80rem:gap-y-1']);
  });

  it('puts the link (with its ring) and the date in the right group', () => {
    renderTicket();
    openTab('gherkin', 2);
    const { right } = headerParts(LONG_GHERKIN_NAME);
    const link = newTabLinkFor('a-gherkin-long')!;
    expect(link).toBeDefined();
    expect(link.parentElement).toBe(right);
    expectClasses(link, RING);
    expect(right.children).toHaveLength(2);
    expect(right.children[1].textContent).not.toBe('');
  });
});

describe('DFLT-00365 HTML tab header row keeps the link clear of the name', () => {
  it.each([LONG_HTML_NAME, 'レポート'])('row, name and right group carry the gap / wrap classes (%s)', name => {
    renderTicket();
    openTab('html', 3);
    const { row, nameEl, right } = headerParts(name);
    expectClasses(row, [...ROW, 'mb-2']);
    expectNoClasses(row, ROW_FORBIDDEN);
    expectClasses(nameEl, [...NAME, 'font-bold', 'text-xs']);
    expectNoClasses(nameEl, NAME_FORBIDDEN);
    expectClasses(right, RIGHT_GROUP);
  });

  it('wraps the link in a flex span so the link stays as wide as its label', () => {
    renderTicket();
    openTab('html', 3);
    const { right } = headerParts(LONG_HTML_NAME);
    const link = newTabLinkFor('a-html-long')!;
    expect(link).toBeDefined();
    expect(link.parentElement).toBe(right);
    expect(right.tagName).toBe('SPAN');
    expect(right.children).toHaveLength(1);
    expectClasses(link, RING);
  });

  it('leaves the right group empty for an artifact with no content', () => {
    renderTicket();
    openTab('html', 3);
    const { right } = headerParts('内容のないレポート');
    expect(right.children).toHaveLength(0);
    expect(newTabLinkFor('a-html-empty')).toBeUndefined();
  });
});

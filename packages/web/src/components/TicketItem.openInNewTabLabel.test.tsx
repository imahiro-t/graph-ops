// DFLT-00366: in the Gherkin and HTML tabs a long artifact name used to
// squeeze the "open in new tab" label (two lines at 1457px, three lines of
// two characters at 375px). DFLT-00365's header row classes already fixed
// that: measured in a real browser (Chrome, light and dark, ja and en), the
// label is one line at 1457px, 1280px, 375px and 320px for long names with
// no break opportunity, long Japanese names and short names alike, and at
// 320px with a 200% font it wraps between words or after a few characters
// (2-3 lines) because the label is wider than the ~90-106px row -- never one
// character a line. What was still wrong there was the Gherkin tab's date:
// "2026/10/3" is wider than the ~90px right group, so it ran 7-13px past it
// into the card's padding. The date now takes ARTIFACT_HEADER_LABEL_TEXT like
// the type badge.
//
// The link itself is deliberately not given downloadLink's structure (label
// span with ARTIFACT_HEADER_LABEL_TEXT, min-w-0 on the <a>, shrink-0 on the
// icon): openInNewTabLink is shared with the node tab, whose row is ~64px at
// 320px / 200%, and there the icon's shrink-0 left the Japanese label one
// character a line (6 lines instead of 3). These tests pin the classes;
// jsdom does no layout, so the geometry is in the implementation notes.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, ArtifactType, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const art = (id: string, name: string, type: ArtifactType, content: string | null): Artifact => ({
  id,
  ticket_id: 'TEST-00366',
  node_id: 'TEST-00366-01',
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

// No break opportunity at all, like the names that squeezed the label.
const LONG_GHERKIN_NAME = 'gherkinspecverificationforartifactheaderrowwrappingandopeninnewtablabel0123456789abcdef';
const LONG_HTML_NAME = 'htmlreportverificationforartifactheaderrowwrappingandopeninnewtablabel0123456789abcdef';
const SHORT_GHERKIN_NAME = 'spec';
const SHORT_HTML_NAME = 'report';

const ARTIFACTS: Artifact[] = [
  art('g-long', LONG_GHERKIN_NAME, 'gherkin', 'Feature: 仕様\n'),
  art('g-short', SHORT_GHERKIN_NAME, 'gherkin', 'Feature: 短い\n'),
  art('h-long', LONG_HTML_NAME, 'html', '<p>report</p>'),
  art('h-short', SHORT_HTML_NAME, 'html', '<p>short</p>')
];

const ticket: TicketDetail = {
  id: 'TEST-00366',
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

afterEach(async () => {
  vi.unstubAllGlobals();
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

const ROW = ['flex', 'justify-between', 'gap-x-2', 'below-80rem:flex-wrap', 'below-80rem:gap-y-2'];
const RIGHT_GROUP = [
  'below-80rem:flex-wrap',
  'below-80rem:ml-auto',
  'below-80rem:min-w-0',
  'below-80rem:max-w-full',
  'below-80rem:justify-end-safe',
  'from-80rem:shrink-0'
];
const NAME = ['min-w-0', 'wrap-break-word'];
const LINK = [
  'flex',
  'items-center',
  'gap-1',
  'text-[0.6875rem]',
  'focus-visible:ring-2',
  'focus-visible:ring-blue-500',
  'dark:focus-visible:ring-blue-400'
];
// See the header comment: these would make the node tab's label one
// character a line at 320px / 200%.
const LINK_FORBIDDEN = ['below-80rem:min-w-0', 'min-w-0', 'whitespace-nowrap', 'shrink-0'];
const LABEL_TEXT = ['below-80rem:min-w-0', 'below-80rem:wrap-break-word', 'from-80rem:whitespace-nowrap'];

const newTabLinkFor = (id: string) =>
  screen
    .getAllByRole('link', { name: i18n.t('ticketItem.openInNewTab') })
    .find(l => l.getAttribute('href')?.startsWith(`/artifacts/${id}/`))!;

const headerParts = (name: string) => {
  const nameEl = screen.getByText(name);
  const row = nameEl.parentElement!;
  expect(row.children).toHaveLength(2);
  return { row, nameEl, right: row.children[1] as HTMLElement };
};

// The link keeps its current shape: icon (not shrink-0) followed directly by
// the label text, no wrapper span.
const expectLinkShape = (link: HTMLElement) => {
  expectClasses(link, LINK);
  expectNoClasses(link, LINK_FORBIDDEN);
  expect(link.children).toHaveLength(1);
  const icon = link.children[0];
  expect(icon.tagName.toLowerCase()).toBe('svg');
  expect(icon.getAttribute('aria-hidden')).toBe('true');
  expectClasses(icon, ['w-3', 'h-3']);
  expectNoClasses(icon, ['shrink-0']);
  expect(link.textContent).toBe(i18n.t('ticketItem.openInNewTab'));
};

describe('DFLT-00366 Gherkin tab: "open in new tab" label and date in the header row', () => {
  it.each([
    ['g-long', LONG_GHERKIN_NAME],
    ['g-short', SHORT_GHERKIN_NAME]
  ])('row, name, right group, link and date carry the wrap classes (%s)', (id, name) => {
    renderTicket();
    openTab('gherkin', 2);
    const { row, nameEl, right } = headerParts(name);
    expectClasses(row, ROW);
    expectClasses(nameEl, NAME);
    expectClasses(right, [...RIGHT_GROUP, 'flex', 'items-center', 'gap-3', 'below-80rem:gap-y-1']);
    const link = newTabLinkFor(id);
    expect(link.parentElement).toBe(right);
    expectLinkShape(link);
    // The date: may break under 80rem instead of running past the right
    // group, one line from 80rem up.
    expect(right.children).toHaveLength(2);
    const date = right.children[1];
    expect(date.textContent).not.toBe('');
    expectClasses(date, [...LABEL_TEXT, 'text-[0.625rem]']);
    expectNoClasses(date, ['truncate', 'wrap-anywhere', 'whitespace-nowrap']);
  });
});

describe('DFLT-00366 HTML tab: "open in new tab" label in the header row', () => {
  it.each([
    ['h-long', LONG_HTML_NAME],
    ['h-short', SHORT_HTML_NAME]
  ])('row, name, right group and link carry the wrap classes (%s)', (id, name) => {
    renderTicket();
    openTab('html', 2);
    const { row, nameEl, right } = headerParts(name);
    expectClasses(row, ROW);
    expectClasses(nameEl, NAME);
    expectClasses(right, [...RIGHT_GROUP, 'flex', 'items-center']);
    const link = newTabLinkFor(id);
    expect(link.parentElement).toBe(right);
    expect(right.children).toHaveLength(1);
    expectLinkShape(link);
  });
});

describe('DFLT-00366 the link is the same in every tab', () => {
  it('Artifacts tab rows use the same link classes as the Gherkin/HTML header rows', () => {
    renderTicket();
    openTab('gherkin', 2);
    const gherkinLinkClass = newTabLinkFor('g-long').getAttribute('class');
    openTab('artifacts', 4);
    const panel = screen.getByRole('tabpanel');
    const links = within(panel).getAllByRole('link', { name: i18n.t('ticketItem.openInNewTab') });
    expect(links.length).toBeGreaterThan(0);
    for (const l of links) expect(l.getAttribute('class')).toBe(gherkinLinkClass);
  });

  it('keeps the accessible name in Japanese and English', async () => {
    renderTicket();
    openTab('html', 2);
    expect(screen.getAllByRole('link', { name: '別タブで開く' })).toHaveLength(2);
    await i18n.changeLanguage('en');
    expect(await screen.findAllByRole('link', { name: 'Open in new tab' })).toHaveLength(2);
  });
});

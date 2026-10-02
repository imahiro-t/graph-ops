// DFLT-00363: DFLT-00345 gave the artifact card's Download link the blue
// focus-visible ring the other controls use. The two other links in the
// artifact panel -- "open in new tab" (openInNewTabLink, in the node, Gherkin,
// HTML and Artifacts tabs) and "download all artifacts" at the right end of
// the tab row -- still showed the browser's default outline. They now get the
// same ring (ring-blue-500, ring-blue-400 in dark mode, on focus-visible only,
// no default outline). The "download all" link keeps its rounded-lg so the
// ring follows its border.
//
// jsdom does no layout, so whether the ring is clipped by the card or the
// panel's overflow was checked in a real browser (see the implementation
// notes). These tests pin the classes.
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, ArtifactType, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const art = (id: string, name: string, type: ArtifactType, content: string | null): Artifact => ({
  id,
  ticket_id: 'TEST-00363',
  node_id: 'TEST-00363-01',
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const ARTIFACTS: Artifact[] = [
  art('a-plan', '実行計画', 'text', '# 計画\n'),
  art('a-gherkin', 'Gherkin 仕様', 'gherkin', 'Feature: 仕様\n'),
  art('a-html', 'テストレポート', 'html', '<p>report</p>')
];

const NODE: GraphNode = {
  id: 'TEST-00363-01',
  ticket_id: 'TEST-00363',
  name: '計画作成',
  type: 'plan',
  status: 'DONE',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
};

const ticket: TicketDetail = {
  id: 'TEST-00363',
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

const openNodeDetail = () => {
  const occurrences = screen.getAllByText('計画作成');
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
  for (const c of forbidden) expect(has, `unexpected "${c}" on <${el.tagName.toLowerCase()}>`).not.toContain(c);
};

const RING = ['focus:outline-hidden', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'dark:focus-visible:ring-blue-400'];
// A permanent ring or the browser's default outline.
const NOT_KEYBOARD_ONLY = ['ring-2', 'focus:ring-2', 'outline', 'focus:outline'];

const newTabLinks = () => screen.getAllByRole('link', { name: i18n.t('ticketItem.openInNewTab') });

const checkNewTabLink = (link: HTMLElement) => {
  expectClasses(link, ['rounded-sm', ...RING]);
  expectNoClasses(link, NOT_KEYBOARD_ONLY);
  expect(link.tagName).toBe('A');
  expect(link).not.toHaveAttribute('tabindex');
  link.focus();
  expect(document.activeElement).toBe(link);
};

describe('DFLT-00363 the "open in new tab" link shows the blue focus ring', () => {
  it('in the node detail', () => {
    renderTicket();
    openNodeDetail();
    const links = newTabLinks();
    expect(links).toHaveLength(3);
    links.forEach(checkNewTabLink);
  });

  it('in the Gherkin tab', () => {
    renderTicket();
    openTab('gherkin', 1);
    const links = newTabLinks();
    expect(links).toHaveLength(1);
    links.forEach(checkNewTabLink);
  });

  it('in the HTML tab', () => {
    renderTicket();
    openTab('html', 1);
    const links = newTabLinks();
    expect(links).toHaveLength(1);
    links.forEach(checkNewTabLink);
  });

  it('in the Artifacts tab, where the html link is shrunk to its label', () => {
    renderTicket();
    openTab('artifacts', ARTIFACTS.length);
    const links = newTabLinks();
    expect(links).toHaveLength(3);
    links.forEach(checkNewTabLink);
    // The html artifact's link sits in a block of its own; a flex parent keeps
    // it (and its ring) from stretching across the whole card.
    const htmlLink = links.find(l => l.getAttribute('href')?.startsWith('/artifacts/a-html/'))!;
    expect(htmlLink).toBeDefined();
    expectClasses(htmlLink.parentElement!, ['mt-2', 'flex']);
  });
});

describe('DFLT-00363 the "download all artifacts" link shows the blue focus ring', () => {
  it('has the ring on keyboard focus only and keeps its rounded-lg border', () => {
    renderTicket();
    const link = screen.getByRole('link', { name: i18n.t('ticketItem.downloadAllArtifacts') });
    expect(link).toHaveAttribute('href', '/api/tickets/TEST-00363/artifacts/download');
    expectClasses(link, ['rounded-lg', 'border', ...RING]);
    expectNoClasses(link, [...NOT_KEYBOARD_ONLY, 'rounded-sm']);
    expect(link).not.toHaveAttribute('tabindex');
    link.focus();
    expect(document.activeElement).toBe(link);
  });
});

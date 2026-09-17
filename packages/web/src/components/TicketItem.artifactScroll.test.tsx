// DFLT-00085: inline text/gherkin artifact previews are capped at 16rem and
// scroll inside their own border, in all three places TicketItem shows them
// (node detail, Gherkin tab, Artifacts tab) -- while the ticket description
// and the default (new-tab preview) render stay uncapped.
//
// jsdom does not do layout, so "is it really 16rem tall / can it scroll" is
// not observable here. What these tests pin down is the mechanism that
// produces it: max-h-64 + overflow-y-auto on the bordered box itself, never
// h-64, and never on the opted-out call sites.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { formatDateTime } from '../i18n/formatDate';
import i18n from '../i18n';
import { Artifact, ArtifactType, GraphNode, TicketDetail } from '../types';
import { GherkinViewer } from './GherkinViewer';
import { MarkdownViewer } from './MarkdownViewer';
import { TicketItem } from './TicketItem';

const LONG_DESCRIPTION = Array.from({ length: 60 }, (_, i) => `説明の ${i + 1} 行目`).join('\n\n');

const art = (
  id: string,
  name: string,
  type: ArtifactType,
  content: string,
  created_at = '2026-01-01T00:00:00Z'
): Artifact => ({
  id,
  ticket_id: 'TEST-00085',
  node_id: 'TEST-00085-01',
  name,
  type,
  content,
  created_at
});

const GHERKIN = art('a-gherkin', 'Gherkin仕様', 'gherkin', 'Feature: 長い仕様\n  Scenario: 例\n    Given 前提\n');
const TEXT = art('a-text', '実行計画', 'text', '# 計画\n\n長い本文\n');

// Same name, different creation times -- what a review loop-back produces
// (the implementation node runs twice, so "実装メモ" exists twice on one
// node). The short gherkin pair covers the same thing for content that does
// not reach 16rem, which criterion 2 cares about.
const NOTES_1 = art('a-notes-1', '実装メモ', 'text', '# 1 回目\n', '2026-01-01T09:00:00Z');
const NOTES_2 = art('a-notes-2', '実装メモ', 'text', '# 2 回目\n', '2026-01-01T15:30:00Z');
const SHORT_GHERKIN_1 = art('a-short-1', '仕様', 'gherkin', 'Feature: 短い\n', '2026-01-01T09:05:00Z');
const SHORT_GHERKIN_2 = art('a-short-2', '仕様', 'gherkin', 'Feature: 短い（2 回目）\n', '2026-01-01T15:35:00Z');

const PLAN_NODE: GraphNode = {
  id: 'TEST-00085-01',
  ticket_id: 'TEST-00085',
  name: '計画作成',
  type: 'plan',
  status: 'DONE',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
};

const makeTicket = (artifacts: Artifact[] = [GHERKIN, TEXT]): TicketDetail => ({
  id: 'TEST-00085',
  project_id: 'proj-1',
  title: 'タイトル',
  description: LONG_DESCRIPTION,
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [PLAN_NODE],
  edges: [],
  artifacts
});

function renderExpanded(artifacts?: Artifact[]) {
  return render(
    <TicketItem
      ticket={makeTicket(artifacts)}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );
}

// The accessible name of a capped preview: artifact name *and* creation
// time. The name alone repeats across a ticket once a review loops a node
// back, so the timestamp is what keeps each region individually addressable
// -- for a screen-reader user and, here, for getByRole.
const regionName = (artifact: Artifact) =>
  i18n.t('ticketItem.artifactScrollRegion', {
    name: artifact.name,
    timestamp: formatDateTime(artifact.created_at, i18n.language)
  });

// Each capped preview is an accessible region named after its artifact, so
// tests (like a screen-reader user) can address one specific preview even
// when the description and several artifacts are on screen at once.
function scrollBox(artifact: Artifact, testId: 'gherkin-viewer' | 'markdown-viewer') {
  const el = screen.getByRole('region', { name: regionName(artifact) });
  expect(el).toHaveAttribute('data-testid', testId);
  expect(el.className).toContain('max-h-64');
  expect(el.className).toContain('overflow-y-auto');
  // A fixed height would stretch a short artifact and leave dead space --
  // completion criterion 2.
  expect(el.className).not.toMatch(/(^|\s)h-64(\s|$)/);
  // The box that carries the border is the scroller, so the frame stays put
  // and only the content moves.
  expect(el.className).toContain('border');
  // A scroll container must be reachable and scrollable by keyboard.
  expect(el).toHaveAttribute('tabindex', '0');
  return el;
}

// TicketItem measures its graph panel with a ResizeObserver, which jsdom
// does not implement (same stub as TicketItem.labels.test.tsx).
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// The node's name appears both in the graph panel and in the node list; the
// list row (the last one) is the accordion header that expands the artifacts.
const openNodeDetail = () => {
  const occurrences = screen.getAllByText('計画作成');
  fireEvent.click(occurrences[occurrences.length - 1]);
};
const openTab = (key: 'gherkin' | 'artifacts', count: number) =>
  fireEvent.click(screen.getByText(i18n.t(`ticketItem.tabs.${key}`, { count })));

describe('DFLT-00085 inline artifact previews scroll inside a 16rem box', () => {
  it('caps the gherkin and text previews in the node detail', () => {
    renderExpanded();
    openNodeDetail();

    scrollBox(GHERKIN, 'gherkin-viewer');
    scrollBox(TEXT, 'markdown-viewer');
  });

  it('keeps the "open in new tab" link outside the scroll box', () => {
    renderExpanded();
    openNodeDetail();

    const box = scrollBox(GHERKIN, 'gherkin-viewer');
    expect(within(box).queryByText(i18n.t('ticketItem.openInNewTab'))).toBeNull();
    expect(screen.getAllByText(i18n.t('ticketItem.openInNewTab')).length).toBeGreaterThan(0);
  });

  it('caps the preview in the Gherkin tab', () => {
    renderExpanded();
    openTab('gherkin', 1);

    scrollBox(GHERKIN, 'gherkin-viewer');
  });

  it('caps both previews in the Artifacts tab', () => {
    renderExpanded();
    openTab('artifacts', 2);

    scrollBox(GHERKIN, 'gherkin-viewer');
    scrollBox(TEXT, 'markdown-viewer');
  });

  it('leaves the ticket description uncapped', () => {
    renderExpanded();

    // Nothing is expanded yet, so the only markdown on screen is the
    // description -- it must not have picked up the artifact cap.
    const description = screen.getByTestId('markdown-viewer');
    expect(description.className).not.toContain('max-h-64');
    expect(description).not.toHaveAttribute('tabindex');
    expect(screen.queryByRole('region')).toBeNull();
  });

  it('renders both viewers uncapped by default (the new-tab preview page)', () => {
    const { rerender } = render(<MarkdownViewer content="# 全文" />);
    const md = screen.getByTestId('markdown-viewer');
    expect(md.className).not.toContain('max-h-64');
    expect(md.className).not.toContain('overflow-y-auto');
    expect(md).not.toHaveAttribute('tabindex');

    rerender(<GherkinViewer content="Feature: 全文" />);
    const gk = screen.getByTestId('gherkin-viewer');
    expect(gk.className).not.toContain('max-h-64');
    expect(gk.className).not.toContain('overflow-y-auto');
    expect(gk).not.toHaveAttribute('tabindex');
  });

  // outline-none makes the ring the only focus indicator, so its color has to
  // clear WCAG 1.4.11 / 2.4.11's 3:1 against the light theme's white and
  // slate-50. indigo-400 did not (2.85-2.98:1); blue-500 does. Contrast is a
  // property of the color, not of the DOM, so what is pinned here is the
  // class -- the accessibility review's measured numbers are in the viewers'
  // comments.
  it('uses a focus ring color that clears 3:1 on the light theme', () => {
    const { rerender } = render(<MarkdownViewer content="# 本文" scrollable label="名前" />);
    const md = screen.getByTestId('markdown-viewer');
    expect(md.className).toContain('focus-visible:ring-blue-500');
    expect(md.className).not.toContain('ring-indigo-400');

    rerender(<GherkinViewer content="Feature: 本文" scrollable label="名前" />);
    const gk = screen.getByTestId('gherkin-viewer');
    expect(gk.className).toContain('focus-visible:ring-blue-500');
    expect(gk.className).not.toContain('ring-indigo-400');
  });
});

// A loop-back re-runs a node, so the same artifact name shows up more than
// once -- the very situation this ticket exists for ("差し戻しで成果物が
// 増える"). Sighted users separate them by position and timestamp; these
// tests hold the line that assistive technology gets the same information,
// i.e. that no two previews share an accessible name.
describe('DFLT-00085 same-named artifacts stay individually addressable', () => {
  const DUPLICATES = [NOTES_1, NOTES_2, SHORT_GHERKIN_1, SHORT_GHERKIN_2];

  const expectUniqueRegionNames = () => {
    const names = screen
      .getAllByRole('region')
      .map(el => el.getAttribute('aria-label'));
    expect(names.length).toBeGreaterThan(1);
    expect(new Set(names).size).toBe(names.length);
  };

  it('names each duplicate preview distinctly in the node detail', () => {
    renderExpanded(DUPLICATES);
    openNodeDetail();

    // getByRole throws on multiple matches, so resolving all four at all is
    // itself the assertion that the names are unique.
    scrollBox(NOTES_1, 'markdown-viewer');
    scrollBox(NOTES_2, 'markdown-viewer');
    // Short content: still capped by max-h-64 and never stretched by h-64.
    scrollBox(SHORT_GHERKIN_1, 'gherkin-viewer');
    scrollBox(SHORT_GHERKIN_2, 'gherkin-viewer');
    expectUniqueRegionNames();
  });

  it('names each duplicate preview distinctly in the Gherkin tab', () => {
    renderExpanded(DUPLICATES);
    openTab('gherkin', 2);

    scrollBox(SHORT_GHERKIN_1, 'gherkin-viewer');
    scrollBox(SHORT_GHERKIN_2, 'gherkin-viewer');
    expectUniqueRegionNames();
  });

  it('names each duplicate preview distinctly in the Artifacts tab', () => {
    renderExpanded(DUPLICATES);
    openTab('artifacts', 4);

    scrollBox(NOTES_1, 'markdown-viewer');
    scrollBox(NOTES_2, 'markdown-viewer');
    scrollBox(SHORT_GHERKIN_1, 'gherkin-viewer');
    scrollBox(SHORT_GHERKIN_2, 'gherkin-viewer');
    expectUniqueRegionNames();
  });

  it('puts the creation time in the accessible name, matching what the UI shows', () => {
    renderExpanded(DUPLICATES);
    openTab('gherkin', 2);

    const name = regionName(SHORT_GHERKIN_1);
    expect(name).toContain(SHORT_GHERKIN_1.name);
    // The Gherkin tab prints this same timestamp next to the artifact, so the
    // accessible name adds no information the screen contradicts.
    const shown = formatDateTime(SHORT_GHERKIN_1.created_at, i18n.language);
    expect(name).toContain(shown);
    expect(screen.getAllByText(shown).length).toBeGreaterThan(0);
  });
});

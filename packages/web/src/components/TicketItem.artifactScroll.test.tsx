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
import i18n from '../i18n';
import { Artifact, ArtifactType, GraphNode, TicketDetail } from '../types';
import { GherkinViewer } from './GherkinViewer';
import { MarkdownViewer } from './MarkdownViewer';
import { TicketItem } from './TicketItem';

const LONG_DESCRIPTION = Array.from({ length: 60 }, (_, i) => `説明の ${i + 1} 行目`).join('\n\n');

const art = (id: string, name: string, type: ArtifactType, content: string): Artifact => ({
  id,
  ticket_id: 'TEST-00085',
  node_id: 'TEST-00085-01',
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const GHERKIN = art('a-gherkin', 'Gherkin仕様', 'gherkin', 'Feature: 長い仕様\n  Scenario: 例\n    Given 前提\n');
const TEXT = art('a-text', '実行計画', 'text', '# 計画\n\n長い本文\n');

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

const makeTicket = (): TicketDetail => ({
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
  artifacts: [GHERKIN, TEXT]
});

function renderExpanded() {
  return render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );
}

// Each capped preview is an accessible region named after its artifact, so
// tests (like a screen-reader user) can address one specific preview even
// when the description and several artifacts are on screen at once.
function scrollBox(artifactName: string, testId: 'gherkin-viewer' | 'markdown-viewer') {
  const el = screen.getByRole('region', {
    name: i18n.t('ticketItem.artifactScrollRegion', { name: artifactName })
  });
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

    scrollBox('Gherkin仕様', 'gherkin-viewer');
    scrollBox('実行計画', 'markdown-viewer');
  });

  it('keeps the "open in new tab" link outside the scroll box', () => {
    renderExpanded();
    openNodeDetail();

    const box = scrollBox('Gherkin仕様', 'gherkin-viewer');
    expect(within(box).queryByText(i18n.t('ticketItem.openInNewTab'))).toBeNull();
    expect(screen.getAllByText(i18n.t('ticketItem.openInNewTab')).length).toBeGreaterThan(0);
  });

  it('caps the preview in the Gherkin tab', () => {
    renderExpanded();
    openTab('gherkin', 1);

    scrollBox('Gherkin仕様', 'gherkin-viewer');
  });

  it('caps both previews in the Artifacts tab', () => {
    renderExpanded();
    openTab('artifacts', 2);

    scrollBox('Gherkin仕様', 'gherkin-viewer');
    scrollBox('実行計画', 'markdown-viewer');
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
});

// DFLT-00334: at 320px with a 200% default font, an artifact's header row
// (icon + name, Download, type badge) is only ~80px wide inside an expanded
// node card (~122px in the Artifacts tab). It used to stay on one line, so the
// name was squeezed to one character per line and Download / the badge ran
// past the card's right edge, where the node card (overflow-hidden) clipped
// them. Under 80rem the row, the name group and the right group now wrap, and
// the name, the Download link and its label, and the badge may shrink to the
// row and break a word only when it is wider than the row.
//
// jsdom does no layout and ignores media queries, so the geometry (nothing
// sticking out, nothing clipped, names not squeezed, one line at 1280px) is
// checked in a real browser and recorded in the implementation notes. These
// tests pin down the classes and structure that produce it: the below-80rem
// wrapping classes are there, the classes used from 80rem up are kept, and no
// truncating class hides the text instead.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, ArtifactType, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const art = (id: string, name: string, type: ArtifactType, content: string): Artifact => ({
  id,
  ticket_id: 'TEST-00334',
  node_id: 'TEST-00334-01',
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const ARTIFACTS: Artifact[] = [
  art('a-plan', '実行計画（成果物見出し行の折り返し）', 'text', '# 計画\n'),
  art('a-gherkin', 'Gherkin 仕様（見出し行の折り返し）', 'gherkin', 'Feature: 仕様\n'),
  art('a-html', 'test-report-cmp-320-200-node06-clip', 'html', '<p>report</p>'),
  art('a-short', '計画', 'text', '短い\n')
];

const NODE: GraphNode = {
  id: 'TEST-00334-01',
  ticket_id: 'TEST-00334',
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
  id: 'TEST-00334',
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

// The node's name appears in the graph panel and in the node list; the list
// row (the last one) is the accordion header that expands the artifacts.
const openNodeDetail = () => {
  const occurrences = screen.getAllByText('計画作成');
  fireEvent.click(occurrences[occurrences.length - 1]);
};
const openArtifactsTab = () =>
  fireEvent.click(screen.getByText(i18n.t('ticketItem.tabs.artifacts', { count: ARTIFACTS.length })));

const classes = (el: Element) => el.getAttribute('class')?.split(/\s+/).filter(Boolean) ?? [];
const expectClasses = (el: Element, expected: string[]) => {
  const has = classes(el);
  for (const c of expected) expect(has, `expected "${c}" on <${el.tagName.toLowerCase()} class="${has.join(' ')}">`).toContain(c);
};
const TRUNCATING = ['truncate', 'whitespace-nowrap', 'overflow-hidden'];
const expectNoClasses = (el: Element, forbidden: string[]) => {
  const has = classes(el);
  for (const c of forbidden) expect(has, `unexpected "${c}" on <${el.tagName.toLowerCase()}>`).not.toContain(c);
};

// Resolves the parts of one artifact's header row, starting from the element
// that holds exactly the artifact's name.
function headerParts(name: string, scope: HTMLElement = document.body) {
  const nameEl = within(scope).getByText(name, { selector: 'span' });
  const nameGroup = nameEl.parentElement!;
  const row = nameGroup.parentElement!;
  const card = row.parentElement!;
  const rightGroup = row.children[1] as HTMLElement;
  const link = within(rightGroup).getByRole('link', { name: i18n.t('ticketItem.download') });
  const label = within(link).getByText(i18n.t('ticketItem.download'));
  const linkIcon = link.querySelector('svg')!;
  const badge = rightGroup.lastElementChild as HTMLElement;
  const icon = nameGroup.querySelector('svg');
  return { nameEl, nameGroup, row, card, rightGroup, link, label, linkIcon, badge, icon };
}

// The expanded node's accordion body (other places on the page, such as the
// text preview, may repeat a short name like 「計画」).
const nodePanel = () =>
  screen.getByText(ARTIFACTS[0].name, { selector: 'span' }).closest('.space-y-3') as HTMLElement;

const artifactsPanel = () => {
  // The Artifacts tab lists every artifact of the ticket; its cards are the
  // p-3 bordered boxes, distinct from the node detail's p-2.5 ones.
  const first = screen.getAllByText(ARTIFACTS[0].name, { selector: 'span' });
  return first[first.length - 1].closest('.space-y-2') as HTMLElement;
};

describe('DFLT-00334 node artifact header rows wrap under 80rem', () => {
  it('lets the row, the name group and the right group wrap while keeping the wide-screen classes', () => {
    renderTicket();
    openNodeDetail();

    for (const a of ARTIFACTS) {
      const p = headerParts(a.name, nodePanel());
      expectClasses(p.row, ['flex', 'items-center', 'justify-between', 'below-80rem:flex-wrap', 'below-80rem:gap-x-2', 'below-80rem:gap-y-1']);
      expectClasses(p.nameGroup, ['flex', 'items-center', 'gap-1.5', 'below-80rem:flex-wrap', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
      expect(p.nameEl).not.toBe(p.nameGroup);
      expectClasses(p.nameEl, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
      expectNoClasses(p.nameEl, ['wrap-anywhere']);
      expectNoClasses(p.nameGroup, ['basis-0']);
      expectClasses(p.rightGroup, ['flex', 'items-center', 'gap-2', 'below-80rem:flex-wrap', 'below-80rem:ml-auto', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
      expect(classes(p.rightGroup).some(c => c === 'below-80rem:justify-end' || c === 'below-80rem:justify-end-safe')).toBe(true);
      expectClasses(p.badge, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
      expect(p.badge).toHaveTextContent(a.type);
      for (const el of [p.row, p.nameGroup, p.nameEl, p.rightGroup, p.badge, p.link]) expectNoClasses(el, TRUNCATING);
      // Card and accordion padding are left alone (the fix is in the row).
      expectClasses(p.card, ['p-2.5']);
      expectClasses(p.card.parentElement!, ['p-3']);
    }
  });

  it('keeps the type icon from shrinking', () => {
    renderTicket();
    openNodeDetail();

    for (const a of ARTIFACTS.filter(x => x.type !== 'image' && x.type !== 'json')) {
      const { icon } = headerParts(a.name, nodePanel());
      expect(icon).not.toBeNull();
      expectClasses(icon!, ['shrink-0', 'w-3.5', 'h-3.5']);
    }
  });
});

describe('DFLT-00334 Artifacts tab header rows wrap under 80rem', () => {
  it('has the same wrapping classes as the node detail', () => {
    renderTicket();
    openArtifactsTab();
    const panel = artifactsPanel();

    for (const a of ARTIFACTS) {
      const p = headerParts(a.name, panel);
      expectClasses(p.card, ['p-3']);
      expectClasses(p.row, ['flex', 'items-center', 'justify-between', 'below-80rem:flex-wrap', 'below-80rem:gap-x-2', 'below-80rem:gap-y-1']);
      expectClasses(p.nameGroup, ['flex', 'items-center', 'gap-2', 'below-80rem:flex-wrap', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
      expectClasses(p.icon!, ['shrink-0']);
      expectClasses(p.nameEl, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
      expectNoClasses(p.nameEl, ['wrap-anywhere']);
      expectNoClasses(p.nameGroup, ['basis-0']);
      expectClasses(p.rightGroup, ['below-80rem:flex-wrap', 'below-80rem:ml-auto', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
      expectClasses(p.badge, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
      for (const el of [p.row, p.nameGroup, p.nameEl, p.rightGroup, p.badge]) expectNoClasses(el, TRUNCATING);
    }
  });
});

describe('DFLT-00334 the Download link may shrink to its row', () => {
  const checkLink = (p: ReturnType<typeof headerParts>, id: string) => {
    expectClasses(p.link, ['flex', 'items-center', 'gap-1', 'below-80rem:min-w-0']);
    expect(p.label).not.toBe(p.link);
    expectClasses(p.label, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
    for (const el of [p.link, p.label]) expectNoClasses(el, [...TRUNCATING, 'wrap-anywhere']);
    if (classes(p.label).includes('break-keep') || classes(p.label).includes('below-80rem:break-keep')) {
      expectClasses(p.label, ['below-80rem:wrap-break-word']);
    }
    expectClasses(p.linkIcon, ['shrink-0']);
    expect(p.link).toHaveAttribute('href', `/api/artifacts/${id}/content?download=1`);
  };

  it('in the node detail', () => {
    renderTicket();
    openNodeDetail();
    for (const a of ARTIFACTS) checkLink(headerParts(a.name, nodePanel()), a.id);
  });

  it('in the Artifacts tab', () => {
    renderTicket();
    openArtifactsTab();
    const panel = artifactsPanel();
    for (const a of ARTIFACTS) checkLink(headerParts(a.name, panel), a.id);
  });

  it('keeps its accessible name in English', async () => {
    const previous = i18n.language;
    await i18n.changeLanguage('en');
    try {
      renderTicket();
      openNodeDetail();
      expect(screen.getAllByRole('link', { name: 'Download' })).toHaveLength(ARTIFACTS.length);
    } finally {
      await i18n.changeLanguage(previous);
    }
  });
});

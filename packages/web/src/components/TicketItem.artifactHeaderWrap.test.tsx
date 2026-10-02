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
//
// DFLT-00345: from 80rem up (1280px, 100% font) a name of ~90 characters with
// no break opportunity used to keep the name group at its full width, so the
// row ran past the card and the right group shrank instead, squeezing
// 「ダウンロード」 and the badge to one character a line. From 80rem up the
// name group and the name now shrink (min-w-0) and the name breaks
// (wrap-break-word), while the right group does not shrink (shrink-0) and the
// Download label and the badge stay on one line (whitespace-nowrap). The
// Download link also gets the blue focus-visible ring the other controls use.
// As above, the geometry is measured in a real browser; these tests pin the
// classes.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, ArtifactType, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const art = (id: string, name: string, type: ArtifactType, content: string | null): Artifact => ({
  id,
  ticket_id: 'TEST-00334',
  node_id: 'TEST-00334-01',
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const LONG_TEXT_NAME = 'implementationnotesforartifactheaderrowwrappingatwidescreenwithaverylongunbrokenname0123456';
const LONG_HTML_NAME = 'testreportcmp1280x100nodedetailandartifactstabheaderrowoverflowmeasurementresults20261002';

const ARTIFACTS: Artifact[] = [
  art('a-plan', '実行計画（成果物見出し行の折り返し）', 'text', '# 計画\n'),
  art('a-gherkin', 'Gherkin 仕様（見出し行の折り返し）', 'gherkin', 'Feature: 仕様\n'),
  art('a-html', 'test-report-cmp-320-200-node06-clip', 'html', '<p>report</p>'),
  art('a-short', '計画', 'text', '短い\n'),
  // Types the node detail shows no icon for, and an artifact with no stored
  // bytes (no Download link, so its right group holds only the badge).
  art('a-image', 'screenshot-320-200-node-detail', 'image', 'iVBORw0KGgo='),
  art('a-json', '計測結果（JSON）', 'json', '{"clip":0}'),
  art('a-empty', '内容のない成果物', 'text', null),
  // DFLT-00345: ~90 characters with no break opportunity.
  art('a-long-text', LONG_TEXT_NAME, 'text', '# long\n'),
  art('a-long-html', LONG_HTML_NAME, 'html', '<p>long</p>')
];
const LONG_NAMES = [LONG_TEXT_NAME, LONG_HTML_NAME];
const WITH_CONTENT = ARTIFACTS.filter(a => a.content);
// The types the node detail header shows an icon for.
const TYPES_WITH_NODE_ICON: ArtifactType[] = ['gherkin', 'html', 'text'];

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
  const link = within(rightGroup).queryByRole('link', { name: i18n.t('ticketItem.download') });
  const badge = rightGroup.lastElementChild as HTMLElement;
  const icon = nameGroup.querySelector('svg');
  return { nameEl, nameGroup, row, card, rightGroup, link, badge, icon };
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
      for (const el of [p.row, p.nameGroup, p.nameEl, p.rightGroup, p.badge]) expectNoClasses(el, TRUNCATING);
      // Card and accordion padding are left alone (the fix is in the row).
      expectClasses(p.card, ['p-2.5']);
      expectClasses(p.card.parentElement!, ['p-3']);
    }
  });

  it('keeps the type icon from shrinking, and shows none for image or json', () => {
    renderTicket();
    openNodeDetail();

    for (const a of ARTIFACTS) {
      const { icon } = headerParts(a.name, nodePanel());
      if (TYPES_WITH_NODE_ICON.includes(a.type)) {
        expect(icon, a.name).not.toBeNull();
        expectClasses(icon!, ['shrink-0', 'w-3.5', 'h-3.5']);
      } else {
        expect(icon, a.name).toBeNull();
      }
    }
  });

  it('leaves only the badge in the right group when there is nothing to download', () => {
    renderTicket();
    openNodeDetail();

    const p = headerParts('内容のない成果物', nodePanel());
    expect(p.link).toBeNull();
    expect(p.rightGroup.children).toHaveLength(1);
    expect(p.badge).toHaveTextContent('text');
    expectClasses(p.rightGroup, ['below-80rem:flex-wrap', 'below-80rem:ml-auto', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
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
    const link = p.link!;
    expect(link, id).not.toBeNull();
    const label = within(link).getByText(i18n.t('ticketItem.download'));
    expectClasses(link, ['flex', 'items-center', 'gap-1', 'below-80rem:min-w-0']);
    expect(label).not.toBe(link);
    expectClasses(label, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
    // break-keep would keep 「ダウンロード」 (164px) on one line in an 80px row.
    for (const el of [link, label]) expectNoClasses(el, [...TRUNCATING, 'wrap-anywhere', 'break-keep', 'below-80rem:break-keep']);
    expectClasses(link.querySelector('svg')!, ['shrink-0']);
    expect(link).toHaveAttribute('href', `/api/artifacts/${id}/content?download=1`);
  };

  it('in the node detail', () => {
    renderTicket();
    openNodeDetail();
    for (const a of WITH_CONTENT) checkLink(headerParts(a.name, nodePanel()), a.id);
  });

  it('in the Artifacts tab', () => {
    renderTicket();
    openArtifactsTab();
    const panel = artifactsPanel();
    for (const a of WITH_CONTENT) checkLink(headerParts(a.name, panel), a.id);
  });

  it('keeps its accessible name in English', async () => {
    const previous = i18n.language;
    await i18n.changeLanguage('en');
    try {
      renderTicket();
      openNodeDetail();
      expect(screen.getAllByRole('link', { name: 'Download' })).toHaveLength(WITH_CONTENT.length);
    } finally {
      await i18n.changeLanguage(previous);
    }
  });
});

describe('DFLT-00345 from 80rem up only the name side shrinks and wraps', () => {
  const FROM_80_ROW = ['from-80rem:gap-x-2'];
  const FROM_80_NAME_GROUP = ['from-80rem:min-w-0'];
  const FROM_80_NAME = ['from-80rem:min-w-0', 'from-80rem:wrap-break-word'];
  const FROM_80_RIGHT_GROUP = ['from-80rem:shrink-0'];
  const FROM_80_LABEL = ['from-80rem:whitespace-nowrap'];

  const checkRow = (p: ReturnType<typeof headerParts>, a: Artifact) => {
    // The one-line layout of the row (name left, right group right) is kept.
    expectClasses(p.row, ['flex', 'items-center', 'justify-between', ...FROM_80_ROW]);
    expectClasses(p.nameGroup, ['flex', 'items-center', ...FROM_80_NAME_GROUP]);
    expectClasses(p.nameEl, FROM_80_NAME);
    expectClasses(p.rightGroup, ['flex', 'items-center', ...FROM_80_RIGHT_GROUP]);
    expectClasses(p.badge, FROM_80_LABEL);
    expect(p.badge).toHaveTextContent(a.type);
    if (p.link) expectClasses(within(p.link).getByText(i18n.t('ticketItem.download')), FROM_80_LABEL);
    // The name never gets a class that hides it or lets it collapse to one
    // character a line, and nowrap is only on the right side.
    expectNoClasses(p.nameEl, ['truncate', 'wrap-anywhere', 'from-80rem:wrap-anywhere', 'basis-0', 'from-80rem:basis-0', 'from-80rem:whitespace-nowrap', 'from-80rem:truncate']);
    expectNoClasses(p.nameGroup, ['basis-0', 'from-80rem:basis-0', 'from-80rem:shrink-0', 'from-80rem:whitespace-nowrap']);
    // Nothing unconditional is added that would also change the DFLT-00334
    // behaviour under 80rem.
    for (const el of [p.row, p.nameGroup, p.nameEl, p.rightGroup, p.badge]) expectNoClasses(el, ['min-w-0', 'shrink-0', 'whitespace-nowrap', 'wrap-break-word', ...TRUNCATING]);
    // The DFLT-00334 below-80rem classes are still there.
    expectClasses(p.row, ['below-80rem:flex-wrap', 'below-80rem:gap-x-2', 'below-80rem:gap-y-1']);
    expectClasses(p.nameGroup, ['below-80rem:flex-wrap', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
    expectClasses(p.nameEl, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
    expectClasses(p.rightGroup, ['below-80rem:flex-wrap', 'below-80rem:ml-auto', 'below-80rem:min-w-0', 'below-80rem:max-w-full']);
    expectClasses(p.badge, ['below-80rem:min-w-0', 'below-80rem:wrap-break-word']);
  };

  it('uses the long unbroken names the bug was found with', () => {
    for (const name of LONG_NAMES) {
      expect(name.length).toBeGreaterThanOrEqual(85);
      expect(name).toMatch(/^[a-z0-9]+$/);
    }
  });

  it('in the node detail, for long and short names alike', () => {
    renderTicket();
    openNodeDetail();
    for (const a of ARTIFACTS) checkRow(headerParts(a.name, nodePanel()), a);
  });

  it('in the Artifacts tab, for long and short names alike', () => {
    renderTicket();
    openArtifactsTab();
    const panel = artifactsPanel();
    for (const a of ARTIFACTS) checkRow(headerParts(a.name, panel), a);
  });
});

describe('DFLT-00345 the Download link shows the blue focus ring', () => {
  const FOCUS_RING = ['rounded-sm', 'focus:outline-hidden', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'dark:focus-visible:ring-blue-400'];

  const checkFocus = (p: ReturnType<typeof headerParts>, id: string) => {
    const link = p.link!;
    expect(link, id).not.toBeNull();
    expectClasses(link, FOCUS_RING);
    // The ring appears on keyboard focus only, not as a permanent ring.
    expectNoClasses(link, ['ring-2', 'focus:ring-2', 'outline', 'focus:outline']);
    // Still a real link, so it stays in the Tab order and can take focus.
    expect(link.tagName).toBe('A');
    expect(link).toHaveAttribute('href', `/api/artifacts/${id}/content?download=1`);
    expect(link).not.toHaveAttribute('tabindex');
    link.focus();
    expect(document.activeElement).toBe(link);
  };

  it('in the node detail', () => {
    renderTicket();
    openNodeDetail();
    for (const a of WITH_CONTENT) checkFocus(headerParts(a.name, nodePanel()), a.id);
  });

  it('in the Artifacts tab', () => {
    renderTicket();
    openArtifactsTab();
    const panel = artifactsPanel();
    for (const a of WITH_CONTENT) checkFocus(headerParts(a.name, panel), a.id);
  });
});

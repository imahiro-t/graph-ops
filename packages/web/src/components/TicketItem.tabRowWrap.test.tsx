// DFLT-00238: the artifact panel's tab row wraps below lg instead of running
// past the panel. At 320-375px (and with a 200% default font or zoom) the
// four tabs and the "download all" link reached 415-1260px while the panel's
// overflow-hidden clipped them at its right edge, so the last tabs and the
// link could get keyboard focus without being seen. Now the row and its tab
// group wrap below lg, the tabs and the link may shrink and break their
// label, and with a large default font on a narrow screen (under 15rem) they
// pad less and put their icon on its own line, so the stacked tabs stay
// within the panel's pinned height. From lg up the tab group stays on one
// line; DFLT-00240 moved the row's own single-line boundary to xl, because
// at 1024px in English the link still ran about 14px past the panel, so
// between lg and xl the link drops below the tabs when it does not fit, and
// the tab icons keep their 16px at every width (shrink-0). jsdom does no
// layout (nor media queries), so this checks the classes; the geometry at
// 160-1440px, with a 200% default font and 3-digit counts, was measured in a
// real browser (see the tickets' implementation notes).
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const NARROW_LARGE_TEXT = '[@media(max-width:15rem)]:';

const artifact = (id: string, type: Artifact['type']): Artifact => ({
  id,
  ticket_id: 'TEST-00238',
  node_id: 'n1',
  name: `${type} artifact`,
  type,
  content: '',
  created_at: '2026-01-01T00:00:00Z'
});

const makeTicket = (artifacts: Artifact[]): TicketDetail => ({
  id: 'TEST-00238',
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
  artifacts
});

const WITH_ARTIFACTS = [artifact('a1', 'gherkin'), artifact('a2', 'html'), artifact('a3', 'text')];

const renderTicket = (artifacts: Artifact[] = WITH_ARTIFACTS) =>
  render(
    <TicketItem
      ticket={makeTicket(artifacts)}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

const tabLabels = (artifactCount: number, gherkin: number, html: number) => [
  i18n.t('ticketItem.tabs.nodes', { count: 0 }),
  i18n.t('ticketItem.tabs.gherkin', { count: gherkin }),
  i18n.t('ticketItem.tabs.html', { count: html }),
  i18n.t('ticketItem.tabs.artifacts', { count: artifactCount })
];

const getTabs = (row: HTMLElement, labels: string[]) =>
  labels.map(name => within(row).getByRole('tab', { name }));

const getDownloadLink = (row: HTMLElement) =>
  within(row).getByRole('link', { name: i18n.t('ticketItem.downloadAllArtifacts') });

describe.each(['ja', 'en'] as const)('TicketItem artifact tab row on a narrow screen (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('holds the four tabs and the download link', () => {
    renderTicket();
    const row = screen.getByTestId('ticket-artifact-tabs');
    const tabs = getTabs(row, tabLabels(3, 1, 1));
    expect(tabs).toHaveLength(4);
    expect(getDownloadLink(row)).toHaveAttribute('href', '/api/tickets/TEST-00238/artifacts/download');
  });

  it('wraps the row below xl and keeps it on one line from xl up', () => {
    renderTicket();
    const row = screen.getByTestId('ticket-artifact-tabs');
    expect(row).toHaveClass('flex', 'flex-wrap', 'xl:flex-nowrap', 'justify-between', 'px-4', 'pb-2', 'xl:pb-0', `${NARROW_LARGE_TEXT}px-2`);
    // An unconditional nowrap or horizontal clip would bring the cut-off back,
    // and the lg: forms would bring back the 14px overflow at 1024px (DFLT-00240).
    expectNoneOf(row, [
      'flex-nowrap',
      'lg:flex-nowrap',
      'lg:pb-0',
      'overflow-hidden',
      'overflow-x-hidden',
      'lg:flex-wrap',
      'pb-0',
      'px-2'
    ]);
  });

  it('wraps the tab group below lg and lets it shrink only there', () => {
    renderTicket();
    const row = screen.getByTestId('ticket-artifact-tabs');
    const group = getTabs(row, tabLabels(3, 1, 1))[0].parentElement!;
    expect(group.parentElement).toBe(row);
    expect(group).toHaveClass('flex', 'flex-wrap', 'lg:flex-nowrap', 'max-lg:min-w-0');
    expectNoneOf(group, ['flex-nowrap', 'min-w-0']);
  });

  it('lets each tab shrink and break its label below lg only', () => {
    renderTicket();
    const row = screen.getByTestId('ticket-artifact-tabs');
    for (const tab of getTabs(row, tabLabels(3, 1, 1))) {
      expect(tab).toHaveClass(
        'max-lg:min-w-0',
        'px-3',
        'lg:px-4',
        `${NARROW_LARGE_TEXT}px-2`,
        `${NARROW_LARGE_TEXT}py-2`,
        `${NARROW_LARGE_TEXT}flex-wrap`
      );
      // Unconditional forms would change the single row from lg up.
      expectNoneOf(tab, ['whitespace-nowrap', 'shrink-0', 'min-w-0', 'px-4', 'flex-wrap', 'lg:min-w-0']);
      // The icon keeps its 16px at every width, lg and up included (DFLT-00240).
      const icon = tab.querySelector('svg')!;
      expect(icon).toHaveClass('w-4', 'h-4', 'shrink-0');
      expect(icon).not.toHaveClass('max-lg:shrink-0');
      const label = tab.querySelector('span')!;
      expect(label).toHaveClass('max-lg:min-w-0', 'break-words');
      expect(label).not.toHaveClass('whitespace-nowrap');
    }
  });

  it('lets the download link shrink and move to the right of its own line below lg', () => {
    renderTicket();
    const link = getDownloadLink(screen.getByTestId('ticket-artifact-tabs'));
    expect(link).toHaveClass('ml-auto', 'min-w-0', 'lg:shrink-0', `${NARROW_LARGE_TEXT}flex-wrap`, `${NARROW_LARGE_TEXT}px-2`);
    expectNoneOf(link, ['shrink-0', 'whitespace-nowrap', 'flex-wrap']);
    const label = link.querySelector('span')!;
    expect(label).toHaveTextContent(i18n.t('ticketItem.downloadAllArtifacts'));
    expect(label).toHaveClass('min-w-0', 'break-words');
  });

  it('keeps all four tabs and shows no download link without artifacts', () => {
    renderTicket([]);
    const row = screen.getByTestId('ticket-artifact-tabs');
    expect(getTabs(row, tabLabels(0, 0, 0))).toHaveLength(4);
    expect(within(row).queryByRole('link')).not.toBeInTheDocument();
  });
});

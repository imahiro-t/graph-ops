// DFLT-00239: the header row of an expanded ticket's description card wraps
// on a narrow line instead of squeezing its items. With a 200% default font
// on a 320-336px screen the title, the refined time and the "Show all"
// button no longer fit side by side: the refined time was crushed into a
// narrow column and, in English, the time and the button ran past the card
// (hidden by the panel's clip). Now the row and its right-hand group
// (refined time + expand button) are flex-wrap with the old gap-3 as their
// horizontal gap, so a row that fits on one line looks as before and one
// that does not moves the right-hand group, then the button, down a line.
// jsdom does no layout, so this checks the classes; the page's scrollWidth,
// elementFromPoint on each item and the row's height at 100% and 200%,
// 320-1280px, were measured in a real browser (see the ticket's
// implementation notes).
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatDateTime } from '../i18n/formatDate';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const REFINED_AT = '2026-01-02T03:04:05Z';

const makeTicket = (): TicketDetail => ({
  id: 'TEST-00239',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明の本文',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  refined_at: REFINED_AT,
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  artifacts: []
});

const renderExpanded = () =>
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

// The refined time, the expand button and the two containers above them.
function descriptionHeader() {
  const refined = screen.getByText(
    i18n.t('ticketItem.description.refinedAt', { time: formatDateTime(REFINED_AT, i18n.language) })
  );
  const expand = screen.getByRole('button', { name: i18n.t('ticketItem.description.expand') });
  const group = refined.parentElement as HTMLElement;
  const row = group.parentElement as HTMLElement;
  return { refined, expand, group, row };
}

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

describe.each(['ja', 'en'] as const)('TicketItem description header wraps on a narrow line (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('shows the title, the refined time and the expand button in one header row', () => {
    renderExpanded();
    const { refined, expand, group, row } = descriptionHeader();
    expect(refined).toBeVisible();
    expect(expand).toBeVisible();
    expect(group).toContainElement(expand);
    expect(row.firstElementChild).toHaveTextContent(i18n.t('ticketItem.description.title'));
  });

  it('wraps the row with the old horizontal gap and a vertical one', () => {
    renderExpanded();
    const { row } = descriptionHeader();
    expect(row).toHaveClass('flex', 'flex-wrap', 'items-center', 'justify-between', 'gap-x-3', 'gap-y-1', 'mb-2');
    expect(row).not.toHaveClass('flex-nowrap');
  });

  it('wraps the right-hand group and lets it and the refined time shrink', () => {
    renderExpanded();
    const { refined, group } = descriptionHeader();
    expect(group).toHaveClass('flex', 'flex-wrap', 'items-center', 'gap-x-3', 'gap-y-1', 'min-w-0');
    expect(group).not.toHaveClass('shrink-0');
    expect(refined).toHaveClass('min-w-0');
    expect(refined).not.toHaveClass('whitespace-nowrap');
  });
});

// DFLT-00276: at 160px wide with a 200% text size, the details panel's
// metadata bar let the creation date run to R171 (ja) / R185.5 (en), past the
// ticket card's R136 edge, where the card's overflow-x clip cut it off. The
// date's div is a flex item whose min-width: auto is the width of its longest
// word, and a single mono word ("2026/9/28", "9/28/2026,") was already wider
// than the card. The div now carries min-w-0 (so it can shrink) and
// wrap-anywhere (inherited by the date span, breaking inside a word only when
// the word cannot fit on a line), so nothing changes at any usable width.
// Measuring the rest of the bar at 160px / 200% found two more items past
// the card: the labels item (the "Edit labels" button's longest word, R139.5
// in English; a long label chip, whose max-w-40 is 320px at a 32px root) and
// the closed reason (one long word, up to R296.9). The labels item gets the
// same min-w-0 wrap-anywhere, and each chip min-w-0 so a long name truncates.
// The closed reason item gets min-w-0 and flex-wrap, with wrap-anywhere on the
// reason span only: on the whole item it was inherited by the "Closed reason:"
// text, which the one-line row then broke mid-word even at 320px / 200%
// ("Close / d / reaso / n:"). Its icon is shrink-0 so it is not squeezed.
// jsdom does no layout, so this checks the classes; the widths themselves
// were measured in a real browser (see the ticket's implementation notes).
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatDateTime } from '../i18n/formatDate';
import { Label, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const CREATED_AT = '2026-09-28T12:58:48Z';

const LABEL: Label = { id: 'l1', project_id: 'proj-1', name: 'accessibility-improvement-long-label', color: 'blue', created_at: '', updated_at: '' };

const makeTicket = (overrides: Partial<TicketDetail> = {}): TicketDetail => ({
  id: 'TEST-00276',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: CREATED_AT,
  updated_at: CREATED_AT,
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  artifacts: [],
  ...overrides
});

const renderTicket = (overrides: Partial<TicketDetail> = {}) =>
  render(
    <TicketItem
      ticket={makeTicket(overrides)}
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

describe.each(['ja', 'en'])('TicketItem metadata bar creation date (%s)', lng => {
  it('lets the creation date shrink and break inside a word when it cannot fit (DFLT-00276)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket();

    const formatted = formatDateTime(CREATED_AT, lng);
    const details = screen.getByTestId('ticket-details');
    const dateSpan = within(details).getByText(formatted);
    const item = dateSpan.parentElement as HTMLElement;

    // The item is the metadata bar's "Created: <date>" entry.
    expect(item.textContent).toBe(`${i18n.t('ticketItem.createdAt')}: ${formatted}`);
    expect(item).toHaveClass('min-w-0', 'wrap-anywhere');
    // The date keeps its look.
    expect(dateSpan).toHaveClass('font-mono');
  });

  it('lets the labels item and its chips shrink, and breaks only the closed reason itself inside a word (DFLT-00276)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket({ status: 'CLOSED', closed_reason: 'superseded-by-DFLT-00002', labels: [LABEL] });

    const details = screen.getByTestId('ticket-details');
    const labels = within(details).getByTestId('ticket-detail-labels');
    expect(labels).toHaveClass('flex', 'flex-wrap', 'min-w-0', 'wrap-anywhere');
    const chip = within(labels).getByTestId('label-chip');
    // min-w-0 on top of the chip's own max-w-40 / truncate.
    expect(chip).toHaveClass('min-w-0', 'max-w-40', 'whitespace-nowrap');

    const reason = within(details).getByText('superseded-by-DFLT-00002');
    const reasonItem = reason.parentElement as HTMLElement;
    expect(reasonItem.textContent).toBe(`${i18n.t('ticketItem.close.reasonLabel')}: superseded-by-DFLT-00002`);
    // The reason moves onto its own line when it does not fit beside the label.
    expect(reasonItem).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    // Not on the item: inherited, it broke the "Closed reason:" label mid-word.
    expect(reasonItem).not.toHaveClass('wrap-anywhere');
    // Only the reason itself breaks inside a word, and only when it cannot fit.
    expect(reason).toHaveClass('font-medium', 'min-w-0', 'wrap-anywhere');
    // The icon keeps its size instead of shrinking to a dot.
    const icon = reasonItem.querySelector('svg') as SVGElement;
    expect(icon).toHaveAttribute('aria-hidden', 'true');
    expect(icon).toHaveClass('shrink-0', 'w-3.5', 'h-3.5');
  });
});

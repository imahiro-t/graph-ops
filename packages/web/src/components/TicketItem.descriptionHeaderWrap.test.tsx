// DFLT-00239: the header row of an expanded ticket's description card wraps
// on a narrow line instead of squeezing its items. With a 200% default font
// on a 320-336px screen the title, the refined time and the "Full text"
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
//
// DFLT-00256: at 200% on 320-375px the FileText and History icons were
// squeezed (History to 0px) and the body's long words ran past the
// MarkdownViewer's overflow-x-auto box, which hid their end. The icons are
// now shrink-0 with the texts in spans of their own (the texts wrap, not the
// icons), the card's padding is p-3 below sm and with a large default font,
// and the body wrapper is wrap-break-word in both the collapsed and the
// expanded state. Measured in a real browser as above.
import { fireEvent, render, screen, within } from '@testing-library/react';
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

// The refined time's text and the span around it (with the History icon),
// the expand button, the two containers above them, the title's span and the
// card itself. The title text is looked up only inside the header row, so the
// same string elsewhere on the page is never picked up by mistake.
function descriptionHeader() {
  const refinedText = screen.getByText(
    i18n.t('ticketItem.description.refinedAt', { time: formatDateTime(REFINED_AT, i18n.language) })
  );
  const expand = screen.getByRole('button', { name: i18n.t('ticketItem.description.fullText') });
  const refined = refinedText.parentElement as HTMLElement;
  const group = refined.parentElement as HTMLElement;
  const row = group.parentElement as HTMLElement;
  const title = row.firstElementChild as HTMLElement;
  const titleText = within(title).getByText(i18n.t('ticketItem.description.title'));
  const card = row.parentElement as HTMLElement;
  return { refinedText, refined, expand, group, row, title, titleText, card };
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
    const { refinedText, expand, group, row } = descriptionHeader();
    expect(refinedText).toBeVisible();
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

  // At 200% on 320-336px the English title "Description" is one word wider
  // than the card, so without these it ran past the card and was clipped by
  // the panel ("Descripti"). The title may shrink and break inside the word.
  it('lets the title shrink and break inside a long word', () => {
    renderExpanded();
    const { title, titleText } = descriptionHeader();
    expect(title).toHaveClass('min-w-0', 'wrap-anywhere');
    expect(title).not.toHaveClass('shrink-0');
    expect(title).not.toHaveClass('whitespace-nowrap');
    expect(titleText.tagName).toBe('SPAN');
    expect(titleText).not.toBe(title);
    expect(titleText).toHaveClass('min-w-0', 'wrap-anywhere');
    expect(titleText).not.toHaveClass('whitespace-nowrap');
  });

  it('wraps the right-hand group and lets it and the refined time shrink', () => {
    renderExpanded();
    const { refinedText, refined, group } = descriptionHeader();
    expect(group).toHaveClass('flex', 'flex-wrap', 'items-center', 'gap-x-3', 'gap-y-1', 'min-w-0');
    expect(group).not.toHaveClass('shrink-0');
    expect(refined).toHaveClass('min-w-0');
    expect(refined).not.toHaveClass('whitespace-nowrap');
    expect(refinedText.tagName).toBe('SPAN');
    expect(refinedText).toHaveClass('min-w-0', 'wrap-anywhere');
    expect(refinedText).not.toHaveClass('whitespace-nowrap');
  });

  // DFLT-00256: at 200% on 320-375px the FileText icon was squeezed to
  // 8.5-18px (English) and the History icon to 0-2px.
  it('keeps the FileText and History icons from shrinking and hidden from assistive technology', () => {
    renderExpanded();
    const { title, refined } = descriptionHeader();
    const fileIcon = title.querySelector(':scope > svg');
    const historyIcon = refined.querySelector(':scope > svg');
    expect(fileIcon).not.toBeNull();
    expect(historyIcon).not.toBeNull();
    expect(fileIcon).toHaveClass('w-3.5', 'h-3.5', 'shrink-0');
    expect(historyIcon).toHaveClass('w-3', 'h-3', 'shrink-0');
    expect(fileIcon).toHaveAttribute('aria-hidden', 'true');
    expect(historyIcon).toHaveAttribute('aria-hidden', 'true');
  });

  // p-4 at sm and up with the default font; p-3 below sm and with a large
  // default font (the same pair as the detail panel and the artifact card).
  it('gives the card p-4, narrowed to p-3 on a narrow screen or with a large default font', () => {
    renderExpanded();
    const { card } = descriptionHeader();
    expect(card).toHaveClass('p-4', 'max-sm:p-3', 'upto-15rem:p-3', 'rounded-xl');
  });

  // The body's long words (paths, inline code, "autopilot" at 200% in a list
  // item) break inside the card in both states instead of running past the
  // MarkdownViewer's overflow-x-auto box.
  it('lets the body break long words both collapsed and expanded', () => {
    renderExpanded();
    const { card, expand } = descriptionHeader();
    const body = card.children[1] as HTMLElement;
    expect(body).toHaveTextContent('説明の本文');
    expect(body).toHaveClass('wrap-break-word', 'max-h-56', 'overflow-y-auto');
    expect(body).not.toHaveClass('wrap-anywhere');
    expect(body).not.toHaveClass('[overflow-wrap:anywhere]');
    fireEvent.click(expand);
    expect(screen.getByRole('button', { name: i18n.t('ticketItem.description.fullText') })).toBeVisible();
    expect(body).toHaveClass('wrap-break-word');
    expect(body).not.toHaveClass('max-h-56');
    expect(body).not.toHaveClass('overflow-y-auto');
  });
});

// DFLT-00143: in the ticket list's header row, the ticket ID and title can be
// selected as text (a click that is part of a selection doesn't toggle the
// row, a plain click still does), and a button next to the ID copies it to
// the clipboard.
//
// Clicks go through fireEvent rather than userEvent: user-event's click
// replays the pointer sequence and changes the document selection on
// mousedown (which would wipe the selection a test just set up), and
// userEvent.setup() installs its own navigator.clipboard stub.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import ja from '../i18n/locales/ja/translation.json';
import en from '../i18n/locales/en/translation.json';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const TICKET_ID = 'TEST-00001';

const makeTicket = (): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: 'コピーできるタイトル',
  description: '説明',
  status: 'TODO',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  nodes: [],
  edges: [],
  artifacts: []
});

const renderItem = () => {
  const onToggleExpand = vi.fn();
  const utils = render(
    <TicketItem ticket={makeTicket()} isExpanded={false} onToggleExpand={onToggleExpand} onRefresh={vi.fn()} myName="" />
  );
  return {
    ...utils,
    onToggleExpand,
    headerRow: screen.getByTestId('ticket-header-row'),
    idEl: screen.getByTestId('ticket-header-id'),
    titleEl: screen.getByTestId('ticket-header-title'),
    copyButton: screen.getByTestId('ticket-copy-id')
  };
};

// The copy button's live region: the header row's only role="status" while
// the row is collapsed (the expanded panel's one isn't rendered).
const copyStatus = (headerRow: HTMLElement) => {
  const regions = headerRow.querySelectorAll('[role="status"]');
  expect(regions).toHaveLength(1);
  return regions[0] as HTMLElement;
};

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');

const setClipboard = (value: unknown) => {
  Object.defineProperty(navigator, 'clipboard', { value, configurable: true, writable: true });
};

afterEach(() => {
  window.getSelection()?.removeAllRanges();
  if (originalClipboard) {
    Object.defineProperty(navigator, 'clipboard', originalClipboard);
  } else {
    delete (navigator as unknown as Record<string, unknown>).clipboard;
  }
  vi.useRealTimers();
});

describe('TicketItem header text selection', () => {
  it('makes only the ID and the title selectable', () => {
    const { headerRow, idEl, titleEl } = renderItem();
    expect(idEl).toHaveTextContent(TICKET_ID);
    expect(idEl).toHaveClass('select-text');
    expect(titleEl).toHaveClass('select-text');
    expect(headerRow).toHaveClass('select-none');
  });

  it('toggles the row on a plain click without a selection', () => {
    const { headerRow, titleEl, onToggleExpand } = renderItem();
    fireEvent.click(titleEl, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
    fireEvent.click(headerRow, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(2);
  });

  it('does not toggle when the title is selected', () => {
    const { titleEl, idEl, onToggleExpand } = renderItem();
    window.getSelection()!.selectAllChildren(titleEl);
    fireEvent.click(titleEl, { detail: 1 });
    window.getSelection()!.selectAllChildren(idEl);
    fireEvent.click(idEl, { detail: 1 });
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it('does not toggle for a selection that starts outside the row and ends in the title', () => {
    const { container, titleEl, onToggleExpand } = renderItem();
    const outside = document.createElement('p');
    outside.textContent = '行の外のテキスト';
    container.insertBefore(outside, container.firstChild);

    const range = document.createRange();
    range.setStart(outside.firstChild!, 2);
    range.setEnd(titleEl.firstChild!, 3);
    const sel = window.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
    expect(sel.isCollapsed).toBe(false);

    fireEvent.click(titleEl, { detail: 1 });
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it('still toggles when the only selection lies outside the row (before or after it)', () => {
    const { container, titleEl, onToggleExpand } = renderItem();
    const before = document.createElement('p');
    before.textContent = '行の前のテキスト';
    container.insertBefore(before, container.firstChild);
    const after = document.createElement('p');
    after.textContent = '行の後のテキスト';
    container.appendChild(after);

    window.getSelection()!.selectAllChildren(after);
    expect(window.getSelection()!.isCollapsed).toBe(false);
    fireEvent.click(titleEl, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);

    window.getSelection()!.selectAllChildren(before);
    fireEvent.click(titleEl, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(2);
  });

  it('does not toggle on the second click of a double click', () => {
    const { titleEl, onToggleExpand } = renderItem();
    fireEvent.click(titleEl, { detail: 2 });
    fireEvent.click(titleEl, { detail: 3 });
    expect(onToggleExpand).not.toHaveBeenCalled();
  });
});

describe('TicketItem ID copy button', () => {
  let writeText: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    writeText = vi.fn(async () => undefined);
  });

  it('has an accessible name that includes the ID', () => {
    setClipboard({ writeText });
    const { copyButton, headerRow } = renderItem();
    const label = i18n.t('ticketItem.copyId.button', { id: TICKET_ID });
    expect(label).toContain(TICKET_ID);
    expect(copyButton).toHaveAttribute('type', 'button');
    expect(copyButton).toHaveAttribute('aria-label', label);
    expect(copyButton).toHaveAttribute('title', label);
    expect(screen.getByRole('button', { name: label })).toBe(copyButton);
    expect(copyStatus(headerRow)).toBeEmptyDOMElement();
  });

  it('copies the ID, does not toggle the row, and shows the copied state for a while', async () => {
    vi.useFakeTimers();
    setClipboard({ writeText });
    const { copyButton, headerRow, onToggleExpand } = renderItem();

    // Settle the writeText promise inside act before touching the timers.
    await act(async () => {
      fireEvent.click(copyButton);
    });

    expect(writeText).toHaveBeenCalledWith(TICKET_ID);
    expect(onToggleExpand).not.toHaveBeenCalled();
    const copied = i18n.t('ticketItem.copyId.copied', { id: TICKET_ID });
    expect(copyButton).toHaveAttribute('aria-label', copied);
    expect(copyButton).toHaveAttribute('title', copied);
    expect(copyStatus(headerRow)).toHaveTextContent(copied);

    act(() => {
      vi.advanceTimersByTime(1500);
    });
    expect(copyButton).toHaveAttribute('aria-label', i18n.t('ticketItem.copyId.button', { id: TICKET_ID }));
    expect(copyStatus(headerRow)).toBeEmptyDOMElement();
  });

  it('shows the failed state when writeText rejects', async () => {
    writeText.mockRejectedValue(new Error('denied'));
    setClipboard({ writeText });
    const { copyButton, headerRow, onToggleExpand } = renderItem();

    await act(async () => {
      fireEvent.click(copyButton);
    });

    expect(writeText).toHaveBeenCalledWith(TICKET_ID);
    expect(onToggleExpand).not.toHaveBeenCalled();
    const failed = i18n.t('ticketItem.copyId.failed', { id: TICKET_ID });
    expect(screen.getByTestId('ticket-copy-id')).toHaveAttribute('aria-label', failed);
    expect(copyStatus(headerRow)).toHaveTextContent(failed);
  });

  it('shows the failed state when the Clipboard API is missing', async () => {
    setClipboard(undefined);
    const { copyButton, headerRow, onToggleExpand } = renderItem();

    await act(async () => {
      fireEvent.click(copyButton);
    });

    expect(onToggleExpand).not.toHaveBeenCalled();
    const failed = i18n.t('ticketItem.copyId.failed', { id: TICKET_ID });
    expect(screen.getByTestId('ticket-copy-id')).toHaveAttribute('aria-label', failed);
    expect(copyStatus(headerRow)).toHaveTextContent(failed);
  });
});

describe('ID copy translations', () => {
  it('define non-empty copyId texts with {{id}} in ja and en', () => {
    for (const locale of [ja, en]) {
      const copyId = (locale as { ticketItem: { copyId: Record<string, string> } }).ticketItem.copyId;
      expect(Object.keys(copyId).sort()).toEqual(['button', 'copied', 'failed']);
      for (const text of Object.values(copyId)) {
        expect(text.trim()).not.toBe('');
        expect(text).toContain('{{id}}');
      }
    }
  });
});

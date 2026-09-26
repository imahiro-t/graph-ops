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

  it('does not toggle when the title is selected (a click with no mousedown seen on the row)', () => {
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

  it('does not toggle when a press/release on the row makes the selection (mousedown, then select, then click)', () => {
    const { titleEl, idEl, onToggleExpand } = renderItem();
    fireEvent.mouseDown(titleEl, { detail: 1 });
    window.getSelection()!.selectAllChildren(titleEl);
    fireEvent.click(titleEl, { detail: 1 });
    expect(onToggleExpand).not.toHaveBeenCalled();

    // Re-selecting something else (the ID) over an existing selection also
    // counts as a selection change.
    fireEvent.mouseDown(idEl, { detail: 1 });
    window.getSelection()!.selectAllChildren(idEl);
    fireEvent.click(idEl, { detail: 1 });
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it('toggles on a click outside the selectable text while an earlier selection is still there', () => {
    // Chromium keeps the selection on a mousedown over the row's select-none
    // parts, so after copying the title the row must still open and close.
    const { headerRow, titleEl, onToggleExpand } = renderItem();
    window.getSelection()!.selectAllChildren(titleEl);
    const chevron = screen.getByTestId('ticket-toggle-expand');
    const status = headerRow.querySelector('.rounded-full')!;

    fireEvent.mouseDown(chevron, { detail: 1 });
    fireEvent.click(chevron, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);

    fireEvent.mouseDown(status, { detail: 1 });
    fireEvent.click(status, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(2);

    fireEvent.mouseDown(headerRow, { detail: 1 });
    fireEvent.click(headerRow, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(3);

    // The selection is still there throughout (jsdom, like Chromium, keeps it).
    expect(window.getSelection()!.isCollapsed).toBe(false);
  });

  it('toggles on a plain click inside an existing selection that the click does not change', () => {
    // Chromium clears a selection only after the click when the press lands
    // inside it, so the click still sees the unchanged selection.
    const { titleEl, onToggleExpand } = renderItem();
    window.getSelection()!.selectAllChildren(titleEl);
    fireEvent.mouseDown(titleEl, { detail: 1 });
    fireEvent.click(titleEl, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
  });

  it('always toggles on a keyboard click of the chevron (detail 0), even with a selection in the row', () => {
    const { titleEl, onToggleExpand } = renderItem();
    window.getSelection()!.selectAllChildren(titleEl);
    const chevron = screen.getByTestId('ticket-toggle-expand');
    fireEvent.click(chevron, { detail: 0 });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
    fireEvent.click(chevron, { detail: 0 });
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
    // DFLT-00171: the name is not repeated in a title; the tooltip is
    // IconButton's own.
    expect(copyButton).not.toHaveAttribute('title');
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
    expect(copyButton).not.toHaveAttribute('title');
    expect(copyStatus(headerRow)).toHaveTextContent(copied);

    act(() => {
      vi.advanceTimersByTime(1500);
    });
    expect(copyButton).toHaveAttribute('aria-label', i18n.t('ticketItem.copyId.button', { id: TICKET_ID }));
    expect(copyStatus(headerRow)).toBeEmptyDOMElement();
  });

  it('meets the contrast and target size fixes (slate-500 icon, 24x24px button)', () => {
    setClipboard({ writeText });
    const { copyButton } = renderItem();
    expect(copyButton).toHaveClass('w-6', 'h-6', 'text-slate-500');
    expect(copyButton).not.toHaveClass('text-slate-400');
  });

  it('leaves no timer behind when the row unmounts while writeText is pending', async () => {
    vi.useFakeTimers();
    let resolveWrite: () => void = () => {};
    writeText.mockImplementation(() => new Promise<void>(resolve => { resolveWrite = resolve; }));
    setClipboard({ writeText });
    const { copyButton, unmount } = renderItem();

    fireEvent.click(copyButton);
    expect(writeText).toHaveBeenCalledWith(TICKET_ID);
    unmount();
    await act(async () => {
      resolveWrite();
    });
    expect(vi.getTimerCount()).toBe(0);
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

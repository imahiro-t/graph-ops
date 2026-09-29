// DFLT-00310: copying the metadata bar's labels ("作成日時:", "Created:") and
// the "Edit labels" name, drawn with U+200B / U+2060 as one text node each
// (DFLT-00295), puts them on the clipboard without those characters. The
// copy listener (lib/plainCopy) is installed on the document as App does.
// The expected text is derived from the *Visible keys (plainText), and also
// checked against literals so a broken key cannot pass. jsdom's
// Selection.toString does no layout, so line breaks are not checked here.
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatDateTime } from '../i18n/formatDate';
import { installPlainCopy, PLAIN_COPY_ATTR } from '../lib/plainCopy';
import { plainText, WBR_MARK, WORD_JOINER, ZWSP } from '../lib/wbr';
import { dispatchCopy, select, selectContents } from '../test/copyEvent';
import { Label, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const CREATED_AT = '2026-09-28T12:58:48Z';
const DESCRIPTION = 'コピーしたい説明文';
const LABEL: Label = { id: 'l1', project_id: 'proj-1', name: 'bug', color: 'red', created_at: '', updated_at: '' };

const makeTicket = (overrides: Partial<TicketDetail> = {}): TicketDetail => ({
  id: 'TEST-00310',
  project_id: 'proj-1',
  title: 'タイトル',
  description: DESCRIPTION,
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: CREATED_AT,
  updated_at: CREATED_AT,
  priority: 'MEDIUM',
  labels: [LABEL],
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
      projectLabels={[LABEL]}
    />
  );

const LITERALS: Record<string, Record<string, string>> = {
  'ticketItem.createdAtVisible': { ja: '作成日時:', en: 'Created:' },
  'ticket.labels.title': { ja: 'ラベル:', en: 'Labels:' },
  'ticketItem.close.reasonLabelVisible': { ja: 'クローズ理由:', en: 'Closed reason:' }
};

// The label as a person reads it, from its key, checked against the literal.
const expectedLabel = (key: string, lng: string) => {
  const text = `${plainText(i18n.t(key))}:`;
  expect(text).toBe(LITERALS[key][lng]);
  return text;
};

// The MetaLabel element drawing `text` (as read, with its colon).
const metaLabel = (text: string) => {
  const details = screen.getByTestId('ticket-details');
  const found = Array.from(details.querySelectorAll<HTMLElement>(`[${PLAIN_COPY_ATTR}]`)).filter(
    el => plainText(el.textContent ?? '') === text
  );
  expect(found).toHaveLength(1);
  // Drawn with the invisible characters, which the copy must not take along.
  expect(found[0].textContent).toMatch(new RegExp(`[${ZWSP}${WORD_JOINER}]`));
  return found[0];
};

const expectNoInvisible = (s: string | undefined) => {
  expect(s).toBeDefined();
  expect(s).not.toContain(ZWSP);
  expect(s).not.toContain(WORD_JOINER);
};

const descriptionText = () => {
  const el = within(screen.getByTestId('ticket-details')).getByText(DESCRIPTION, { exact: false });
  return el.firstChild as Text;
};

let uninstall: () => void = () => {};

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  uninstall = installPlainCopy(document);
});

afterEach(async () => {
  uninstall();
  document.getSelection()?.removeAllRanges();
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'])('copying the metadata bar labels (%s, DFLT-00310)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it.each(['ticketItem.createdAtVisible', 'ticket.labels.title'])('copies the %s label alone without U+200B / U+2060', key => {
    renderTicket();
    const text = expectedLabel(key, lng);
    const label = metaLabel(text);
    selectContents(label);
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toBe(text);
    expectNoInvisible(written['text/plain']);
    expectNoInvisible(written['text/html']);
  });

  it('copies the closed reason label of a closed ticket without U+200B / U+2060', () => {
    renderTicket({ status: 'CLOSED', closed_reason: 'superseded-by-DFLT-00002' });
    const text = expectedLabel('ticketItem.close.reasonLabelVisible', lng);
    const label = metaLabel(text);
    selectContents(label);
    const { written } = dispatchCopy(label);
    expect(written['text/plain']).toBe(text);
    expectNoInvisible(written['text/plain']);
    expectNoInvisible(written['text/html']);
  });

  it('copies the "Edit labels" name without U+200B', () => {
    renderTicket();
    const name = i18n.t('ticket.labels.edit');
    expect(name).toBe(lng === 'ja' ? 'ラベルを編集' : 'Edit labels');
    expect(plainText(i18n.t('ticket.labels.editVisible'))).toBe(name);
    const button = within(screen.getByTestId('ticket-detail-labels')).getByRole('button', { name: `${name}: TEST-00310` });
    const text = button.querySelector(`[${PLAIN_COPY_ATTR}]`) as HTMLElement;
    expect(text).not.toBeNull();
    selectContents(text);
    const { event, written } = dispatchCopy(text);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toBe(name);
    expectNoInvisible(written['text/plain']);
    expectNoInvisible(written['text/html']);
  });

  it('copies a label with its value, text/html included, without the characters', () => {
    renderTicket();
    const text = expectedLabel('ticketItem.createdAtVisible', lng);
    const label = metaLabel(text);
    const formatted = formatDateTime(CREATED_AT, lng);
    const date = within(screen.getByTestId('ticket-details')).getByText(formatted);
    select(label.firstChild as Text, 0, date.firstChild as Text);
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toContain(text);
    expect(written['text/plain']).toContain(formatted);
    expectNoInvisible(written['text/plain']);
    expect(written['text/html']).toContain(text);
    expectNoInvisible(written['text/html']);
  });

  // The title, in the header row, comes before the metadata bar (the
  // description comes after it).
  it('copies a selection from the title into a label without the characters', () => {
    renderTicket();
    const text = expectedLabel('ticketItem.createdAtVisible', lng);
    const label = metaLabel(text);
    const title = screen.getByTestId('ticket-header-title').firstChild as Text;
    expect(title.nodeValue).toBe('タイトル');
    select(title, 0, label.firstChild as Text);
    const { event, written } = dispatchCopy(title);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toContain('タイトル');
    expect(written['text/plain']).toContain(text);
    expectNoInvisible(written['text/plain']);
    expectNoInvisible(written['text/html']);
  });

  it('leaves a copy of the description alone to the browser', () => {
    renderTicket();
    select(descriptionText(), 0);
    const { event, written } = dispatchCopy(descriptionText());
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });
});

describe('copying with a "<wbr/>" string in the description (DFLT-00310)', () => {
  const WITH_MARK = `説明 ${WBR_MARK} を含む`;

  // The paragraph holding the string, which the Markdown renderer may split
  // into more than one text node.
  const markParagraph = () => {
    const el = within(screen.getByTestId('ticket-details')).getByText(
      (_, node) => node?.tagName === 'P' && (node.textContent ?? '').includes(WBR_MARK)
    );
    expect(el.textContent).toContain(WBR_MARK);
    return el;
  };

  it('leaves a copy of that description alone', () => {
    renderTicket({ description: WITH_MARK });
    const paragraph = markParagraph();
    selectContents(paragraph);
    expect(document.getSelection()?.toString()).toContain(WBR_MARK);
    const { event, written } = dispatchCopy(paragraph);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });

  // The description comes after the metadata bar, so the range runs from the
  // label to the description (a selection dragged from the description up to
  // the label is this same range).
  it('keeps the string when the copy reaches a label', () => {
    renderTicket({ description: WITH_MARK });
    const label = metaLabel('作成日時:');
    const paragraph = markParagraph();
    select(label.firstChild as Text, 0, paragraph);
    const { written } = dispatchCopy(paragraph);
    expect(written['text/plain']).toContain(WBR_MARK);
    expect(written['text/plain']).toContain('作成日時:');
    expectNoInvisible(written['text/plain']);
    expectNoInvisible(written['text/html']);
  });
});

describe('the ticket ID copy button with the copy listener installed (DFLT-00310)', () => {
  const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');

  afterEach(() => {
    if (originalClipboard) {
      Object.defineProperty(navigator, 'clipboard', originalClipboard);
    } else {
      delete (navigator as unknown as Record<string, unknown>).clipboard;
    }
  });

  it('still copies the ticket ID', async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true, writable: true });
    renderTicket();
    await act(async () => {
      fireEvent.click(screen.getByTestId('ticket-copy-id'));
    });
    expect(writeText).toHaveBeenCalledWith('TEST-00310');
  });
});

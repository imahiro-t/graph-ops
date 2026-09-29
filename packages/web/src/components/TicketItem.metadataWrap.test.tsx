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
// DFLT-00292: with wrap-anywhere on the whole date item and the whole labels
// item, the labels inherited it too and broke mid-word at 160px / 200%
// ("Create / d:", "Edit / labe / ls"), and the closed reason's label could
// break before its colon ("クローズ理由 / :"). wrap-anywhere now sits on the
// values only (the date, the closed reason, each chip, and the label save
// error). Each label is a MetaLabel -- break-keep, with its last character
// and colon in a whitespace-nowrap span, and a <wbr> between the words of a
// Japanese label ("クローズ<wbr>理由") -- and never breaks inside a word: at
// 160px / 200% a word wider than the bar ("Created:", "クローズ") still ends
// inside the card's clip when left whole, so it gets no overflow-wrap (QA
// review R1 of DFLT-00292). The "Edit labels"
// button is flex-wrap wrap-break-word with its name in a break-keep span
// ("ラベルを<wbr>編集" in Japanese). LabelSelect's wrapper is flex-wrap so a
// save error goes to its own line instead of squeezing the button.
// The plain keys (ticketItem.createdAt, ticketItem.close.reasonLabel,
// ticket.labels.edit) are the baseline the drawn *Visible text is compared
// with here, so keep them even where the UI draws the *Visible key instead.
// jsdom does no layout, so this checks the classes; the widths themselves
// were measured in a real browser (see the ticket's implementation notes).
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

const renderTicket = (overrides: Partial<TicketDetail> = {}, projectLabels: Label[] = []) =>
  render(
    <TicketItem
      ticket={makeTicket(overrides)}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={projectLabels}
    />
  );

const LONG_WORD = 'label-0123456789abcdef-not-found';

// Checks a MetaLabel: the label and its colon in one span that breaks between
// words only, with the last character and the colon joined by nowrap. `words`
// are the parts a <wbr> separates (the Japanese "クローズ / 理由"); the text
// must still read as the plain translation (`text`).
const expectMetaLabel = (label: HTMLElement, text: string, words: string[] = [text]) => {
  expect(label.textContent).toBe(`${text}:`);
  const wbrs = label.querySelectorAll('wbr');
  expect(wbrs).toHaveLength(words.length - 1);
  wbrs.forEach((wbr, i) => {
    expect(wbr.parentElement).toBe(label);
    expect(wbr.previousSibling?.textContent).toBe(words[i]);
  });
  expect(label.textContent).not.toContain('<wbr');
  expect(label).toHaveClass('break-keep');
  // Never broken inside a word, and keeps its longest word's width as a flex item.
  for (const cls of ['wrap-anywhere', 'wrap-break-word', 'min-w-0']) expect(label).not.toHaveClass(cls);
  const tails = label.querySelectorAll('.whitespace-nowrap');
  expect(tails).toHaveLength(1);
  expect(tails[0].textContent).toBe(`${Array.from(text).pop()}:`);
  expect(tails[0].parentElement).toBe(label);
};

// No element from el up to the metadata bar (or beyond) has wrap-anywhere.
const expectNoInheritedWrapAnywhere = (el: Element) => {
  const bar = screen.getByTestId('ticket-detail-labels').parentElement as HTMLElement;
  for (let node: Element | null = el; node; node = node.parentElement) {
    expect(node).not.toHaveClass('wrap-anywhere');
    if (node === bar) break;
  }
  // Nor anywhere above it.
  expect(el.closest('.wrap-anywhere')).toBeNull();
};

const labelSpanOf = (item: HTMLElement) => item.querySelector(':scope > .break-keep') as HTMLElement;

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'])('TicketItem metadata bar wrapping (%s)', lng => {
  it('breaks only the date itself inside a word and keeps the label whole (DFLT-00276, DFLT-00292)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket();

    const formatted = formatDateTime(CREATED_AT, lng);
    const details = screen.getByTestId('ticket-details');
    const dateSpan = within(details).getByText(formatted);
    const item = dateSpan.parentElement as HTMLElement;

    // The item is the metadata bar's "Created: <date>" entry.
    expect(item.textContent).toBe(`${i18n.t('ticketItem.createdAt')}: ${formatted}`);
    // It can shrink, but no longer passes wrap-anywhere down to its label.
    expect(item).toHaveClass('min-w-0');
    expect(item).not.toHaveClass('wrap-anywhere');
    // Only the date breaks inside a word, and it keeps its look.
    expect(dateSpan).toHaveClass('font-mono', 'wrap-anywhere');
    // The label breaks between words only, never before its colon.
    const label = labelSpanOf(item);
    // Japanese may break only between "作成" and "日時" (DFLT-00292).
    expectMetaLabel(label, i18n.t('ticketItem.createdAt'), lng === 'ja' ? ['作成', '日時'] : undefined);
    expectNoInheritedWrapAnywhere(label);
  });

  it('breaks only the values inside a word in the labels item and the closed reason (DFLT-00276, DFLT-00292)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket({ status: 'CLOSED', closed_reason: 'superseded-by-DFLT-00002', labels: [LABEL] });

    const details = screen.getByTestId('ticket-details');
    const labels = within(details).getByTestId('ticket-detail-labels');
    expect(labels).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    // Inherited, it broke the "Edit labels" button mid-word.
    expect(labels).not.toHaveClass('wrap-anywhere');
    expectMetaLabel(labelSpanOf(labels), i18n.t('ticket.labels.title'));
    const chip = within(labels).getByTestId('label-chip');
    // min-w-0 on top of the chip's own max-w-40 / truncate; the chip is a value.
    expect(chip).toHaveClass('min-w-0', 'wrap-anywhere', 'max-w-40', 'whitespace-nowrap');

    const reason = within(details).getByText('superseded-by-DFLT-00002');
    const reasonItem = reason.parentElement as HTMLElement;
    expect(reasonItem.textContent).toBe(`${i18n.t('ticketItem.close.reasonLabel')}: superseded-by-DFLT-00002`);
    // The reason moves onto its own line when it does not fit beside the label.
    expect(reasonItem).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    // Not on the item: inherited, it broke the "Closed reason:" label mid-word.
    expect(reasonItem).not.toHaveClass('wrap-anywhere');
    // The label is a flex item of its own that breaks between words only.
    const reasonLabel = labelSpanOf(reasonItem);
    expect(reasonLabel.parentElement).toBe(reasonItem);
    // Japanese may break only between "クローズ" and "理由", never "クローズ理 / 由:".
    expectMetaLabel(reasonLabel, i18n.t('ticketItem.close.reasonLabel'), lng === 'ja' ? ['クローズ', '理由'] : undefined);
    expectNoInheritedWrapAnywhere(reasonLabel);
    // Only the reason itself breaks inside a word, and only when it cannot fit.
    expect(reason).toHaveClass('font-medium', 'min-w-0', 'wrap-anywhere');
    // The icon keeps its size instead of shrinking to a dot.
    const icon = reasonItem.querySelector('svg') as SVGElement;
    expect(icon).toHaveAttribute('aria-hidden', 'true');
    expect(icon).toHaveClass('shrink-0', 'w-3.5', 'h-3.5');
  });

  it('keeps wrap-anywhere off the "Edit labels" button and wraps its name between words only (DFLT-00292)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket({ status: 'CLOSED', closed_reason: 'superseded-by-DFLT-00002', labels: [LABEL] });

    const labels = screen.getByTestId('ticket-detail-labels');
    const editName = i18n.t('ticket.labels.edit');
    const button = within(labels).getByRole('button', { name: `${editName}: TEST-00276` });
    expectNoInheritedWrapAnywhere(button);
    // flex-wrap: the name moves below the icon instead of being squeezed beside it.
    expect(button).toHaveClass('flex', 'flex-wrap', 'min-w-0', 'max-w-full', 'wrap-break-word', 'rounded-full', 'upto-15rem:rounded-xl');
    // The Tag icon keeps its size.
    expect(button.querySelector('svg')).toHaveClass('shrink-0');

    const text = button.querySelector(':scope > span') as HTMLElement;
    expect(text).toHaveClass('min-w-0', 'break-keep');
    // The visible name (ticket.labels.editVisible) matches ticket.labels.edit.
    expect(text.textContent).toBe(editName);
    expect(text.textContent).not.toContain('wbr');
    const wbrs = text.querySelectorAll('wbr');
    if (lng === 'ja') {
      // "ラベルを / 編集" is the only break opportunity under keep-all.
      expect(wbrs).toHaveLength(1);
      expect(wbrs[0].previousSibling?.textContent).toBe('ラベルを');
      expect(wbrs[0].nextSibling?.textContent).toBe('編集');
    } else {
      expect(wbrs).toHaveLength(0);
    }

    // LabelSelect's wrapper can wrap the error below the button and stays in the card.
    const wrapper = button.parentElement as HTMLElement;
    expect(wrapper).toHaveClass('flex-wrap', 'min-w-0', 'max-w-full');
  });

  it('lets a label save error break inside a word on a line of its own (DFLT-00292)', async () => {
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    vi.stubGlobal(
      'fetch',
      vi.fn((_input: RequestInfo | URL, init?: RequestInit) =>
        init?.method === 'PATCH'
          ? Promise.reject(new Error(LONG_WORD))
          : Promise.resolve(new Response('[]', { status: 200 }))
      )
    );
    const other: Label = { ...LABEL, id: 'l2', name: 'bug' };
    renderTicket({ status: 'CLOSED', closed_reason: 'superseded-by-DFLT-00002', labels: [LABEL] }, [LABEL, other]);

    const labels = screen.getByTestId('ticket-detail-labels');
    const button = within(labels).getByRole('button', { name: `${i18n.t('ticket.labels.edit')}: TEST-00276` });
    await user.click(button);
    await user.click(within(labels).getByRole('checkbox', { name: 'bug' }));

    const alert = await within(labels).findByRole('alert');
    await waitFor(() => expect(alert).toHaveTextContent(i18n.t('ticket.labels.saveError', { message: LONG_WORD })));
    // The error is a value: it may break inside a word, within the card.
    expect(alert).toHaveClass('min-w-0', 'max-w-full', 'wrap-anywhere');
    // Its parent is the wrapper, which sends it to the next line when needed.
    expect(alert.parentElement).toBe(button.parentElement);
    expect(alert.parentElement).toHaveClass('flex-wrap');
    // The button still inherits no wrap-anywhere.
    expectNoInheritedWrapAnywhere(button);
  });
});

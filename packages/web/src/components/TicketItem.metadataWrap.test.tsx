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
// error). Each label was a MetaLabel -- break-keep, with its last character
// and colon in a whitespace-nowrap span, and a <wbr> between the words of a
// Japanese label ("クローズ<wbr>理由") -- and never broke inside a word: at
// 160px / 200% a word wider than the bar ("Created:", "クローズ") still ended
// inside the card's clip when left whole, so it got no overflow-wrap (QA
// review R1 of DFLT-00292). The "Edit labels" button is flex-wrap
// wrap-break-word with its name in a break-keep span ("ラベルを<wbr>編集" in
// Japanese). LabelSelect's wrapper was flex-wrap so a save error went to its
// own line instead of squeezing the button.
// DFLT-00295 replaces the parts of that paragraph written in the past tense:
// there is no nowrap span and no <wbr> element any more, a label breaks
// inside a word when one word cannot fit (see below), and LabelSelect's
// wrapper is display: contents.
// Each label is now one text node -- its words joined by U+200B (the
// "<wbr/>" mark, see lib/wbr) and its last character tied to the colon by
// U+2060 -- so Chromium's accessibility tree shows it as one text, not
// "作成" "日" "時:". It is inline-block with a max-width of its item plus the
// details panel's padding (--details-pad), and breaks inside a word only when
// one word is wider than that (wrap-break-word), so it always ends inside the
// card's clip. "ノード数: " is one text node as well. The label picker's panel
// is positioned from an anchor around the button only, so a save error does
// not move it (LabelSelect.test.tsx covers fitting it into the card), and
// the labels item grows into the rest of its line while there is a save
// error, so the message is not squeezed to the width of the labels.
// The expected label text is derived from the *Visible keys (plainText), and
// also checked against literals so a broken *Visible key cannot pass.
// jsdom does no layout, so this checks the classes; the widths themselves
// were measured in a real browser (see the ticket's implementation notes).
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatDateTime } from '../i18n/formatDate';
import { plainText, WORD_JOINER, ZWSP } from '../lib/wbr';
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

// Checks a MetaLabel: the label and its colon as one text node, its `words`
// joined by U+200B (the Japanese "クローズ / 理由") and the last character tied
// to the colon by U+2060; without them it reads as `text`.
const expectMetaLabel = (label: HTMLElement, text: string, words: string[] = [text]) => {
  expect(words.join('')).toBe(text);
  expect(label.childNodes).toHaveLength(1);
  expect(label.firstChild?.nodeType).toBe(Node.TEXT_NODE);
  expect(label.children).toHaveLength(0);
  expect(label.querySelector('wbr')).toBeNull();
  expect(label.textContent).toBe(`${words.join(ZWSP)}${WORD_JOINER}:`);
  expect(plainText(label.textContent ?? '')).toBe(`${text}:`);
  expect(label.textContent).not.toContain('<wbr');
  // Breaks between words, and inside a word only when the word is wider than
  // its item plus the details panel's padding.
  expect(label).toHaveClass('inline-block', 'break-keep', 'wrap-break-word', 'max-w-[calc(100%+var(--details-pad))]');
  for (const cls of ['wrap-anywhere', 'min-w-0']) expect(label).not.toHaveClass(cls);
};

// The label text a test expects, from its *Visible key, which must also read
// as the literal (so the baseline cannot drift along with a broken key).
const visibleLabel = (key: string, literal: Record<string, string>, lng: string) => {
  const text = plainText(i18n.t(key));
  expect(text).toBe(literal[lng]);
  return text;
};
const CREATED = { ja: '作成日時', en: 'Created' };
const CLOSED_REASON = { ja: 'クローズ理由', en: 'Closed reason' };

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
    const created = visibleLabel('ticketItem.createdAtVisible', CREATED, lng);
    expect(plainText(item.textContent ?? '')).toBe(`${created}: ${formatted}`);
    // It can shrink, but no longer passes wrap-anywhere down to its label.
    expect(item).toHaveClass('min-w-0');
    expect(item).not.toHaveClass('wrap-anywhere');
    // Only the date breaks inside a word, and it keeps its look.
    expect(dateSpan).toHaveClass('font-mono', 'wrap-anywhere');
    // The label breaks between words only, never before its colon.
    const label = labelSpanOf(item);
    // Japanese may break only between "作成" and "日時" (DFLT-00292).
    expectMetaLabel(label, created, lng === 'ja' ? ['作成', '日時'] : undefined);
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
    const closedReason = visibleLabel('ticketItem.close.reasonLabelVisible', CLOSED_REASON, lng);
    expect(plainText(reasonItem.textContent ?? '')).toBe(`${closedReason}: superseded-by-DFLT-00002`);
    // The reason moves onto its own line when it does not fit beside the label.
    expect(reasonItem).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    // Not on the item: inherited, it broke the "Closed reason:" label mid-word.
    expect(reasonItem).not.toHaveClass('wrap-anywhere');
    // The label is a flex item of its own that breaks between words only.
    const reasonLabel = labelSpanOf(reasonItem);
    expect(reasonLabel.parentElement).toBe(reasonItem);
    // Japanese may break only between "クローズ" and "理由", never "クローズ理 / 由:".
    expectMetaLabel(reasonLabel, closedReason, lng === 'ja' ? ['クローズ', '理由'] : undefined);
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
    // The visible name (ticket.labels.editVisible) is one string that reads
    // as ticket.labels.edit, with U+200B where it may break (DFLT-00295).
    expect(text.querySelector('wbr')).toBeNull();
    expect(text.childNodes).toHaveLength(1);
    expect(plainText(text.textContent ?? '')).toBe(editName);
    expect(text.textContent).not.toContain('wbr');
    const parts = (text.textContent ?? '').split(ZWSP);
    if (lng === 'ja') {
      // "ラベルを / 編集" is the only break opportunity under keep-all.
      expect(parts).toEqual(['ラベルを', '編集']);
    } else {
      expect(parts).toEqual([editName]);
    }

    // The panel's anchor wraps the button only (DFLT-00295).
    const anchor = button.parentElement as HTMLElement;
    expect(anchor).toHaveClass('relative', 'min-w-0', 'max-w-full');
    // LabelSelect's wrapper is display: contents, so the anchor (and a save
    // error) are items of the labels item itself, which wraps them.
    const wrapper = anchor.parentElement as HTMLElement;
    expect(wrapper).toHaveClass('contents');
    expect(wrapper).not.toHaveClass('relative');
    expect(wrapper.parentElement).toBe(labels);
    expect(labels).toHaveClass('flex', 'flex-wrap');
  });

  it('draws "Nodes: " as one text node before its value (DFLT-00295)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket();
    const bar = screen.getByTestId('ticket-detail-labels').parentElement as HTMLElement;
    const item = bar.firstElementChild as HTMLElement;
    expect(item.childNodes).toHaveLength(2);
    expect(item.childNodes[0].nodeType).toBe(Node.TEXT_NODE);
    expect(item.childNodes[0].textContent).toBe(`${i18n.t('ticketItem.nodeCount')}: `);
    expect(item.childNodes[0].textContent).toBe(lng === 'ja' ? 'ノード数: ' : 'Nodes: ');
    expect((item.childNodes[1] as HTMLElement).tagName).toBe('SPAN');
    expect(item.childNodes[1].textContent).toBe('0');
    // A word wider than the bar breaks inside itself instead of running past the card.
    expect(item).toHaveClass('min-w-0', 'wrap-break-word');
    expect(item).not.toHaveClass('wrap-anywhere');
  });

  it('pads the details panel with the variable its labels may run into (DFLT-00295)', async () => {
    await i18n.changeLanguage(lng);
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    expect(details).toHaveClass(
      'p-(--details-pad)',
      '[--details-pad:1.5rem]',
      'max-sm:[--details-pad:0.75rem]',
      'upto-15rem:[--details-pad:0.75rem]',
      '[@media(max-width:200px)]:[--details-pad:0.5rem]',
      'space-y-6'
    );
    for (const cls of ['p-6', 'max-sm:p-3', 'upto-15rem:p-3', '[@media(max-width:200px)]:p-2']) expect(details).not.toHaveClass(cls);
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
    // The error is a value: it may break inside a word, within the card. On
    // a line of its own, it adds nothing to the labels item's width, so the
    // item does not wrap onto another line of the bar (DFLT-00295).
    expect(alert).toHaveClass('w-0', 'min-w-full', 'max-w-full', 'wrap-anywhere');
    // Its parent is the wrapper (display: contents), so it is an item of the
    // labels item, which sends it to the next line when needed; added after
    // the button, it never moves the button (DFLT-00295).
    const anchor = button.parentElement as HTMLElement;
    expect(alert.parentElement).toBe(anchor.parentElement);
    expect(alert.parentElement).toHaveClass('contents');
    expect(alert.parentElement?.parentElement).toBe(labels);
    expect(anchor.nextElementSibling).toBe(alert);
    // While there is an error the labels item grows into the rest of its line
    // of the bar, so the message is not squeezed under the labels; the
    // variant matches only an element holding a role="alert" (DFLT-00295).
    expect(labels).toHaveClass('has-[[role=alert]]:grow');
    expect(labels).not.toHaveClass('grow');
    expect(labels.matches(':has([role=alert])')).toBe(true);
    // The panel is positioned from the anchor, which holds the button but not
    // the error, so the error does not move it (DFLT-00295).
    expect(anchor).toHaveClass('relative');
    expect(anchor).not.toContainElement(alert);
    expect(anchor).toContainElement(within(labels).getByRole('group'));
    // The button still inherits no wrap-anywhere.
    expectNoInheritedWrapAnywhere(button);
  });
});

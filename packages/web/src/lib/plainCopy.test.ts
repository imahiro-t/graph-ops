// DFLT-00310: a copy touching an element drawn with U+200B / U+2060 (a
// PLAIN_COPY_ATTR element) puts its text on the clipboard without them; any
// other copy is left to the browser. See lib/plainCopy.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { dispatchCopy, select, selectContents } from '../test/copyEvent';
import { handlePlainCopy, installPlainCopy, PLAIN_COPY_ATTR } from './plainCopy';
import { WBR_MARK, WORD_JOINER, ZWSP } from './wbr';

const LABEL = `作成${ZWSP}日時${WORD_JOINER}:`;

let uninstall: () => void = () => {};

// <p id="before">説明 <wbr/></p>
// <div><span data-plain-copy>作成 ZWSP 日時 WORD_JOINER :</span> <span id="value">2026/9/28</span></div>
// <p id="after">後ろ ZWSP の説明</p><textarea>入力 ZWSP</textarea>
const build = () => {
  document.body.innerHTML = '';
  const before = document.createElement('p');
  before.textContent = `説明 ${WBR_MARK}`;
  const item = document.createElement('div');
  const label = document.createElement('span');
  label.setAttribute(PLAIN_COPY_ATTR, '');
  label.textContent = LABEL;
  const value = document.createElement('span');
  value.textContent = '2026/9/28';
  item.append(label, ' ', value);
  const after = document.createElement('p');
  after.textContent = `後ろ${ZWSP}の説明`;
  const field = document.createElement('textarea');
  field.value = `入力${ZWSP}`;
  document.body.append(before, item, after, field);
  return { before, label, value, after, field, text: (el: Element) => el.firstChild as Text };
};

type Built = ReturnType<typeof build>;

const expectNoInvisible = (s: string) => {
  expect(s).not.toContain(ZWSP);
  expect(s).not.toContain(WORD_JOINER);
};

beforeEach(() => {
  uninstall = installPlainCopy(document);
});

afterEach(() => {
  uninstall();
  document.getSelection()?.removeAllRanges();
  document.body.innerHTML = '';
});

describe('plainCopy (DFLT-00310)', () => {
  it('copies a label alone without U+200B / U+2060, in text/plain and text/html', () => {
    const { label } = build();
    selectContents(label);
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toBe('作成日時:');
    expect(written['text/html']).toContain('作成日時:');
    expectNoInvisible(written['text/html']);
  });

  it('copies a label with its value without the invisible characters', () => {
    const { label, value, text } = build();
    select(text(label), 0, text(value));
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toBe('作成日時: 2026/9/28');
    expect(written['text/html']).toContain('2026/9/28');
    expectNoInvisible(written['text/html']);
  });

  it('covers a selection starting before the label, and keeps a "<wbr/>" string', () => {
    const { before, label, text } = build();
    select(text(before), 0, text(label));
    const { event, written } = dispatchCopy(before);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toContain(`説明 ${WBR_MARK}`);
    expect(written['text/plain']).toContain('作成日時:');
    expectNoInvisible(written['text/plain']);
    // In HTML the string is text, so it is escaped rather than an element.
    expect(written['text/html']).toContain('説明 &lt;wbr/&gt;');
    expectNoInvisible(written['text/html']);
  });

  it('removes the characters from the rest of a selection that touches a label', () => {
    const { label, after, text } = build();
    select(text(label), 0, text(after));
    const { written } = dispatchCopy(label);
    expect(written['text/plain']).toContain('後ろの説明');
    expectNoInvisible(written['text/plain']);
  });

  it.each([
    // "成" U+200B "日" is drawn as "成日".
    [1, 4, ZWSP, '成日'],
    // "時" U+2060 ":" is drawn as "時:".
    [4, 7, WORD_JOINER, '時:']
  ])('copies part of a label (%i-%i) without the invisible character it holds', (start, end, invisible, expected) => {
    const { label, text } = build();
    const selection = select(text(label), start, text(label), end);
    expect(selection.toString()).toContain(invisible);
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toBe(expected);
    expectNoInvisible(written['text/html']);
  });

  it('leaves a selection that touches no label to the browser', () => {
    const { after } = build();
    selectContents(after);
    const { event, written } = dispatchCopy(after);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });

  // A range whose edge only touches the label, holding none of its characters
  // (Range.intersectsNode is true for these): a triple click or a drag to the
  // start of the next line ends a selection at (label text, 0).
  it.each([
    ['ending at the start of the label text', ({ before, label, text }: Built) => select(text(before), 0, text(label), 0)],
    ['ending at the start of the label element', ({ before, label, text }: Built) => select(text(before), 0, label, 0)],
    [
      'starting at the end of the label text',
      ({ label, value, text }: Built) => select(text(label), LABEL.length, text(value))
    ],
    ['starting at the end of the label element', ({ label, after, text }: Built) => select(label, 1, text(after))]
  ])('leaves a selection %s to the browser', (_name, make) => {
    const built = build();
    const selection = make(built);
    expect(selection.isCollapsed).toBe(false);
    expect(selection.getRangeAt(0).intersectsNode(built.label)).toBe(true);
    expect(selection.toString()).not.toContain('作成');
    const { event, written } = dispatchCopy(built.before);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });

  it('covers a selection holding only the first or last character of the label', () => {
    const { before, label, value, text } = build();
    select(text(before), 0, text(label), 1);
    let copied = dispatchCopy(before);
    expect(copied.event.defaultPrevented).toBe(true);
    expect(copied.written['text/plain']).toContain('作');
    select(text(label), LABEL.length - 1, text(value));
    copied = dispatchCopy(label);
    expect(copied.event.defaultPrevented).toBe(true);
    expect(copied.written['text/plain']).toBe(': 2026/9/28');
  });

  it('leaves a copy with a collapsed selection to the browser', () => {
    const { label, text } = build();
    select(text(label), 2, text(label), 2);
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });

  it('leaves a copy from a field to the browser, even with a label selected', () => {
    const { label, field } = build();
    selectContents(label);
    field.focus();
    const { event, written } = dispatchCopy(field);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });

  it('writes nothing for a copy another handler already cancelled', () => {
    const { label } = build();
    selectContents(label);
    const { written } = dispatchCopy(label, { prevent: true });
    expect(written).toEqual({});
  });

  it('does nothing without clipboardData', () => {
    const { label } = build();
    selectContents(label);
    const event = new Event('copy', { bubbles: true, cancelable: true }) as ClipboardEvent;
    expect(() => handlePlainCopy(event)).not.toThrow();
    expect(event.defaultPrevented).toBe(false);
  });

  it('stops listening once uninstalled', () => {
    const { label } = build();
    uninstall();
    uninstall = () => {};
    selectContents(label);
    const { event, written } = dispatchCopy(label);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });
});

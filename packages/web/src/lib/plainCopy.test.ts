// DFLT-00310: a copy touching an element drawn with U+200B / U+2060 (a
// PLAIN_COPY_ATTR element) puts its text on the clipboard without them; any
// other copy is left to the browser. See lib/plainCopy.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { dispatchCopy, select, selectContents } from '../test/copyEvent';
import { handlePlainCopy, installPlainCopy, PLAIN_COPY_ATTR } from './plainCopy';
import { stripInvisible, WBR_MARK, WORD_JOINER, ZWSP } from './wbr';

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

// DFLT-00317: the text/html of such a copy leaves out what cannot be
// selected (user-select: none, unless a descendant turns it back on) and
// screen-reader text (sr-only), and keeps only four text styles inline.
describe('plainCopy text/html (DFLT-00317)', () => {
  const OTHER_LABEL = `更新${ZWSP}日時${WORD_JOINER}:`;
  const labelHtml = (text = LABEL) => `<span ${PLAIN_COPY_ATTR}="">${text}</span>`;
  const RED = 'rgb(200, 0, 0)';
  let sheet: HTMLStyleElement;

  beforeEach(() => {
    sheet = document.createElement('style');
    sheet.textContent = [
      '.select-none { user-select: none }',
      '.select-text { user-select: text }',
      '.font-bold { font-weight: 700 }',
      '.italic { font-style: italic }',
      '.underline { text-decoration-line: underline }',
      `.text-red { color: ${RED} }`,
      '.truncate { overflow: hidden; white-space: nowrap; text-overflow: ellipsis }'
    ].join('\n');
    document.head.appendChild(sheet);
  });

  afterEach(() => {
    sheet.remove();
    vi.restoreAllMocks();
  });

  const mount = (html: string) => {
    document.body.innerHTML = html;
  };
  const byId = (id: string) => document.getElementById(id) as HTMLElement;
  const textOf = (id: string) => byId(id).firstChild as Text;

  // Copies the current selection and returns what it wrote as HTML.
  const copyHtml = () => {
    const { event, written } = dispatchCopy(document.body);
    expect(event.defaultPrevented).toBe(true);
    return written['text/html'];
  };

  const parse = (html: string) => {
    const div = document.createElement('div');
    div.innerHTML = html;
    return div;
  };

  // The copied element whose own text (first text child) is `text`.
  const copyOf = (root: Element, text: string) =>
    Array.from(root.querySelectorAll('*')).find(el => el.firstChild?.nodeValue === text) as Element;

  // Makes getComputedStyle return `pick(el)` as user-select wherever it
  // gives a value, so a test can play Chromium (inherited values) or
  // Firefox (auto for a child).
  const withUserSelect = (pick: (el: Element) => string | null) => {
    const view = document.defaultView as Window & typeof globalThis;
    const original = view.getComputedStyle.bind(view);
    vi.spyOn(view, 'getComputedStyle').mockImplementation((el, pseudo) => {
      const style = original(el, pseudo);
      const value = pick(el);
      if (value === null) return style;
      return new Proxy(style, {
        get(target, prop) {
          if (prop === 'getPropertyValue') {
            return (name: string) =>
              name === 'user-select' || name === '-webkit-user-select' ? value : target.getPropertyValue(name);
          }
          const member = Reflect.get(target, prop, target);
          return typeof member === 'function' ? member.bind(target) : member;
        }
      });
    });
  };

  it('leaves out an unselectable label chip and icon', () => {
    mount(
      `<div id="row">${labelHtml()} <span>2026-09-29 06:42</span>` +
        '<span class="select-none">UI改善</span><svg class="select-none"><path d="M0 0"></path></svg></div>'
    );
    selectContents(byId('row'));
    const html = copyHtml();
    expect(html).toContain('作成日時:');
    expect(html).toContain('2026-09-29 06:42');
    expect(html).not.toContain('UI改善');
    expect(html).not.toContain('<svg');
  });

  it('leaves out the descendants of an unselectable element', () => {
    mount(`<div id="row">${labelHtml()}<span class="select-none"><span>9/9</span></span></div>`);
    selectContents(byId('row'));
    const html = copyHtml();
    expect(html).not.toContain('9/9');
    expect(html).toContain('作成日時:');
  });

  it('keeps a select-text part of a select-none row, in a holder without class', () => {
    mount(
      '<div id="item"><div id="header" class="select-none">▶' +
        '<span class="select-text">DFLT-00317</span>' +
        '<span class="select-text">Safari・Firefox でのコピー</span>' +
        '<span class="select-none">9/9</span><span class="select-none">UI改善</span></div>' +
        `<div id="bar"><span id="label" ${PLAIN_COPY_ATTR}="">${LABEL}</span></div></div>`
    );
    select(textOf('header'), 0, textOf('label'));
    const html = copyHtml();
    expect(html).toContain('DFLT-00317');
    expect(html).toContain('Safari・Firefox でのコピー');
    for (const left of ['▶', '9/9', 'UI改善']) expect(html).not.toContain(left);
    const holder = copyOf(parse(html), 'DFLT-00317').parentElement as Element;
    expect(holder.tagName).toBe('DIV');
    expect(holder.hasAttribute('class')).toBe(false);
  });

  it.each([
    // Chromium: a child of a select-none element reads none.
    [
      'inherited values (Chromium)',
      (el: Element) => (el.closest('.select-text') ? 'text' : el.closest('.select-none') ? 'none' : 'auto')
    ],
    // Firefox: a child with no user-select of its own reads auto.
    [
      'auto for a child (Firefox)',
      (el: Element) => (el.matches('.select-text') ? 'text' : el.matches('.select-none') ? 'none' : 'auto')
    ]
  ])('starts from the state above the range, with %s', (_name, pick) => {
    withUserSelect(pick);
    mount(
      `<div>${labelHtml()}</div><div class="select-none"><div id="common">${labelHtml(OTHER_LABEL)}` +
        '<span>補足</span><span class="select-text">タイトル</span></div></div>'
    );
    selectContents(byId('common'));
    const html = copyHtml();
    expect(html).not.toContain('補足');
    expect(html).not.toContain('更新日時:');
    expect(html).toContain('タイトル');
  });

  it('writes nothing of a range under an sr-only ancestor', () => {
    mount(
      `<div>${labelHtml()}</div><div id="hidden" class="sr-only">` +
        `<div id="inner">${labelHtml(OTHER_LABEL)} <span>2026-09-29 07:00</span></div></div>`
    );
    selectContents(byId('inner'));
    const html = copyHtml();
    expect(html).toBe('');
  });

  it('leaves out an sr-only element with its descendants', () => {
    mount(
      `<div id="row">${labelHtml()} <span>2026-09-29 06:42</span>` +
        '<span class="sr-only">読み上げ用の説明<span>（補足）</span></span></div>'
    );
    selectContents(byId('row'));
    const html = copyHtml();
    expect(html).not.toContain('読み上げ用の説明');
    expect(html).not.toContain('（補足）');
    expect(html).toContain('作成日時:');
    expect(html).toContain('2026-09-29 06:42');
  });

  it('keeps a small hidden element without the sr-only class', () => {
    mount(
      `<div id="row">${labelHtml()} <span>2026-09-29 06:42</span>` +
        '<span style="position: absolute; width: 1px; height: 1px; overflow: hidden">小さな注記</span></div>'
    );
    selectContents(byId('row'));
    expect(copyHtml()).toContain('小さな注記');
  });

  it('drops class and id and writes the four text styles inline', () => {
    mount(
      `<div id="row">${labelHtml()} <span id="meta-value" class="truncate font-bold text-red">2026-09-29 06:42</span>` +
        '<span class="italic">（暫定）</span>' +
        '<a class="underline" href="/tickets/DFLT-00310">DFLT-00310</a></div>'
    );
    selectContents(byId('row'));
    const html = copyHtml();
    expect(html).not.toContain('class=');
    expect(html).not.toContain('id=');
    const root = parse(html);
    const value = copyOf(root, '2026-09-29 06:42').getAttribute('style') ?? '';
    expect(value).toMatch(/font-weight: (700|bold)/);
    expect(value).toContain(`color: ${RED}`);
    expect(copyOf(root, '（暫定）').getAttribute('style')).toContain('font-style: italic');
    const link = copyOf(root, 'DFLT-00310');
    expect(link.getAttribute('style')).toContain('text-decoration-line: underline');
    expect(link.getAttribute('href')).toBe('/tickets/DFLT-00310');
  });

  it('does not carry over a layout inline style', () => {
    mount(
      `<div id="row">${labelHtml()} ` +
        '<span style="max-width: 10rem; overflow: hidden; white-space: nowrap">2026-09-29 06:42</span></div>'
    );
    selectContents(byId('row'));
    const style = copyOf(parse(copyHtml()), '2026-09-29 06:42').getAttribute('style') ?? '';
    for (const name of ['max-width', 'overflow', 'white-space']) expect(style).not.toContain(name);
  });

  it('pastes into a contenteditable on the page without any class, so truncate does not apply', () => {
    const title = 'Safari・Firefox でのコピーで、ラベルの不可視文字が入る';
    mount(
      `<div id="item"><span class="truncate">${title}</span><div>${labelHtml()}</div></div>` +
        '<div id="editor" contenteditable="true"></div>'
    );
    selectContents(byId('item'));
    const html = copyHtml();
    const editor = byId('editor');
    editor.innerHTML = html;
    expect(editor.querySelectorAll('[class]')).toHaveLength(0);
    expect(editor.textContent).toContain(title);
  });

  it("wraps each range in a span with its common ancestor's styles, not repeated below", () => {
    mount(
      `<div id="outer" class="font-bold text-red">前文<span id="child">` +
        `<span id="label" ${PLAIN_COPY_ATTR}="">${LABEL}</span></span></div>`
    );
    select(textOf('outer'), 1, textOf('label'));
    const root = parse(copyHtml());
    expect(root.childNodes).toHaveLength(1);
    const wrap = root.firstElementChild as Element;
    expect(wrap.tagName).toBe('SPAN');
    expect(wrap.getAttribute('style')).toMatch(/font-weight: (700|bold)/);
    expect(wrap.getAttribute('style')).toContain(`color: ${RED}`);
    expect(wrap.firstChild?.nodeValue).toBe('文');
    const child = wrap.childNodes[1] as Element;
    const style = child.getAttribute('style') ?? '';
    expect(style).not.toContain('font-weight');
    expect(style).not.toContain('color');
  });

  // The copy with the wrapping span taken off and without class, id and
  // style, against Range.cloneContents treated the same way.
  const bare = (root: Element) => {
    for (const el of Array.from(root.querySelectorAll('*'))) {
      for (const name of ['class', 'id', 'style']) el.removeAttribute(name);
    }
    return root.innerHTML;
  };
  const unwrapped = (html: string) => {
    const root = parse(html);
    const wrap = root.firstElementChild as Element;
    expect(root.childNodes).toHaveLength(1);
    const div = document.createElement('div');
    div.append(...Array.from(wrap.childNodes));
    return bare(div);
  };
  const cloned = (range: Range) => {
    const div = document.createElement('div');
    div.appendChild(range.cloneContents());
    const walker = document.createTreeWalker(div, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      node.nodeValue = stripInvisible(node.nodeValue ?? '');
    }
    return bare(div);
  };

  it.each([
    ['within text nodes', () => select(textOf('before'), 1, textOf('date'), 4)],
    ['by element and offset', () => select(byId('row'), 1, byId('row'), 4)]
  ])('cuts a range given %s as Range.cloneContents does', (_name, make) => {
    mount(
      `<div id="row"><span id="before">前の文</span><span ${PLAIN_COPY_ATTR}="">${LABEL}</span> ` +
        '<b id="date">2026-09-29</b><span>後ろ</span></div>'
    );
    const selection = make();
    const expected = cloned(selection.getRangeAt(0));
    const plain = stripInvisible(selection.toString());
    const html = copyHtml();
    expect(unwrapped(html)).toBe(expected);
    expect(parse(html).textContent).toBe(plain);
  });

  it('joins two ranges in order, each in its own span', () => {
    mount(`<div id="first">${labelHtml()} <span>2026-09-29 06:42</span></div><p id="second">クローズ</p>`);
    const rs = ['first', 'second'].map(id => {
      const range = document.createRange();
      range.selectNodeContents(byId(id));
      return range;
    });
    // jsdom keeps one range per selection; Firefox keeps several.
    const stub = {
      rangeCount: rs.length,
      isCollapsed: false,
      getRangeAt: (i: number) => rs[i],
      toString: () => rs.map(String).join('')
    };
    vi.spyOn(document, 'getSelection').mockReturnValue(stub as unknown as Selection);
    const html = copyHtml();
    expect(html.indexOf('作成日時:')).toBeGreaterThanOrEqual(0);
    expect(html.indexOf('作成日時:')).toBeLessThan(html.indexOf('クローズ'));
    const root = parse(html);
    expect(root.childNodes).toHaveLength(2);
    for (const wrap of Array.from(root.childNodes)) expect((wrap as Element).tagName).toBe('SPAN');
  });

  it('keeps text/plain as the selection without the invisible characters', () => {
    mount(`<div id="row">${labelHtml()} <span>2026-09-29 06:42</span><span class="select-none">UI改善</span></div>`);
    const selection = selectContents(byId('row'));
    const { written } = dispatchCopy(document.body);
    expect(written['text/plain']).toBe(stripInvisible(selection.toString()));
    expect(written['text/plain']).toContain('UI改善');
    expectNoInvisible(written['text/plain']);
  });

  it('leaves a selection with no label to the browser, unselectable parts and all', () => {
    mount(`<div>${labelHtml()}</div><div id="row"><span class="select-none">UI改善</span><span>値</span></div>`);
    selectContents(byId('row'));
    const { event, written } = dispatchCopy(document.body);
    expect(event.defaultPrevented).toBe(false);
    expect(written).toEqual({});
  });

  it('still strips the invisible characters and keeps a "<wbr/>" string', () => {
    // Escaped, so the page holds the string rather than a <wbr> element.
    mount(`<div id="row">${labelHtml()} <span>値 ${WBR_MARK.replace(/</g, '&lt;')}</span></div>`);
    selectContents(byId('row'));
    const html = copyHtml();
    expectNoInvisible(html);
    expect(html).toContain('値 &lt;wbr/&gt;');
  });
});

// Copying drawn labels without their invisible characters (DFLT-00310).
//
// The metadata bar's labels ("作成日時:", "Created:") and the "Edit labels"
// button's name are each drawn as one text node, with U+200B between the
// words and U+2060 before the colon (lib/wbr). That keeps each one a single
// text in Chromium's accessibility tree (DFLT-00295), so the drawing stays as
// it is. But a copy took those characters to the clipboard, where a search or
// a comparison with the words as typed then fails. So the copy is fixed here,
// on its way to the clipboard, instead of in the drawing: an element drawn
// that way carries PLAIN_COPY_ATTR, and when a copied selection holds any of
// its characters, the clipboard gets the selection's text/plain and
// text/html without U+200B / U+2060 (stripInvisible, which keeps a "<wbr/>"
// string). A selection that holds none -- even one whose edge touches a
// label -- is left to the browser untouched.
//
// Out of scope: innerText / textContent still hold the characters -- that is
// the one-text-node drawing itself. The characters are removed from the whole
// selection, not only from the labels' part of it: telling which characters
// of a selection came from a label would mean mapping its boundaries onto
// each label's text node, and a U+200B / U+2060 is more often a nuisance than
// wanted in pasted text anyway. The text/html written here only comes close
// to the browser's own, without matching it (DFLT-00317, see selectionHtml).
import { stripInvisible } from './wbr';

// Marks an element whose text is drawn with U+200B / U+2060 (lib/wbr).
export const PLAIN_COPY_ATTR = 'data-plain-copy';

// Spread onto such an element: <span {...plainCopyProps}>.
export const plainCopyProps = { [PLAIN_COPY_ATTR]: '' } as const;

const isEditable = (node: unknown): boolean => {
  if (!node || typeof node !== 'object' || !('nodeType' in node)) return false;
  const el = (node as Node).nodeType === 1 ? (node as Element) : (node as Node).parentElement;
  if (!el) return false;
  if (el.closest('input, textarea, select')) return true;
  const editable = el.closest('[contenteditable]');
  return !!editable && editable.getAttribute('contenteditable') !== 'false';
};

const ranges = (selection: Selection): Range[] => {
  const out: Range[] = [];
  for (let i = 0; i < selection.rangeCount; i++) out.push(selection.getRangeAt(i));
  return out;
};

// The selection's text/html (DFLT-00317). Range.cloneContents would copy
// every node in the range with its class attribute and nothing else, while a
// browser's own copy leaves out what cannot be selected and writes computed
// styles inline instead of classes. Pasted into rich text, the clone then
// held icons, label chips and screen-reader text, and pasted into this page
// its Tailwind classes came back to life (a title cut short by "truncate").
// So the copy is rebuilt here by walking the original DOM (a clone cannot
// be matched against it afterwards), with these rules for what it holds:
// - An element whose computed user-select is none is left out, and so are
//   its descendants, unless one of them turns selection back on (text, all,
//   contain) -- the header row is select-none but its ticket ID and title
//   are select-text. auto or an empty value takes the parent's state, and
//   the state above the range starts from the nearest ancestor that sets
//   one, so a browser returning inherited values (Chromium) and one
//   returning auto for a child (Firefox) give the same result.
// - An element with the sr-only class is left out with all it holds, as is
//   the whole range when one of its ancestors has it. The class alone
//   decides, since telling screen-reader text by its computed box (1px by
//   1px, absolute) could drop a small element that is meant to be seen.
// - Each copied element loses its class, id and style attributes and gets
//   only font-weight, font-style, color and text-decoration-line from its
//   computed style, as an inline style. font-weight, font-style and color
//   are written only where they differ from the parent, as they inherit.
// - Each range is wrapped in a span carrying its common ancestor's styles,
//   which the range's own text directly under that ancestor would lose
//   otherwise. A range left with nothing to copy gives no span.
// Matching the browser's text/html exactly (every computed style, its
// <meta charset> and fragment comments) is not a goal: pasted text keeps
// what it means -- the text and its emphasis -- not the page's layout.
const selectionHtml = (doc: Document, rs: Range[]): string => {
  const container = doc.createElement('div');
  const styles = computedStyles(doc);
  for (const range of rs) {
    const wrap = rangeHtml(doc, range, styles);
    if (wrap) container.appendChild(wrap);
  }
  const walker = doc.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.nodeValue ?? '';
    const stripped = stripInvisible(text);
    if (stripped !== text) node.nodeValue = stripped;
  }
  return container.innerHTML;
};

type StyleOf = (el: Element) => CSSStyleDeclaration | null;

// getComputedStyle, read once per element of a copy.
const computedStyles = (doc: Document): StyleOf => {
  const cache = new Map<Element, CSSStyleDeclaration | null>();
  return el => {
    let style = cache.get(el);
    if (style === undefined) {
      style = doc.defaultView?.getComputedStyle(el) ?? null;
      cache.set(el, style);
    }
    return style;
  };
};

const SELECTABLE = new Set(['text', 'all', 'contain']);

const userSelectOf = (style: CSSStyleDeclaration | null): string =>
  style?.getPropertyValue('user-select') || style?.getPropertyValue('-webkit-user-select') || '';

// An element's selectability from its computed user-select: none turns it
// off, text / all / contain turn it back on, and anything else (auto, or
// empty where the value is not computed) keeps `inherited`.
const selectable = (style: CSSStyleDeclaration | null, inherited: boolean): boolean => {
  const value = userSelectOf(style);
  if (value === 'none') return false;
  if (SELECTABLE.has(value)) return true;
  return inherited;
};

// Whether `el` is selectable, from the nearest of it and its ancestors that
// sets user-select; selectable when none does.
const selectableFrom = (el: Element, styleOf: StyleOf): boolean => {
  for (let node: Element | null = el; node; node = node.parentElement) {
    const value = userSelectOf(styleOf(node));
    if (value === 'none') return false;
    if (SELECTABLE.has(value)) return true;
  }
  return true;
};

const isSrOnly = (el: Element): boolean => el.classList.contains('sr-only');

const INHERITED_STYLES = ['font-weight', 'font-style', 'color'] as const;

// The inline style for a copy of an element with `style`: the inherited
// three where they differ from `parent` (all of them for no parent), and a
// text-decoration-line other than none. Empty values are never written.
const inlineStyle = (style: CSSStyleDeclaration | null, parent: CSSStyleDeclaration | null): string => {
  if (!style) return '';
  const parts: string[] = [];
  for (const name of INHERITED_STYLES) {
    const value = style.getPropertyValue(name);
    if (value && (!parent || parent.getPropertyValue(name) !== value)) parts.push(`${name}: ${value}`);
  }
  const line = style.getPropertyValue('text-decoration-line');
  if (line && line !== 'none') parts.push(`text-decoration-line: ${line}`);
  return parts.join('; ');
};

const setInlineStyle = (el: Element, style: string): void => {
  if (style) el.setAttribute('style', style);
  else el.removeAttribute('style');
};

// One range's copy, wrapped in a span with its common ancestor's styles, or
// null when nothing in it is copied.
const rangeHtml = (doc: Document, range: Range, styleOf: StyleOf): Element | null => {
  const common = range.commonAncestorContainer;
  const base = common.nodeType === Node.ELEMENT_NODE ? (common as Element) : common.parentElement;
  if (!base || base.closest('.sr-only')) return null;
  const on = selectableFrom(base, styleOf);
  const wrap = doc.createElement('span');
  if (common === base) {
    for (const child of Array.from(base.childNodes)) {
      const copy = copyNode(range, child, on, base, styleOf);
      if (copy) wrap.appendChild(copy);
    }
  } else {
    // The range lies within one text (or other character data) node.
    const copy = copyNode(range, common, on, base, styleOf);
    if (copy) wrap.appendChild(copy);
  }
  if (!wrap.hasChildNodes()) return null;
  setInlineStyle(wrap, inlineStyle(styleOf(base), null));
  return wrap;
};

// Text, CDATA section, processing instruction and comment nodes.
const CHARACTER_DATA = new Set([3, 4, 7, 8]);

// The copy of `node`'s part in `range`, or null when none of it is copied.
// Like Range.cloneContents, a node the range only partly holds is copied
// shallow with its held part inside, and character data is cut at the
// range's ends. `on` is the parent's selectability and `parent` the source
// element whose styles the copy is written against.
const copyNode = (
  range: Range,
  node: Node,
  on: boolean,
  parent: Element,
  styleOf: StyleOf
): Node | null => {
  if (!range.intersectsNode(node)) return null;
  if (node.nodeType !== Node.ELEMENT_NODE) {
    if (!on) return null;
    const copy = node.cloneNode(false);
    if (CHARACTER_DATA.has(node.nodeType)) {
      const data = (node as CharacterData).data;
      const start = node === range.startContainer ? range.startOffset : 0;
      const end = node === range.endContainer ? range.endOffset : data.length;
      (copy as CharacterData).data = data.slice(start, end);
      if (node.nodeType === Node.TEXT_NODE && start >= end) return null;
    }
    return copy;
  }
  const el = node as Element;
  if (isSrOnly(el)) return null;
  const style = styleOf(el);
  const selfOn = selectable(style, on);
  const copy = el.cloneNode(false) as Element;
  for (const child of Array.from(el.childNodes)) {
    const childCopy = copyNode(range, child, selfOn, el, styleOf);
    if (childCopy) copy.appendChild(childCopy);
  }
  // An unselectable element stays only as the holder of a selectable part.
  if (!selfOn && !copy.hasChildNodes()) return null;
  copy.removeAttribute('class');
  copy.removeAttribute('id');
  setInlineStyle(copy, inlineStyle(style, styleOf(parent)));
  return copy;
};

// Whether `range` holds at least one character of `el`'s text. Not
// Range.intersectsNode: that is also true for a range that only touches `el`
// -- one ending at (label text, 0) or starting at (label text, length), as a
// triple click or a drag to the start of the next line leaves it -- and such
// a copy holds none of the label's characters, so it stays the browser's.
// Each text node is compared by its own characters rather than by el's
// contents, since (el, 0) lies before (el's first text, 0): a range ending at
// the latter would still end after el's start. An empty `el` never matches.
const holdsTextOf = (doc: Document, range: Range, el: Element): boolean => {
  const walker = doc.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const chars = doc.createRange();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const length = node.nodeValue?.length ?? 0;
    if (length === 0) continue;
    chars.setStart(node, 0);
    chars.setEnd(node, length);
    // The range ends after the text's first character starts and starts
    // before its last character ends.
    if (
      range.compareBoundaryPoints(Range.START_TO_END, chars) > 0 &&
      range.compareBoundaryPoints(Range.END_TO_START, chars) < 0
    ) {
      return true;
    }
  }
  return false;
};

const documentOf = (event: Event): Document => {
  const current = event.currentTarget as Node | null;
  if (current && current.nodeType === 9) return current as Document;
  const target = event.target as Node | null;
  return target?.ownerDocument ?? document;
};

// Handles one copy event: writes the plain text and HTML of a selection
// that holds a character of a PLAIN_COPY_ATTR element, and does nothing
// otherwise. Only reads clipboardData.setData, so a test can hand it any
// event carrying one.
export const handlePlainCopy = (event: ClipboardEvent): void => {
  const data = event.clipboardData;
  if (event.defaultPrevented || !data) return;
  const doc = documentOf(event);
  // A selection inside a field is not the document's Selection: leave it to
  // the browser (the label picker's search box, a reject reason).
  if (isEditable(event.target) || isEditable(doc.activeElement)) return;
  const selection = doc.getSelection();
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) return;
  const rs = ranges(selection);
  const marked = Array.from(doc.querySelectorAll(`[${PLAIN_COPY_ATTR}]`));
  if (!marked.some(el => rs.some(range => holdsTextOf(doc, range, el)))) return;
  data.setData('text/plain', stripInvisible(selection.toString()));
  data.setData('text/html', selectionHtml(doc, rs));
  event.preventDefault();
};

// Listens for copy on `doc`; returns the function that stops listening.
export const installPlainCopy = (doc: Document = document): (() => void) => {
  doc.addEventListener('copy', handlePlainCopy);
  return () => doc.removeEventListener('copy', handlePlainCopy);
};

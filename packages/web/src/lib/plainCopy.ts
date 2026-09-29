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
// its characters,
// the clipboard gets the selection's text/plain and text/html without
// U+200B / U+2060 (stripInvisible, which keeps a "<wbr/>" string). A
// selection that holds none -- even one whose edge touches a label -- is left
// to the browser untouched.
//
// Out of scope: innerText / textContent still hold the characters -- that is
// the one-text-node drawing itself. The characters are removed from the whole
// selection, not only from the labels' part of it: telling which characters
// of a selection came from a label would mean mapping its boundaries onto
// each label's text node, and a U+200B / U+2060 is more often a nuisance than
// wanted in pasted text anyway.
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

// The selection's ranges as HTML, without U+200B / U+2060 in its text.
const selectionHtml = (doc: Document, rs: Range[]): string => {
  const container = doc.createElement('div');
  for (const range of rs) container.appendChild(range.cloneContents());
  const walker = doc.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.nodeValue ?? '';
    const stripped = stripInvisible(text);
    if (stripped !== text) node.nodeValue = stripped;
  }
  return container.innerHTML;
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

// Handles one copy event: writes the plain text and HTML of a selection that
// holds a character of a PLAIN_COPY_ATTR element, and does nothing otherwise. Only reads
// clipboardData.setData, so a test can hand it any event carrying one.
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

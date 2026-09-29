// Test helpers for copy events (DFLT-00310). jsdom has no ClipboardEvent or
// DataTransfer, so a copy is a plain cancelable Event carrying a
// clipboardData that records what was written to it.

export interface CopyResult {
  event: Event;
  // Everything setData wrote, by type; empty when the listener wrote nothing.
  written: Record<string, string>;
}

// Dispatches a copy event on `target` (the element the copy starts from),
// bubbling to the document. `prevent` cancels it first, as another handler
// would.
export const dispatchCopy = (target: EventTarget, { prevent = false }: { prevent?: boolean } = {}): CopyResult => {
  const written: Record<string, string> = {};
  const event = new Event('copy', { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'clipboardData', {
    value: {
      setData: (type: string, value: string) => {
        written[type] = value;
      },
      getData: (type: string) => written[type] ?? ''
    }
  });
  if (prevent) event.preventDefault();
  target.dispatchEvent(event);
  return { event, written };
};

// Selects from (startNode, startOffset) to (endNode, endOffset) as the
// document's only range.
export const select = (startNode: Node, startOffset: number, endNode: Node = startNode, endOffset?: number): Selection => {
  const range = document.createRange();
  range.setStart(startNode, startOffset);
  range.setEnd(endNode, endOffset ?? (endNode.nodeType === Node.TEXT_NODE ? (endNode.nodeValue ?? '').length : endNode.childNodes.length));
  const selection = document.getSelection() as Selection;
  selection.removeAllRanges();
  selection.addRange(range);
  return selection;
};

// Selects all of `node`'s contents.
export const selectContents = (node: Node): Selection => {
  const selection = document.getSelection() as Selection;
  selection.removeAllRanges();
  const range = document.createRange();
  range.selectNodeContents(node);
  selection.addRange(range);
  return selection;
};

// Where keyboard focus should go once an item has been removed from a list
// (DFLT-00191): deleting a row or card unmounts the button that was focused,
// which would otherwise drop focus to <body>. Same rule as LabelsEditor's
// focusAfterRemoval: the item that took the removed one's place (the next
// item), else the new last item (the previous one), else null -- the caller
// then falls back to a control near the list (its heading or "add" button).
//
// ids is the list as it was before the removal (where removedId's position is
// read from); remainingIds is the list as it is now. When removedId is not in
// ids (another client removed it first), the first remaining item is chosen.
export function neighborAfterRemoval(ids: readonly string[], removedId: string, remainingIds: readonly string[]): string | null {
  const index = ids.indexOf(removedId);
  return remainingIds[Math.max(index, 0)] ?? remainingIds[remainingIds.length - 1] ?? null;
}

// A [data-focus-key="..."] selector for key, escaped so an id containing
// quotes, brackets or backslashes cannot break (or change) the selector.
export function focusKeySelector(key: string): string {
  return `[data-focus-key="${CSS.escape(key)}"]`;
}

// Whether keyboard focus is currently nowhere useful: on <body> (where it
// lands when the focused element is removed, and in some browsers when it is
// disabled), or on an element no longer in the document.
export function isFocusLost(): boolean {
  const active = document.activeElement;
  return !active || active === document.body || !active.isConnected;
}

// Focuses el unless the user has already put focus somewhere else meanwhile
// (a delete request can take a while, and pulling focus back from wherever
// they moved it would be worse than leaving it). Focusing el when it already
// has focus is harmless, so that case goes ahead too. Returns whether focus
// ended up on el.
export function focusIfLost(el: HTMLElement | null | undefined): boolean {
  if (!el) return false;
  if (document.activeElement !== el && !isFocusLost()) return false;
  el.focus();
  return document.activeElement === el;
}

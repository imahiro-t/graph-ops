// Test helpers for IconButton's tooltip (DFLT-00171). The tooltip is
// rendered into document.body through a portal and is aria-hidden, so it
// cannot be found by role; these look it up by its data attribute instead.
const TOOLTIP_SELECTOR = '[data-icon-button-tooltip]';

// Every tooltip element currently in the DOM, open or not.
export function allIconButtonTooltips(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll<HTMLElement>(TOOLTIP_SELECTOR));
}

// The open (not hidden) tooltips.
export function openIconButtonTooltips(): HTMLElement[] {
  return allIconButtonTooltips().filter(el => !el.hidden);
}

// The single open tooltip; fails when there is none or more than one.
export function openIconButtonTooltip(): HTMLElement {
  const open = openIconButtonTooltips();
  if (open.length !== 1) {
    throw new Error(`expected exactly one open IconButton tooltip, found ${open.length}`);
  }
  return open[0];
}

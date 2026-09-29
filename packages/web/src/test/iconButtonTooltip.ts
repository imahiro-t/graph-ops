// Test helpers for IconButton's tooltip (DFLT-00171). The tooltip is
// rendered into document.body through a portal and is aria-hidden, so it
// cannot be found by role; these look it up by its data attribute instead.
import { act } from '@testing-library/react';
import { OPEN_DELAY_MS } from '../components/IconButton';

const TOOLTIP_SELECTOR = '[data-icon-button-tooltip]';

// Hover opens a tooltip only after the pointer has rested on the button for
// OPEN_DELAY_MS (DFLT-00322). Call this after user.hover() before looking
// for the tooltip -- and before asserting that none opened, so such a check
// is not passed merely because the delay has not run out yet.
export async function waitForHoverOpenDelay(): Promise<void> {
  await act(async () => {
    await new Promise(resolve => setTimeout(resolve, OPEN_DELAY_MS + 20));
  });
}

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

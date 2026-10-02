// Test helpers for IconButton's tooltip (DFLT-00171). The tooltip is
// rendered into document.body through a portal and is aria-hidden, so it
// cannot be found by role; these look it up by its data attribute instead.
import { act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, onTestFinished, vi } from 'vitest';
import { CLOSE_DELAY_MS, OPEN_DELAY_MS } from '../components/IconButton';

const TOOLTIP_SELECTOR = '[data-icon-button-tooltip]';

// Hover opens a tooltip only after the pointer has rested on the button for
// OPEN_DELAY_MS (DFLT-00322). Call this after user.hover() before looking
// for the tooltip -- and before asserting that none opened, so such a check
// is not passed merely because the delay has not run out yet.
//
// It moves the fake clock past the delay instead of waiting for it in real
// time (DFLT-00341), so a test that hovers must run under fake timers: call
// useHoverFakeTimers() in its describe (or startHoverFakeTimers() at the top
// of the test) and create its user with setupHoverUser(). Without fake
// timers it throws rather than falling back to a real wait, so a missing
// setup fails loudly instead of slowing tests down.
export async function waitForHoverOpenDelay(): Promise<void> {
  if (!vi.isFakeTimers()) {
    throw new Error(
      'waitForHoverOpenDelay() needs fake timers: call useHoverFakeTimers() in the describe or startHoverFakeTimers() at the top of the test, and create the user with setupHoverUser()'
    );
  }
  await act(async () => {
    // The clock only moves forward after the hover, so advancing it by the
    // whole delay always fires an open scheduled by that hover.
    await vi.advanceTimersByTimeAsync(OPEN_DELAY_MS);
  });
}

// Hover closes a tooltip only CLOSE_DELAY_MS after the pointer has left the
// button or the tooltip. Call this after user.unhover() (or after moving onto
// the tooltip, a pointerdown or Escape) before checking whether the tooltip
// closed or stayed open, so the check is made once a pending close would
// have fired.
//
// Like waitForHoverOpenDelay(), it moves the fake clock instead of waiting in
// real time (DFLT-00348) and throws without fake timers. It advances a little
// past the delay (the 150ms the tests used to wait in real time) so the close
// fires with the same margin as before.
export async function waitForHoverCloseDelay(): Promise<void> {
  if (!vi.isFakeTimers()) {
    throw new Error(
      'waitForHoverCloseDelay() needs fake timers: call useHoverFakeTimers() in the describe or startHoverFakeTimers() at the top of the test, and create the user with setupHoverUser()'
    );
  }
  await act(async () => {
    await vi.advanceTimersByTimeAsync(CLOSE_DELAY_MS + 50);
  });
}

// Runs each test of the enclosing describe under fake timers, for use with
// waitForHoverOpenDelay() and setupHoverUser(). shouldAdvanceTime keeps the
// fake clock moving with real time too, so findBy*/waitFor, the fake
// backend's delayed replies and other real-time waits still make progress.
export function useHoverFakeTimers(): void {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });
}

// The same fake timers for the current test only, for a test that hovers
// among others in its describe that do not. Call it at the top of the test,
// before rendering; real timers come back when the test finishes.
export function startHoverFakeTimers(): void {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  onTestFinished(() => {
    vi.useRealTimers();
  });
}

// A user-event instance whose internal waits advance the fake clock, for
// tests run under fake timers from useHoverFakeTimers() or
// startHoverFakeTimers().
export function setupHoverUser(options: Parameters<typeof userEvent.setup>[0] = {}): ReturnType<typeof userEvent.setup> {
  return userEvent.setup({ advanceTimers: vi.advanceTimersByTime, ...options });
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

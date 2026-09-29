// Test helpers for IconButton's touch long press (DFLT-00307). jsdom has no
// PointerEvent, so fireEvent.pointerDown(el, { pointerType: 'touch' })
// would dispatch a plain Event without pointerType, isPrimary or clientX.
// These dispatch a MouseEvent (for the coordinates) carrying the pointer
// fields instead; React's synthetic pointer event reads them from it.
import { act, fireEvent } from '@testing-library/react';
import { vi } from 'vitest';
import { LONG_PRESS_MS } from '../components/IconButton';

export type PointerKind = 'touch' | 'mouse' | 'pen';

export interface PointerInit {
  pointerType?: PointerKind;
  isPrimary?: boolean;
  clientX?: number;
  clientY?: number;
  button?: number;
}

export function pointerEvent(type: string, { pointerType = 'touch', isPrimary = true, clientX = 0, clientY = 0, button = 0 }: PointerInit = {}): MouseEvent {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, composed: true, clientX, clientY, button });
  Object.defineProperties(event, {
    pointerType: { value: pointerType },
    isPrimary: { value: isPrimary },
    pointerId: { value: pointerType === 'mouse' ? 1 : isPrimary ? 2 : 3 }
  });
  return event;
}

export function firePointer(el: Element, type: 'pointerdown' | 'pointermove' | 'pointerup' | 'pointercancel', init?: PointerInit): boolean {
  let notCancelled = true;
  act(() => {
    notCancelled = el.dispatchEvent(pointerEvent(type, init));
  });
  return notCancelled;
}

// Advances fake timers inside act.
export function advance(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

// A touch held for the long-press time; the finger is still down.
export function touchHold(el: Element, init?: PointerInit) {
  firePointer(el, 'pointerdown', init);
  advance(LONG_PRESS_MS);
}

// A touch long press whose finger lifts; `click` says whether the browser
// then sends a click (some do, some do not).
export function touchLongPress(el: Element, { click = true }: { click?: boolean } = {}) {
  touchHold(el);
  firePointer(el, 'pointerup');
  if (click) fireEvent.click(el);
}

// A tap: pointerdown, pointerup well within the long-press time, click.
export function touchTap(el: Element) {
  firePointer(el, 'pointerdown');
  firePointer(el, 'pointerup');
  fireEvent.click(el);
}

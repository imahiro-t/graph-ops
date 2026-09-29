// DFLT-00307: IconButton's `longPressTooltip` -- a touch long press opens
// the visible tooltip without running the button, a tap still runs it at
// once, and the tooltip stays after the finger lifts until another
// pointerdown, Escape or LONG_PRESS_TOOLTIP_MS. jsdom has no PointerEvent;
// test/touchLongPress.ts dispatches pointer events with pointerType etc.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { IconButton, LONG_PRESS_CLICK_SUPPRESS_MS, LONG_PRESS_MS, LONG_PRESS_TOOLTIP_MS, type IconButtonProps } from './IconButton';
import { openIconButtonTooltip, openIconButtonTooltips } from '../test/iconButtonTooltip';
import { advance, firePointer, pointerEvent, touchHold, touchLongPress, touchTap } from '../test/touchLongPress';

const Icon = () => <svg aria-hidden="true" />;
const LONG_PRESS_CLASSES = ['select-none', '[-webkit-touch-callout:none]'];

type Props = Partial<IconButtonProps> & { onClick?: () => void };

function renderButton(props: Props = {}) {
  const onClick = props.onClick ?? vi.fn();
  const utils = render(
    <>
      <IconButton label="ラベル" longPressTooltip {...(props as object)} onClick={onClick}>
        <Icon />
      </IconButton>
      <button type="button">outside</button>
    </>
  );
  const button = screen.getByRole('button', { name: 'ラベル' });
  const rerender = (next: Props) =>
    utils.rerender(
      <>
        <IconButton label="ラベル" longPressTooltip {...(next as object)} onClick={onClick}>
          <Icon />
        </IconButton>
        <button type="button">outside</button>
      </>
    );
  return { button, onClick, rerender, unmount: utils.unmount, outside: screen.getByRole('button', { name: 'outside' }) };
}

function contextMenuCancelled(el: Element): boolean {
  let notCancelled = true;
  act(() => {
    notCancelled = fireEvent.contextMenu(el);
  });
  return !notCancelled;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('IconButton longPressTooltip: a touch long press opens the tooltip and does not run the button', () => {
  it('opens after 500ms, and the click after the finger lifts does not call onClick', () => {
    const { button, onClick } = renderButton();
    firePointer(button, 'pointerdown');
    advance(LONG_PRESS_MS - 1);
    expect(openIconButtonTooltips()).toHaveLength(0);
    advance(1);
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
    firePointer(button, 'pointerup');
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
  });

  it('a tap under 500ms calls onClick once and opens no tooltip', () => {
    const { button, onClick } = renderButton();
    firePointer(button, 'pointerdown');
    advance(300);
    firePointer(button, 'pointerup');
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(openIconButtonTooltips()).toHaveLength(0);
    advance(500);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('moving the finger more than 10px cancels it', () => {
    const { button } = renderButton();
    firePointer(button, 'pointerdown', { clientX: 20, clientY: 20 });
    firePointer(button, 'pointermove', { clientX: 31, clientY: 20 });
    advance(LONG_PRESS_MS);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('moving the finger 10px or less does not cancel it', () => {
    const { button } = renderButton();
    firePointer(button, 'pointerdown', { clientX: 20, clientY: 20 });
    firePointer(button, 'pointermove', { clientX: 23, clientY: 24 });
    advance(LONG_PRESS_MS);
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
  });

  it('a pointercancel cancels it, and a later tap runs the button', () => {
    const { button, onClick } = renderButton();
    firePointer(button, 'pointerdown');
    advance(200);
    firePointer(button, 'pointercancel');
    advance(500);
    expect(openIconButtonTooltips()).toHaveLength(0);
    touchTap(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('cancels the contextmenu raised after the long press', () => {
    const { button } = renderButton();
    touchHold(button);
    expect(contextMenuCancelled(button)).toBe(true);
  });

  it('cancels the contextmenu raised during the long press (before 500ms)', () => {
    const { button } = renderButton();
    firePointer(button, 'pointerdown');
    advance(300);
    expect(contextMenuCancelled(button)).toBe(true);
  });

  it('leaves a mouse right click contextmenu alone', () => {
    const { button } = renderButton();
    firePointer(button, 'pointerdown', { pointerType: 'mouse', button: 2 });
    expect(contextMenuCancelled(button)).toBe(false);
  });

  it('gives the button the classes that stop the touch callout and text selection', () => {
    const { button } = renderButton({ className: 'px-2' });
    expect(button).toHaveClass('px-2', ...LONG_PRESS_CLASSES);
  });
});

describe('IconButton longPressTooltip: only a primary touch, and only when opted in', () => {
  it.each([
    ['mouse', {}, { pointerType: 'mouse' as const }],
    ['pen', {}, { pointerType: 'pen' as const }],
    ['touch without longPressTooltip', { longPressTooltip: false }, {}],
    ['touch with tooltipDisabled', { tooltipDisabled: true }, {}],
    ['a second finger', {}, { isPrimary: false }]
  ])('does not open for %s', (_name, props, init) => {
    const { button } = renderButton(props);
    firePointer(button, 'pointerdown', init);
    advance(LONG_PRESS_MS);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('without longPressTooltip, the click after a touch long press calls onClick as before', () => {
    const { button, onClick } = renderButton({ longPressTooltip: false });
    touchLongPress(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('without longPressTooltip, the contextmenu of a touch long press is left alone', () => {
    const { button } = renderButton({ longPressTooltip: false });
    touchHold(button);
    expect(contextMenuCancelled(button)).toBe(false);
  });

  it('without longPressTooltip, the button has none of the long-press classes', () => {
    const { button } = renderButton({ longPressTooltip: false, className: 'px-2' });
    expect(button.className).toBe('px-2');
    for (const cls of LONG_PRESS_CLASSES) expect(button).not.toHaveClass(cls);
  });

  it('opens on a long press of the wrapper of a disabled pointer-events-none button', () => {
    const tooltip = (
      <>
        <span className="block">名前</span>
        <span className="block">無効理由</span>
      </>
    );
    const { button } = renderButton({ disabled: true, className: 'pointer-events-none', tooltip });
    touchHold(button.parentElement!);
    const open = openIconButtonTooltip();
    expect(open).toHaveTextContent('名前');
    expect(open).toHaveTextContent('無効理由');
  });
});

describe('IconButton longPressTooltip: the tooltip stays after the finger lifts and closes on its own terms', () => {
  it('stays for 7 seconds and closes at 8', () => {
    const { button } = renderButton();
    touchLongPress(button);
    advance(7000);
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
    advance(LONG_PRESS_TOOLTIP_MS - 7000);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('closes on a pointerdown elsewhere', () => {
    const { button, outside } = renderButton();
    touchLongPress(button);
    firePointer(outside, 'pointerdown');
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('closes on Escape', () => {
    const { button } = renderButton();
    touchLongPress(button);
    act(() => {
      fireEvent.keyDown(document.body, { key: 'Escape' });
    });
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('does not close on a pointerdown on the tooltip itself', () => {
    const { button } = renderButton();
    touchLongPress(button);
    firePointer(openIconButtonTooltip(), 'pointerdown');
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
  });

  it('a tap on the button closes it and runs the button', () => {
    const { button, onClick } = renderButton();
    touchLongPress(button);
    touchTap(button);
    expect(openIconButtonTooltips()).toHaveLength(0);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('closes after 8 seconds even when a compatibility mouseenter followed the long press', () => {
    const { button } = renderButton();
    touchLongPress(button);
    fireEvent.mouseEnter(button.parentElement!);
    advance(LONG_PRESS_TOOLTIP_MS);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('closes on a pointerdown elsewhere even when a compatibility mouseenter followed the long press', () => {
    const { button, outside } = renderButton();
    touchLongPress(button);
    fireEvent.mouseEnter(button.parentElement!);
    firePointer(outside, 'pointerdown');
    expect(openIconButtonTooltips()).toHaveLength(0);
  });
});

describe('IconButton longPressTooltip: only the one click right after a long press is swallowed', () => {
  function longPressWithoutClick() {
    const r = renderButton();
    touchLongPress(r.button, { click: false });
    return r;
  }

  it('a following tap runs the button', () => {
    const { button, onClick } = longPressWithoutClick();
    touchTap(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('a following Enter runs the button', () => {
    const { button, onClick } = longPressWithoutClick();
    fireEvent.keyDown(button, { key: 'Enter' });
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('a following mouse click runs the button', () => {
    const { button, onClick } = longPressWithoutClick();
    firePointer(button, 'pointerdown', { pointerType: 'mouse' });
    firePointer(button, 'pointerup', { pointerType: 'mouse' });
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('a click arriving 500ms or more after the finger lifts runs the button', () => {
    const { button, onClick } = longPressWithoutClick();
    advance(LONG_PRESS_CLICK_SUPPRESS_MS);
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});

describe('IconButton longPressTooltip: nothing carries over when the option turns off', () => {
  it('closes an open long-press tooltip, and a later touch click runs the button', () => {
    const { button, onClick, rerender } = renderButton();
    touchLongPress(button, { click: false });
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
    rerender({ longPressTooltip: false });
    expect(openIconButtonTooltips()).toHaveLength(0);
    touchTap(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('turning off while the click is still awaited lets that click through', () => {
    const { button, onClick, rerender } = renderButton();
    touchHold(button);
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
    rerender({ longPressTooltip: false });
    expect(openIconButtonTooltips()).toHaveLength(0);
    firePointer(button, 'pointerup');
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('turning off during a press stops it from opening', () => {
    const { button, onClick, rerender } = renderButton();
    firePointer(button, 'pointerdown');
    advance(200);
    rerender({ longPressTooltip: false });
    advance(500);
    expect(openIconButtonTooltips()).toHaveLength(0);
    firePointer(button, 'pointerup');
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('leaves no timer behind and does not reopen after 8 seconds', () => {
    const { button, rerender } = renderButton();
    touchLongPress(button);
    rerender({ longPressTooltip: false });
    expect(vi.getTimerCount()).toBe(0);
    advance(LONG_PRESS_TOOLTIP_MS);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('leaves no timer behind on unmount', () => {
    const { button, unmount } = renderButton();
    touchLongPress(button, { click: false });
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('IconButton longPressTooltip: hover and focus behave as before', () => {
  it('a hover tooltip is not closed by the 8-second timer and closes when the pointer leaves', () => {
    const { button } = renderButton();
    fireEvent.mouseEnter(button.parentElement!);
    advance(LONG_PRESS_TOOLTIP_MS);
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
    fireEvent.mouseLeave(button.parentElement!);
    advance(100);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('a keyboard focus tooltip is not closed by the 8-second timer', () => {
    const { button } = renderButton();
    act(() => {
      button.focus();
    });
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
    advance(LONG_PRESS_TOOLTIP_MS);
    expect(openIconButtonTooltip()).toHaveTextContent('ラベル');
  });
});

// Keeps the helper honest: pointerType and the rest reach the handlers.
it('dispatches pointer events carrying pointerType, isPrimary and coordinates', () => {
  const el = document.createElement('div');
  const event = pointerEvent('pointerdown', { pointerType: 'pen', isPrimary: false, clientX: 3, clientY: 4 }) as MouseEvent & PointerEvent;
  el.dispatchEvent(event);
  expect([event.pointerType, event.isPrimary, event.clientX, event.clientY]).toEqual(['pen', false, 3, 4]);
});

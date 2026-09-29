// An icon-only button with a visible tooltip (DFLT-00171), in place of the
// native title attribute the app's icon buttons used to rely on. A title
// tooltip only appears on mouse hover -- never on keyboard focus or touch --
// and how assistive technologies treat a title-only name varies, so:
//
// - The accessible name always comes from `label`, as aria-label. The props
//   type refuses `title` and `aria-label` so a duplicate name cannot creep
//   back in next to it.
// - A visible tooltip (`tooltip`, defaulting to `label`) appears on mouse
//   hover and on keyboard focus (:focus-visible only, so a mouse click does
//   not leave one behind). Hover and focus are tracked separately and the
//   tooltip stays open while either holds, so a pointer passing over a
//   keyboard-focused button does not take its tooltip away, nor does Tab
//   moving focus off a hovered one. Escape dismisses it (see below).
// - The tooltip element is aria-hidden: its text is either the name itself
//   or already part of it, so reading it again would only repeat the name.
//   The one exception is `describeWithTooltip`, for a tooltip that says
//   something the name does not (e.g. why a default entry cannot be
//   deleted): the button then points aria-describedby at the tooltip
//   element, which keeps it rendered (hidden) while closed so the reference
//   always resolves. A referenced element counts for the description even
//   when it is hidden.
//
// - A button that is unavailable but still has to explain why (a default
//   entry's delete button, say) takes aria-disabled="true" rather than the
//   native disabled: a disabled button cannot be focused, so its tooltip
//   and description could only ever be reached by mouse hover (DFLT-00203).
//   With aria-disabled the button stays in the Tab order and opens its
//   tooltip on keyboard focus like any other, while a click -- including
//   the click that Enter and Space turn into -- is swallowed here
//   (preventDefault, and onClick is not called). The caller still styles
//   the unavailable state itself (Tailwind's disabled: variant does not
//   match aria-disabled) and should guard its handler as well.
//
// Layout and behaviour details:
//
// - Hover is tracked on a wrapping <span>, not on the button, because some
//   browsers deliver no mouse events to a natively disabled button, and a
//   caller may still use the native disabled where no reason needs to be
//   reachable by keyboard. Put classes that position the button within its parent's flex layout
//   (ml-auto, shrink-0, negative margins, ...) on `wrapperClassName`.
// - The tooltip is rendered into document.body through a portal with fixed
//   positioning, so a scrolling list or an overflow-hidden card cannot clip
//   it. Its position is measured after it is laid out invisibly
//   (visibility: hidden, not display: none, which would measure 0x0), then
//   revealed; it follows scrolling and resizing while open and is kept
//   inside the viewport, flipping to the other side of the button when the
//   preferred side (`tooltipSide`) has no room.
// - React events bubble through the React tree even out of a portal, so
//   the tooltip stops click / mousedown / pointerdown / mouseup from
//   reaching the component that rendered the button (a ticket row's header,
//   for instance, toggles on click).
// - The tooltip itself can be hovered (WCAG 1.4.13): leaving the button or
//   the tooltip only schedules the close, and entering either cancels it.
// - Escape dismisses an open tooltip wherever focus is (WCAG 1.4.13,
//   dismissible), until the pointer leaves and focus leaves the button;
//   an Escape pressed while an input method is composing text is ignored.
//   - With focus inside the button, the press also calls preventDefault --
//     without stopPropagation -- so useModalDialog, which ignores a
//     defaultPrevented key, leaves a surrounding modal open for that press.
//   - With focus elsewhere (the tooltip was opened by hover alone, which is
//     the only way to open one on a natively disabled button), a document
//     listener registered only while the tooltip is open closes it without
//     preventDefault, so the press still does whatever it does where focus
//     is -- cancelling an input's edit, closing a modal. It listens in the
//     capture phase so a handler that stops propagation cannot keep the
//     tooltip from being dismissed.
//   With the tooltip closed, Escape is left alone as before.
//
// - `busy` marks the button as sending the user's own action (DFLT-00206,
//   see Submitting.tsx): aria-busy="true" and the shared "(submitting)"
//   suffix joined to the aria-label. The visible tooltip keeps the plain
//   label (or `tooltip`), so nothing on screen changes.
//
// DFLT-00285 options, for a button that shows its own name as text (the
// header's project switcher) rather than an icon alone:
//
// - `nameFromContent`: no aria-label; the accessible name comes from the
//   button's content, like any text button. `label` is then optional and
//   only serves as the default tooltip text.
// - DFLT-00293: with `nameFromContent`, and only then, the button also
//   takes `title`, passed through to the <button> as is. The name comes
//   from the content, so a title can never become the name (the duplicate
//   name the type otherwise guards against), and a caller that already
//   shows a native title tooltip (the header's New Ticket button, disabled
//   with no project, where it explains why) keeps it where the visible
//   tooltip is disabled. Such a caller should point aria-describedby at
//   its own description: when it is set, accessible name computation takes
//   the description from it and not from the title, so the description
//   stays the same whether a title is set or not.
// - `tooltip` may be any node, so a tooltip can have more than one line
//   (block-level spans inside it). Pass a stable reference (useMemo) when
//   it is an element: the positioning effect re-runs whenever the content
//   changes, so a fresh element on every render of the caller would
//   re-measure and re-subscribe each time.
// - Every tooltip is at most 20rem wide and never wider than the viewport
//   less the 4px margin on each side, and breaks anywhere (overflow-wrap:
//   anywhere, which, unlike break-word, also lowers its min-content width),
//   so a long unbroken name or path in a narrow window wraps inside the
//   viewport instead of widening the tooltip past it. The cap is a % of the
//   fixed element's containing block, the viewport without a classic
//   scrollbar, rather than 100vw, which includes one.
// - `tooltipDisabled`: the tooltip does not open, and `open` itself stays
//   false, so no positioning, no portal and no Escape handling happen either
//   -- a tooltip hidden only by looks would still preventDefault an Escape
//   pressed on the focused button and keep, say, a popup the button opened
//   from closing on that press. Hover and focus are still tracked, so when
//   it goes back to false while the pointer is over the button or the
//   button has keyboard focus, the tooltip opens again, as it would on any
//   other IconButton that the pointer or focus comes back to.
//
// DFLT-00307: `longPressTooltip`, for a button whose label is hidden and
// whose tooltip is the only place to read it (the header's buttons in a
// window of 200px or less, DFLT-00293). A touch never hovers and never
// moves keyboard focus, so there the tooltip also opens on a touch long
// press. Off by default: without it nothing below applies and the button
// behaves exactly as before.
//
// - A primary touch pointer held for LONG_PRESS_MS without moving more than
//   LONG_PRESS_MOVE_TOLERANCE_PX opens the tooltip. Moving further (a
//   scroll), lifting the finger earlier (a tap) or a pointercancel (the
//   browser taking over a pan) cancels it; a tap's click goes through at
//   once, as before. Mouse and pen presses are left alone. A tooltip that
//   is disabled, or has nothing to show, does not start a long press at
//   all, so the press stays an ordinary press.
// - The press does not run the button's action: the click the browser may
//   send after a long press is swallowed in the capture phase. Only that
//   one click, though -- some browsers send none, and a flag left behind
//   would swallow the next genuine click instead. So the flag is cleared
//   when it has swallowed one, on every pointerdown of any pointer type (a
//   tap, or a mouse click on a device with both), on any keydown in the
//   wrapper (Enter and Space send a click with no pointerdown), and
//   LONG_PRESS_CLICK_SUPPRESS_MS after the finger lifts. A browser that
//   sends its click later than that runs the action -- the safe way to
//   fail, as no later action is ever lost.
// - While a long press is under way or its tooltip is open, the contextmenu
//   it raises (Android) is cancelled, and the button takes select-none and
//   -webkit-touch-callout: none (iOS's callout and text selection). Both
//   only with the option, so nothing changes where it is off.
// - The tooltip stays open after the finger lifts, and closes on a
//   pointerdown anywhere but on the tooltip itself (a tap on the button
//   closes it and then acts as a new press, running its action), on Escape
//   (as any open tooltip), and LONG_PRESS_TOOLTIP_MS after the finger
//   lifts. Closing it also clears the hover state: browsers may send a
//   compatibility mouseenter after a long press, and the tooltip would
//   otherwise stay open on that hover. Focus is left alone. A tooltip
//   opened by hover or focus alone is never closed by the timer.
// - Two-tap schemes (the first tap shows the tooltip) and short visible
//   labels were not used: the first changes what a tap does, the second
//   breaks words again at 160px with 200% text (the ticket's decision).
// - When the option turns off (the window grows past 200px) every timer
//   is cleared and the long-press state, its tooltip and the click flag are
//   dropped, so nothing carries over to the wider layout.
import React, { forwardRef, useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { submittingProps, useSubmittingLabel } from './Submitting';

const CLOSE_DELAY_MS = 100;
const VIEWPORT_MARGIN = 4;
const GAP = 6;
// DFLT-00307: the touch long press (see the header comment).
export const LONG_PRESS_MS = 500;
export const LONG_PRESS_MOVE_TOLERANCE_PX = 10;
export const LONG_PRESS_TOOLTIP_MS = 8000;
export const LONG_PRESS_CLICK_SUPPRESS_MS = 500;
const LONG_PRESS_BUTTON_CLASSES = 'select-none [-webkit-touch-callout:none]';

type Timer = ReturnType<typeof setTimeout>;

function clearTimer(ref: React.MutableRefObject<Timer | null>) {
  if (ref.current !== null) {
    clearTimeout(ref.current);
    ref.current = null;
  }
}

type ButtonProps = Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, 'title' | 'aria-label'>;

// Either the accessible name is `label` (as aria-label), or, with
// nameFromContent, the button's own content names it.
// A title only with nameFromContent (see the header comment).
type NameProps = { label: string; nameFromContent?: false; title?: never } | { label?: string; nameFromContent: true; title?: string };

export type IconButtonProps = ButtonProps & NameProps & {
  // The visible tooltip; defaults to `label`.
  tooltip?: React.ReactNode;
  // Keep the tooltip closed (see the header comment).
  tooltipDisabled?: boolean;
  // Expose the tooltip as the button's description (aria-describedby). Only
  // for a tooltip carrying information the name does not.
  describeWithTooltip?: boolean;
  tooltipSide?: 'top' | 'bottom';
  wrapperClassName?: string;
  // The user's own action is being sent (the button shows a spinner):
  // adds aria-busy and the "(submitting)" suffix to the accessible name.
  busy?: boolean;
  // DFLT-00307: also open the tooltip on a touch long press (see the header
  // comment). Off by default.
  longPressTooltip?: boolean;
};

interface Position {
  left: number;
  top: number;
}

const stopPropagation = (e: React.SyntheticEvent) => e.stopPropagation();

function isFocusVisible(el: Element): boolean {
  try {
    return el.matches(':focus-visible');
  } catch {
    // An environment that cannot evaluate :focus-visible errs on the side
    // of showing the tooltip.
    return true;
  }
}

// Escape, unless an input method is composing text (keyCode 229 covers
// browsers that end the composition before reporting the key).
function isDismissKey(e: KeyboardEvent): boolean {
  return e.key === 'Escape' && !e.isComposing && e.keyCode !== 229;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  {
    label,
    // No default: one would stop nameFromContent from narrowing NameProps, leaving label possibly undefined.
    nameFromContent,
    tooltip,
    tooltipDisabled = false,
    describeWithTooltip = false,
    tooltipSide = 'bottom',
    wrapperClassName,
    busy = false,
    longPressTooltip = false,
    type,
    'aria-describedby': ariaDescribedBy,
    onClick,
    children,
    ...buttonProps
  },
  forwardedRef
) {
  const tooltipContent = tooltip ?? label;
  const submittingLabel = useSubmittingLabel();
  const tooltipId = useId();
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  // Opened by a touch long press (DFLT-00307).
  const [pressed, setPressed] = useState(false);
  const canOpen = !tooltipDisabled && tooltipContent != null;
  const open = canOpen && (hovered || focused || pressed);
  const [position, setPosition] = useState<Position | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const wrapperRef = useRef<HTMLSpanElement | null>(null);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const setButtonRef = useCallback(
    (node: HTMLButtonElement | null) => {
      buttonRef.current = node;
      if (typeof forwardedRef === 'function') forwardedRef(node);
      else if (forwardedRef) forwardedRef.current = node;
    },
    [forwardedRef]
  );

  const cancelClose = useCallback(() => {
    if (closeTimerRef.current !== null) {
      clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  }, []);

  const startHover = useCallback(() => {
    cancelClose();
    setHovered(true);
  }, [cancelClose]);

  const scheduleHoverEnd = useCallback(() => {
    cancelClose();
    closeTimerRef.current = setTimeout(() => {
      closeTimerRef.current = null;
      setHovered(false);
    }, CLOSE_DELAY_MS);
  }, [cancelClose]);

  // DFLT-00307: the touch long press. Its progress lives in refs so that it
  // never waits for a render.
  const longPressTimerRef = useRef<Timer | null>(null);
  const autoCloseTimerRef = useRef<Timer | null>(null);
  const suppressResetTimerRef = useRef<Timer | null>(null);
  const pressStartRef = useRef<{ x: number; y: number } | null>(null);
  // The current touch gesture has become a long press.
  const longPressFiredRef = useRef(false);
  // Swallow the one click that follows a long press.
  const suppressClickRef = useRef(false);
  // Read by the long-press timer, which fires after the render it was set in.
  const canOpenRef = useRef(canOpen);
  canOpenRef.current = canOpen;

  const clearSuppressClick = useCallback(() => {
    clearTimer(suppressResetTimerRef);
    suppressClickRef.current = false;
  }, []);

  const cancelPress = useCallback(() => {
    clearTimer(longPressTimerRef);
    pressStartRef.current = null;
  }, []);

  // Close a tooltip the long press opened: the timer, another pointerdown,
  // the option turning off. Hover goes too (see the header comment).
  const endLongPress = useCallback(() => {
    clearTimer(autoCloseTimerRef);
    setPressed(false);
    cancelClose();
    setHovered(false);
  }, [cancelClose]);

  const startAutoClose = useCallback(() => {
    clearTimer(autoCloseTimerRef);
    autoCloseTimerRef.current = setTimeout(() => {
      autoCloseTimerRef.current = null;
      endLongPress();
    }, LONG_PRESS_TOOLTIP_MS);
  }, [endLongPress]);

  // Escape: close for both hover and focus, until each of them starts again
  // (and a long-press tooltip, DFLT-00307).
  const dismiss = useCallback(() => {
    cancelClose();
    clearTimer(autoCloseTimerRef);
    setHovered(false);
    setFocused(false);
    setPressed(false);
  }, [cancelClose]);

  useEffect(() => cancelClose, [cancelClose]);

  // The option turning off (the window growing past 200px), and unmounting:
  // nothing of a long press carries over.
  const pressedRef = useRef(pressed);
  pressedRef.current = pressed;
  useEffect(() => {
    if (!longPressTooltip) return;
    return () => {
      cancelPress();
      clearSuppressClick();
      longPressFiredRef.current = false;
      clearTimer(autoCloseTimerRef);
      if (pressedRef.current) endLongPress();
    };
  }, [longPressTooltip, cancelPress, clearSuppressClick, endLongPress]);

  // A pointerdown anywhere but on the tooltip closes a long-press tooltip.
  // In the capture phase, so a handler that stops propagation cannot keep
  // it open; the tooltip's own stopPropagation is why this checks contains.
  useEffect(() => {
    if (!pressed) return;
    const onDocumentPointerDown = (e: Event) => {
      if (e.target instanceof Node && tooltipRef.current?.contains(e.target)) return;
      endLongPress();
    };
    document.addEventListener('pointerdown', onDocumentPointerDown, true);
    return () => document.removeEventListener('pointerdown', onDocumentPointerDown, true);
  }, [pressed, endLongPress]);

  const handlePointerDown = (e: React.PointerEvent<HTMLSpanElement>) => {
    if (!longPressTooltip) return;
    // Any new press, of any pointer type, ends the wait for a click after
    // an earlier long press.
    clearSuppressClick();
    if (e.pointerType !== 'touch') return;
    // A second finger (a pinch) is not a long press, and cancels one.
    cancelPress();
    longPressFiredRef.current = false;
    if (!e.isPrimary || !canOpen) return;
    pressStartRef.current = { x: e.clientX, y: e.clientY };
    longPressTimerRef.current = setTimeout(() => {
      longPressTimerRef.current = null;
      if (!canOpenRef.current) return;
      longPressFiredRef.current = true;
      suppressClickRef.current = true;
      setPressed(true);
      startAutoClose();
    }, LONG_PRESS_MS);
  };

  const handlePointerMove = (e: React.PointerEvent<HTMLSpanElement>) => {
    const start = pressStartRef.current;
    if (!longPressTooltip || longPressTimerRef.current === null || !start || e.pointerType !== 'touch' || !e.isPrimary) return;
    if (Math.hypot(e.clientX - start.x, e.clientY - start.y) > LONG_PRESS_MOVE_TOLERANCE_PX) cancelPress();
  };

  const handlePointerEnd = (e: React.PointerEvent<HTMLSpanElement>) => {
    if (!longPressTooltip || e.pointerType !== 'touch' || !e.isPrimary) return;
    // Lifted before the long press: a tap, whose click goes through.
    cancelPress();
    if (!longPressFiredRef.current) return;
    longPressFiredRef.current = false;
    // The click after a long press comes right after the finger lifts;
    // stop waiting for it soon after.
    clearTimer(suppressResetTimerRef);
    suppressResetTimerRef.current = setTimeout(() => {
      suppressResetTimerRef.current = null;
      suppressClickRef.current = false;
    }, LONG_PRESS_CLICK_SUPPRESS_MS);
    // The tooltip stays; its time starts when the finger lifts.
    if (pressedRef.current) startAutoClose();
  };

  const handleClickCapture = (e: React.MouseEvent<HTMLSpanElement>) => {
    if (!suppressClickRef.current) return;
    clearSuppressClick();
    e.preventDefault();
    e.stopPropagation();
  };

  const handleContextMenu = (e: React.MouseEvent<HTMLSpanElement>) => {
    if (!longPressTooltip) return;
    if (longPressTimerRef.current !== null || longPressFiredRef.current || pressed) e.preventDefault();
  };

  const updatePosition = useCallback(() => {
    const button = buttonRef.current;
    const tip = tooltipRef.current;
    if (!button || !tip) return;
    const anchor = button.getBoundingClientRect();
    const { width, height } = tip.getBoundingClientRect();
    const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
    const viewportHeight = document.documentElement.clientHeight || window.innerHeight;

    let left = anchor.left + anchor.width / 2 - width / 2;
    left = Math.min(left, viewportWidth - VIEWPORT_MARGIN - width);
    left = Math.max(left, VIEWPORT_MARGIN);

    const below = anchor.bottom + GAP;
    const above = anchor.top - GAP - height;
    const fitsBelow = below + height <= viewportHeight - VIEWPORT_MARGIN;
    const fitsAbove = above >= VIEWPORT_MARGIN;
    let top: number;
    if (tooltipSide === 'top') top = fitsAbove || !fitsBelow ? above : below;
    else top = fitsBelow || !fitsAbove ? below : above;

    setPosition(prev => (prev && prev.left === left && prev.top === top ? prev : { left, top }));
  }, [tooltipSide]);

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    updatePosition();
    window.addEventListener('scroll', updatePosition, true);
    window.addEventListener('resize', updatePosition);
    return () => {
      window.removeEventListener('scroll', updatePosition, true);
      window.removeEventListener('resize', updatePosition);
    };
  }, [open, tooltipContent, updatePosition]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLSpanElement>) => {
    if (!open || !isDismissKey(e.nativeEvent)) return;
    e.preventDefault();
    dismiss();
  };

  // Escape pressed with focus outside the button (a tooltip opened by hover
  // alone). A press inside the button is handleKeyDown's.
  useEffect(() => {
    if (!open) return;
    const onDocumentKeyDown = (e: KeyboardEvent) => {
      if (!isDismissKey(e)) return;
      if (e.target instanceof Node && wrapperRef.current?.contains(e.target)) return;
      dismiss();
    };
    document.addEventListener('keydown', onDocumentKeyDown, true);
    return () => document.removeEventListener('keydown', onDocumentKeyDown, true);
  }, [open, dismiss]);

  const ariaDisabled = buttonProps['aria-disabled'] === true || buttonProps['aria-disabled'] === 'true';
  const handleClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    if (ariaDisabled) {
      e.preventDefault();
      return;
    }
    onClick?.(e);
  };

  const describedBy = [ariaDescribedBy, describeWithTooltip ? tooltipId : undefined].filter(Boolean).join(' ') || undefined;
  const renderTooltip = open || describeWithTooltip;

  return (
    <span
      ref={wrapperRef}
      className={`inline-flex ${wrapperClassName ?? ''}`}
      onMouseEnter={startHover}
      onMouseLeave={scheduleHoverEnd}
      onFocus={e => {
        if (isFocusVisible(e.target)) setFocused(true);
      }}
      onBlur={() => setFocused(false)}
      onKeyDown={handleKeyDown}
      onKeyDownCapture={longPressTooltip ? clearSuppressClick : undefined}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerEnd}
      onPointerCancel={handlePointerEnd}
      onClickCapture={handleClickCapture}
      onContextMenu={handleContextMenu}
    >
      <button
        ref={setButtonRef}
        type={type ?? 'button'}
        aria-label={nameFromContent ? undefined : submittingLabel(label, busy)}
        aria-describedby={describedBy}
        {...submittingProps(busy)}
        {...buttonProps}
        className={longPressTooltip ? `${buttonProps.className ?? ''} ${LONG_PRESS_BUTTON_CLASSES}`.trim() : buttonProps.className}
        onClick={handleClick}
      >
        {children}
      </button>
      {/* The tooltip's text size is in rem (0.6875rem = 11px at a 16px root,
          DFLT-00294) so it follows the root and browser font size. The
          header relies on that: in a window of 200px or less the tooltip
          stands in for labels the header hides, and a 200% text size must
          enlarge it as it did those labels (DFLT-00293). */}
      {renderTooltip &&
        createPortal(
          <span
            ref={tooltipRef}
            id={tooltipId}
            aria-hidden="true"
            hidden={!open}
            data-icon-button-tooltip=""
            className="fixed z-60 max-w-[min(20rem,calc(100%-8px))] rounded-sm px-2 py-1 text-[0.6875rem] font-medium leading-snug shadow-xs bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900 whitespace-normal wrap-anywhere text-left"
            style={{
              left: position?.left ?? 0,
              top: position?.top ?? 0,
              visibility: position ? 'visible' : 'hidden'
            }}
            onMouseEnter={startHover}
            onMouseLeave={scheduleHoverEnd}
            onClick={stopPropagation}
            onMouseDown={stopPropagation}
            onMouseUp={stopPropagation}
            onPointerDown={stopPropagation}
          >
            {tooltipContent}
          </span>,
          document.body
        )}
    </span>
  );
});

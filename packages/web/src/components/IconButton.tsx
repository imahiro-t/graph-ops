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
// - Hover opens the tooltip only after the pointer has rested on the button
//   for OPEN_DELAY_MS (DFLT-00322). A pointer that merely crosses the button
//   on its way elsewhere -- from the language button to "New ticket", say --
//   never opens it, so it never gets to cover the neighbour it was heading
//   for (at some header widths the tooltip lands right on top of it, and a
//   pointer stopping on the tooltip would keep it open by the rule above).
//   Moving from the button onto an open tooltip, and keyboard focus, open
//   (or keep open) without delay.
// - A pointerdown on the tooltip ends its hover-opened state (DFLT-00322),
//   so a tooltip that does end up covering something can be pushed away
//   with one press. It does not close a focus-opened tooltip, and the press
//   still stops at the tooltip (see above), so it never reaches what the
//   tooltip covered.
// - Touch never opens the hover tooltip (DFLT-00322). After a tap the
//   browser sends compatibility mouseover / mouseenter events, and a finger
//   never "leaves", so a tooltip opened by them would stay. The pointerType
//   of the latest pointer event on the wrapper or the tooltip is kept, and a
//   mouseenter while it is "touch" is ignored. No time window is involved:
//   the compatibility events come after pointerup, which a long press can
//   put well after pointerdown. A mouse or pen overwrites the record with
//   its own pointerover / pointermove, which arrive before the mouse events
//   of the same movement. A touch pointerdown also ends a hover-opened
//   state. Focus is not affected: a :focus-visible focus still opens it.
// - The tooltip is measured at the viewport's top-left corner (DFLT-00322).
//   Measured where it sits, a tooltip that a narrowing window has left near
//   the right edge shrinks to a narrow, tall column, and the position
//   worked out from that size put it off the button after a resize.
// - While open, a resize re-measures at once and once more in the next
//   animation frame (DFLT-00322), in case the layout the resize causes
//   (a header that wraps differently, say) is not settled when the resize
//   event is dispatched. Consecutive resizes share one pending frame.
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
import React, { forwardRef, useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { submittingProps, useSubmittingLabel } from './Submitting';

const CLOSE_DELAY_MS = 100;
// How long the pointer has to rest on the button before hover opens the
// tooltip (DFLT-00322, see the header comment).
export const OPEN_DELAY_MS = 300;
const VIEWPORT_MARGIN = 4;
const GAP = 6;

type ButtonProps = Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, 'title' | 'aria-label'>;

// Either the accessible name is `label` (as aria-label), or, with
// nameFromContent, the button's own content names it.
type NameProps = { label: string; nameFromContent?: false } | { label?: string; nameFromContent: true };

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
  const open = !tooltipDisabled && tooltipContent != null && (hovered || focused);
  const [position, setPosition] = useState<Position | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const wrapperRef = useRef<HTMLSpanElement | null>(null);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const openTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Mirrors `hovered` for the event handlers, which must not wait for a
  // re-render to see it.
  const hoveredRef = useRef(false);
  // The pointerType of the latest pointer event on the wrapper or the
  // tooltip (see the header comment on touch).
  const lastPointerTypeRef = useRef<string | null>(null);

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

  const cancelOpen = useCallback(() => {
    if (openTimerRef.current !== null) {
      clearTimeout(openTimerRef.current);
      openTimerRef.current = null;
    }
  }, []);

  const setHover = useCallback((value: boolean) => {
    hoveredRef.current = value;
    setHovered(value);
  }, []);

  // A mouseenter on the button (after OPEN_DELAY_MS) or on the tooltip
  // (`immediate`: it is already on screen). Ignored right after touch.
  const startHover = useCallback(
    (immediate: boolean) => {
      if (lastPointerTypeRef.current === 'touch') return;
      cancelClose();
      if (hoveredRef.current) return;
      if (immediate) {
        cancelOpen();
        setHover(true);
        return;
      }
      if (openTimerRef.current !== null) return;
      openTimerRef.current = setTimeout(() => {
        openTimerRef.current = null;
        setHover(true);
      }, OPEN_DELAY_MS);
    },
    [cancelClose, cancelOpen, setHover]
  );

  const scheduleHoverEnd = useCallback(() => {
    cancelOpen();
    cancelClose();
    if (!hoveredRef.current) return;
    closeTimerRef.current = setTimeout(() => {
      closeTimerRef.current = null;
      setHover(false);
    }, CLOSE_DELAY_MS);
  }, [cancelClose, cancelOpen, setHover]);

  // Ends the hover-opened state at once (a touch, or a press on the tooltip).
  const endHover = useCallback(() => {
    cancelOpen();
    cancelClose();
    setHover(false);
  }, [cancelClose, cancelOpen, setHover]);

  // Escape: close for both hover and focus, until each of them starts again.
  const dismiss = useCallback(() => {
    endHover();
    setFocused(false);
  }, [endHover]);

  useEffect(
    () => () => {
      cancelClose();
      cancelOpen();
    },
    [cancelClose, cancelOpen]
  );

  const recordPointer = (e: React.PointerEvent) => {
    lastPointerTypeRef.current = e.pointerType || null;
  };

  const handleWrapperPointerDown = (e: React.PointerEvent) => {
    recordPointer(e);
    if (e.pointerType === 'touch') endHover();
  };

  const handleTooltipPointerDown = (e: React.PointerEvent) => {
    // Keep the press from reaching the button's ancestors (see the header
    // comment), then end the hover-opened state.
    e.stopPropagation();
    recordPointer(e);
    endHover();
  };

  const updatePosition = useCallback(() => {
    const button = buttonRef.current;
    const tip = tooltipRef.current;
    if (!button || !tip) return;
    const anchor = button.getBoundingClientRect();
    // Measure at the viewport's top-left corner, then put the tooltip back.
    // A fixed element's shrink-to-fit width is limited by the room between
    // its left edge and the viewport's right edge, so measured where it
    // was placed for a wider window, it comes out a narrow, tall column,
    // and a position worked out from that size is off once the tooltip
    // widens again (DFLT-00322).
    const placedLeft = tip.style.left;
    const placedTop = tip.style.top;
    tip.style.left = '0px';
    tip.style.top = '0px';
    const { width, height } = tip.getBoundingClientRect();
    tip.style.left = placedLeft;
    tip.style.top = placedTop;
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
    let frame: number | null = null;
    const onResize = () => {
      updatePosition();
      if (frame !== null) cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        frame = null;
        updatePosition();
      });
    };
    window.addEventListener('scroll', updatePosition, true);
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('scroll', updatePosition, true);
      window.removeEventListener('resize', onResize);
      if (frame !== null) cancelAnimationFrame(frame);
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
      onMouseEnter={() => startHover(false)}
      onMouseLeave={scheduleHoverEnd}
      onPointerOver={recordPointer}
      onPointerEnter={recordPointer}
      onPointerMove={recordPointer}
      onPointerDown={handleWrapperPointerDown}
      onPointerUp={recordPointer}
      onPointerCancel={recordPointer}
      onPointerLeave={recordPointer}
      onFocus={e => {
        if (isFocusVisible(e.target)) setFocused(true);
      }}
      onBlur={() => setFocused(false)}
      onKeyDown={handleKeyDown}
    >
      <button
        ref={setButtonRef}
        type={type ?? 'button'}
        aria-label={nameFromContent ? undefined : submittingLabel(label, busy)}
        aria-describedby={describedBy}
        {...submittingProps(busy)}
        {...buttonProps}
        onClick={handleClick}
      >
        {children}
      </button>
      {/* The tooltip's text size is in rem (0.6875rem = 11px at a 16px root,
          DFLT-00294 / DFLT-00293) so it follows the root and browser font
          size: a 200% text size enlarges it as it does the rest of the
          page. */}
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
            onMouseEnter={() => startHover(true)}
            onMouseLeave={scheduleHoverEnd}
            onPointerOver={recordPointer}
            onPointerEnter={recordPointer}
            onPointerMove={recordPointer}
            onPointerUp={recordPointer}
            onPointerCancel={recordPointer}
            onPointerLeave={recordPointer}
            onClick={stopPropagation}
            onMouseDown={stopPropagation}
            onMouseUp={stopPropagation}
            onPointerDown={handleTooltipPointerDown}
          >
            {tooltipContent}
          </span>,
          document.body
        )}
    </span>
  );
});

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
import React, { forwardRef, useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { submittingProps, useSubmittingLabel } from './Submitting';

const CLOSE_DELAY_MS = 100;
const VIEWPORT_MARGIN = 4;
const GAP = 6;

type ButtonProps = Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, 'title' | 'aria-label'>;

export interface IconButtonProps extends ButtonProps {
  // The accessible name (aria-label).
  label: string;
  // The visible tooltip text; defaults to `label`.
  tooltip?: string;
  // Expose the tooltip as the button's description (aria-describedby). Only
  // for a tooltip carrying information the name does not.
  describeWithTooltip?: boolean;
  tooltipSide?: 'top' | 'bottom';
  wrapperClassName?: string;
  // The user's own action is being sent (the button shows a spinner):
  // adds aria-busy and the "(submitting)" suffix to the accessible name.
  busy?: boolean;
}

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
    tooltip,
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
  const tooltipText = tooltip ?? label;
  const submittingLabel = useSubmittingLabel();
  const tooltipId = useId();
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const open = hovered || focused;
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

  // Escape: close for both hover and focus, until each of them starts again.
  const dismiss = useCallback(() => {
    cancelClose();
    setHovered(false);
    setFocused(false);
  }, [cancelClose]);

  useEffect(() => cancelClose, [cancelClose]);

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
  }, [open, tooltipText, updatePosition]);

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
      className={`inline-flex${wrapperClassName ? ` ${wrapperClassName}` : ''}`}
      onMouseEnter={startHover}
      onMouseLeave={scheduleHoverEnd}
      onFocus={e => {
        if (isFocusVisible(e.target)) setFocused(true);
      }}
      onBlur={() => setFocused(false)}
      onKeyDown={handleKeyDown}
    >
      <button
        ref={setButtonRef}
        type={type ?? 'button'}
        aria-label={submittingLabel(label, busy)}
        aria-describedby={describedBy}
        {...submittingProps(busy)}
        {...buttonProps}
        onClick={handleClick}
      >
        {children}
      </button>
      {renderTooltip &&
        createPortal(
          <span
            ref={tooltipRef}
            id={tooltipId}
            aria-hidden="true"
            hidden={!open}
            data-icon-button-tooltip=""
            className="fixed z-[60] max-w-xs rounded px-2 py-1 text-[11px] font-medium leading-snug shadow-sm bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900 whitespace-normal break-words text-left"
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
            {tooltipText}
          </span>,
          document.body
        )}
    </span>
  );
});

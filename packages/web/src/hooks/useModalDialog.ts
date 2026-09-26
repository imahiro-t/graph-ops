import { RefObject, useEffect, useRef } from 'react';
import { useLatest } from './useLatest';

// Shared focus management for the app's modal dialogs (DFLT-00074). No
// focus-trap library is used on purpose; this covers what the four modals
// need and nothing more:
//
// - On open: remember the element that had focus, then move focus into the
//   dialog (initialFocusRef if given, else the first focusable element, else
//   the panel itself -- give the panel tabIndex={-1} for that case).
// - While open (and the topmost of the open dialogs, see below): Tab /
//   Shift+Tab wrap around inside the panel, Escape calls onEscape.
// - On close (isOpen -> false, or unmount for conditionally mounted modals):
//   put focus back on the remembered element, or on returnFocusFallbackRef
//   when that element is gone (removed from the DOM, or it was <body>) or
//   cannot take focus any more (DFLT-00147: for example the opener button
//   was disabled in the same render that closed the dialog, as the
//   autopilot's start buttons are while the start request runs -- focus()
//   on it does nothing, and focus would otherwise fall to <body>).
//
// Assumptions / contracts:
//
// - Modals using this hook may be stacked (DFLT-00148: a ConfirmDialog opened
//   from inside SettingsModal). Every open dialog pushes a token onto a
//   module-level stack when its effect runs and removes it in the cleanup,
//   and only the dialog whose token is on top of the stack handles keys: the
//   keydown listeners all live on `document`, and the one registered first
//   (the dialog underneath) runs first, so without this it would take Escape
//   (closing itself) and pull Tab focus back into its own panel -- a dialog
//   rendered through a portal sits outside that panel. A dialog that is not
//   on top returns from its handler without calling preventDefault, leaving
//   the event to the topmost one. When the top dialog closes, its cleanup
//   removes its token, so the one underneath handles keys again, and puts
//   focus back on the element that opened it (inside the dialog underneath).
//   The stack order is the order the effects last ran, so a dialog
//   underneath must not re-run its effect while another is open above it
//   (that would move its token back on top): pass stable refs for
//   initialFocusRef / returnFocusFallbackRef, as every caller does.
// - The listener is registered on `document` in the *bubble* phase. React 18
//   delegates events to the root container, so React's onKeyDown handlers
//   inside the dialog run before this listener. A child that handles a key
//   itself (for example NodeTypesEditor's "new node type" input cancelling on
//   Escape, or a Cmd/Ctrl+Enter submit) must call e.preventDefault(); this
//   hook ignores any key event whose defaultPrevented is already true. If a
//   child calls stopPropagation() instead, the event never reaches document,
//   which also leaves the dialog open.
// - Keys pressed while an IME composition is in progress (isComposing, or
//   keyCode 229) are ignored, so Escape only cancels the conversion.
// - React.StrictMode runs the open effect twice in development (run ->
//   cleanup -> run). The cleanup restores focus to the remembered element and
//   the second run records that same element again before moving focus back
//   into the dialog, so the end state is unchanged. The same holds for the
//   stack token: the cleanup removes it and the second run pushes a new one,
//   so the order of the stack is unchanged too. Keep the cleanup's
//   "restore focus" and the effect's "remember activeElement" symmetric if
//   either is changed. One asymmetric case: if the remembered element cannot
//   take focus at that intermediate cleanup (say, a caller disables its
//   opener while the dialog is open), the cleanup moves focus to the
//   fallback and the second run then remembers the fallback, so in a
//   development build focus returns there on close rather than to the
//   opener. Production builds and tests do not run the effect twice. Callers
//   avoid this by keeping the opener enabled while the dialog is open (the
//   overlay and the Tab wrap already keep the page behind it out of reach).

export interface UseModalDialogOptions {
  // Defaults to true for modals that are mounted only while open.
  isOpen?: boolean;
  onEscape: () => void;
  initialFocusRef?: RefObject<HTMLElement>;
  returnFocusFallbackRef?: RefObject<HTMLElement>;
}

const FOCUSABLE_SELECTOR = 'a[href], button, input, select, textarea, [tabindex]';

// Collects the elements Tab can reach inside container, in DOM order.
//
// Layout-based visibility checks (offsetParent, getClientRects) are
// deliberately not used: jsdom does no layout, so every element would look
// invisible in tests. The modals hide things by not rendering them rather
// than with CSS, so an element hidden only via `display: none` (e.g.
// Tailwind's `hidden` class) would still be treated as focusable here.
//
// Radio buttons sharing a name count once, like the browser's Tab order: the
// checked one, or the group's first one when none is checked. (A browser
// entering an unchecked group with Shift+Tab lands on its *last* radio; every
// radio group inside the current modals always has one checked, so that case
// is not modelled.)
export function getFocusableElements(container: HTMLElement): HTMLElement[] {
  const candidates = Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(el => {
    if (el.getAttribute('tabindex') === '-1') return false;
    if (el.matches(':disabled')) return false;
    if (el instanceof HTMLInputElement && el.type === 'hidden') return false;
    if (el.closest('[hidden], [inert]')) return false;
    return true;
  });

  const radioGroupChoice = new Map<string, HTMLInputElement>();
  for (const el of candidates) {
    if (!(el instanceof HTMLInputElement) || el.type !== 'radio' || !el.name) continue;
    const current = radioGroupChoice.get(el.name);
    if (!current || (!current.checked && el.checked)) {
      radioGroupChoice.set(el.name, el);
    }
  }

  return candidates.filter(el => {
    if (!(el instanceof HTMLInputElement) || el.type !== 'radio' || !el.name) return true;
    return radioGroupChoice.get(el.name) === el;
  });
}

// Whether `active` is the tab stop `stop`. Any radio of the same named group
// counts as that group's stop: focus can sit on a radio other than the one
// getFocusableElements picked (for example an unchecked radio of a group with
// no checked member yet, reached by clicking it while it stays unchecked), and
// Tab from there still leaves the whole group, so wrapping must treat it like
// the picked radio.
function isSameTabStop(active: Element | null, stop: HTMLElement): boolean {
  if (active === stop) return true;
  return (
    active instanceof HTMLInputElement &&
    stop instanceof HTMLInputElement &&
    active.type === 'radio' &&
    stop.type === 'radio' &&
    active.name !== '' &&
    active.name === stop.name
  );
}

// Tokens of the dialogs that are open, in the order they opened; the last one
// is the topmost dialog, the only one that handles keys. See the contract at
// the top of this file.
const openDialogStack: object[] = [];

// Reads ref.current at call time on purpose: the fallback element is looked
// up when the dialog closes, not when it opened, since the one that exists at
// close time is the one that can take focus.
function focusRefTarget(ref: RefObject<HTMLElement> | undefined) {
  ref?.current?.focus();
}

export function useModalDialog<T extends HTMLElement = HTMLDivElement>({
  isOpen = true,
  onEscape,
  initialFocusRef,
  returnFocusFallbackRef
}: UseModalDialogOptions): RefObject<T> {
  const containerRef = useRef<T>(null);
  // Read through a ref so a parent re-creating onEscape on every render (for
  // example SettingsModal's handleClose, which closes over `dirty`) neither
  // re-runs the effect below -- which would redo the initial focus while the
  // user types -- nor leaves a stale callback behind.
  const onEscapeRef = useLatest(onEscape);

  useEffect(() => {
    if (!isOpen) return;

    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const token = {};
    openDialogStack.push(token);

    const container = containerRef.current;
    if (container) {
      const initial = initialFocusRef?.current ?? getFocusableElements(container)[0] ?? container;
      initial.focus();
    }

    const handleKeyDown = (e: KeyboardEvent) => {
      // Checked before anything else, and without preventDefault: a dialog
      // underneath another one leaves every key to the topmost dialog.
      if (openDialogStack[openDialogStack.length - 1] !== token) return;
      if (e.defaultPrevented) return;
      const panel = containerRef.current;
      if (!panel) return;

      if (e.key === 'Escape') {
        if (e.isComposing || e.keyCode === 229) return;
        e.preventDefault();
        onEscapeRef.current();
        return;
      }

      if (e.key !== 'Tab') return;

      // Collected on every keypress: disabled states change while the
      // dialog is open (e.g. inputs are disabled during a launch).
      const focusable = getFocusableElements(panel);
      if (focusable.length === 0) {
        e.preventDefault();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;

      // Focus on the panel itself (it has tabIndex={-1}, so a click on a
      // non-interactive part of it focuses it) is treated like focus outside
      // the panel: otherwise Shift+Tab would move to whatever precedes the
      // panel in the document, i.e. the page behind the dialog.
      const outside = !active || active === panel || !panel.contains(active);
      if (outside) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
        return;
      }
      if (e.shiftKey && isSameTabStop(active, first)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && isSameTabStop(active, last)) {
        e.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', handleKeyDown);

    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      const index = openDialogStack.indexOf(token);
      if (index !== -1) openDialogStack.splice(index, 1);
      if (previouslyFocused && previouslyFocused.isConnected && previouslyFocused !== document.body) {
        previouslyFocused.focus();
        // focus() is a silent no-op on an element that cannot take focus
        // (disabled, or hidden by then); fall back rather than leave focus
        // on the dialog element being removed, i.e. on <body>.
        if (document.activeElement !== previouslyFocused) focusRefTarget(returnFocusFallbackRef);
      } else {
        focusRefTarget(returnFocusFallbackRef);
      }
    };
  }, [isOpen, initialFocusRef, returnFocusFallbackRef, onEscapeRef]);

  return containerRef;
}

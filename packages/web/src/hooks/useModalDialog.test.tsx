// DFLT-00074: shared focus management for the modal dialogs.
import React, { useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { getFocusableElements, useModalDialog } from './useModalDialog';

interface DialogProps {
  onEscape: () => void;
  useInitialFocus?: boolean;
  fallbackRef?: React.RefObject<HTMLElement>;
  children?: React.ReactNode;
}

// A conditionally mounted dialog (isOpen left at its default of true).
const Dialog: React.FC<DialogProps> = ({ onEscape, useInitialFocus, fallbackRef, children }) => {
  const initialRef = useRef<HTMLInputElement>(null);
  const ref = useModalDialog({
    onEscape,
    initialFocusRef: useInitialFocus ? initialRef : undefined,
    returnFocusFallbackRef: fallbackRef
  });
  return (
    <div ref={ref} role="dialog" aria-modal="true" aria-label="dialog" tabIndex={-1} data-testid="panel">
      {children ?? (
        <>
          <button type="button">first</button>
          <input aria-label="middle" ref={initialRef} />
          <button type="button">last</button>
        </>
      )}
    </div>
  );
};

// A dialog that stays mounted and is toggled via isOpen, with an opener.
const ToggleHarness: React.FC<{ onEscape?: () => void }> = ({ onEscape }) => {
  const [isOpen, setIsOpen] = useState(false);
  const ref = useModalDialog({
    isOpen,
    onEscape: () => {
      onEscape?.();
      setIsOpen(false);
    }
  });
  return (
    <>
      <button type="button" onClick={() => setIsOpen(true)}>
        open
      </button>
      {isOpen && (
        <div ref={ref} role="dialog" tabIndex={-1}>
          <button type="button">inside</button>
          <button type="button" onClick={() => setIsOpen(false)}>
            close
          </button>
        </div>
      )}
    </>
  );
};

const btn = (name: string) => screen.getByRole('button', { name });

describe('getFocusableElements', () => {
  it('skips disabled, tabindex="-1", hidden and inert elements, and hidden inputs', () => {
    const { container } = render(
      <div>
        <button type="button">ok1</button>
        <button type="button" disabled>
          disabled
        </button>
        <button type="button" tabIndex={-1}>
          minus-one
        </button>
        <input type="hidden" />
        <div hidden>
          <button type="button">in-hidden</button>
        </div>
        <div {...{ inert: '' }}>
          <button type="button">in-inert</button>
        </div>
        <a href="#x">link</a>
        <a>no-href</a>
        <div tabIndex={0}>region</div>
        <textarea aria-label="ta" />
      </div>
    );

    const names = getFocusableElements(container).map(el => el.textContent || el.tagName);
    expect(names).toEqual(['ok1', 'link', 'region', 'TEXTAREA']);
  });

  it('keeps only the checked radio of a same-name group, or the first when none is checked', () => {
    const { container } = render(
      <div>
        <input type="radio" name="a" aria-label="a1" readOnly />
        <input type="radio" name="a" aria-label="a2" checked readOnly />
        <input type="radio" name="b" aria-label="b1" readOnly />
        <input type="radio" name="b" aria-label="b2" readOnly />
      </div>
    );

    const labels = getFocusableElements(container).map(el => el.getAttribute('aria-label'));
    expect(labels).toEqual(['a2', 'b1']);
  });
});

describe('useModalDialog', () => {
  it('focuses initialFocusRef when given', () => {
    render(<Dialog onEscape={vi.fn()} useInitialFocus />);
    expect(screen.getByLabelText('middle')).toHaveFocus();
  });

  it('focuses the first focusable element when no initialFocusRef is given', () => {
    render(<Dialog onEscape={vi.fn()} />);
    expect(btn('first')).toHaveFocus();
  });

  it('focuses the panel itself when it has nothing focusable', () => {
    render(
      <Dialog onEscape={vi.fn()}>
        <p>text only</p>
      </Dialog>
    );
    expect(screen.getByTestId('panel')).toHaveFocus();
  });

  it('wraps Tab from the last element to the first and Shift+Tab from the first to the last', async () => {
    const user = userEvent.setup();
    render(<Dialog onEscape={vi.fn()} />);

    btn('last').focus();
    await user.tab();
    expect(btn('first')).toHaveFocus();

    await user.tab({ shift: true });
    expect(btn('last')).toHaveFocus();
  });

  it('lets Tab move normally between elements that are not at either end', async () => {
    const user = userEvent.setup();
    render(<Dialog onEscape={vi.fn()} />);

    expect(btn('first')).toHaveFocus();
    await user.tab();
    expect(screen.getByLabelText('middle')).toHaveFocus();
    await user.tab();
    expect(btn('last')).toHaveFocus();
  });

  it('moves focus back inside with Tab / Shift+Tab when focus is outside the panel', async () => {
    const user = userEvent.setup();
    render(
      <>
        <button type="button">behind</button>
        <Dialog onEscape={vi.fn()} />
      </>
    );

    btn('behind').focus();
    await user.tab();
    expect(btn('first')).toHaveFocus();

    (document.activeElement as HTMLElement).blur();
    expect(document.body).toHaveFocus();
    await user.tab({ shift: true });
    expect(btn('last')).toHaveFocus();
  });

  it('treats focus on the panel itself as outside: Shift+Tab goes to the last element, Tab to the first', async () => {
    const user = userEvent.setup();
    render(
      <>
        <button type="button">behind</button>
        <Dialog onEscape={vi.fn()} />
      </>
    );
    const panel = screen.getByTestId('panel');

    panel.focus();
    await user.tab({ shift: true });
    expect(btn('last')).toHaveFocus();
    expect(btn('behind')).not.toHaveFocus();

    panel.focus();
    await user.tab();
    expect(btn('first')).toHaveFocus();
  });

  it('keeps focus in place on Tab when the panel has nothing focusable', () => {
    render(
      <Dialog onEscape={vi.fn()}>
        <p>text only</p>
      </Dialog>
    );
    const notPrevented = fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Tab' });
    expect(notPrevented).toBe(false);
    expect(screen.getByTestId('panel')).toHaveFocus();
  });

  it('skips a disabled element when wrapping', async () => {
    const user = userEvent.setup();
    render(
      <Dialog onEscape={vi.fn()}>
        <button type="button">first</button>
        <button type="button">last</button>
        <button type="button" disabled>
          disabled-at-end
        </button>
      </Dialog>
    );

    btn('last').focus();
    await user.tab();
    expect(btn('first')).toHaveFocus();
  });

  it('treats any radio of the edge radio group as that tab stop when wrapping', () => {
    render(
      <Dialog onEscape={vi.fn()}>
        <input type="radio" name="g" aria-label="r1" readOnly />
        <input type="radio" name="g" aria-label="r2" readOnly />
        <button type="button">last</button>
      </Dialog>
    );

    // No radio is checked, so r1 is the group's tab stop, but focus sits on r2.
    screen.getByLabelText('r2').focus();
    fireEvent.keyDown(screen.getByLabelText('r2'), { key: 'Tab', shiftKey: true });
    expect(btn('last')).toHaveFocus();

    fireEvent.keyDown(btn('last'), { key: 'Tab' });
    expect(screen.getByLabelText('r1')).toHaveFocus();
  });

  it('calls onEscape on Escape', async () => {
    const user = userEvent.setup();
    const onEscape = vi.fn();
    render(<Dialog onEscape={onEscape} />);

    await user.keyboard('{Escape}');
    expect(onEscape).toHaveBeenCalledTimes(1);
  });

  it('ignores Escape while an IME composition is in progress', () => {
    const onEscape = vi.fn();
    render(<Dialog onEscape={onEscape} useInitialFocus />);
    const input = screen.getByLabelText('middle');

    fireEvent.keyDown(input, { key: 'Escape', isComposing: true });
    fireEvent.keyDown(input, { key: 'Escape', keyCode: 229 });
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('does not call onEscape when a child already handled Escape with preventDefault', async () => {
    const user = userEvent.setup();
    const onEscape = vi.fn();
    const childHandler = vi.fn();
    render(
      <Dialog onEscape={onEscape}>
        <input
          aria-label="child"
          onKeyDown={e => {
            if (e.key === 'Escape') {
              e.preventDefault();
              childHandler();
            }
          }}
        />
      </Dialog>
    );

    expect(screen.getByLabelText('child')).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(childHandler).toHaveBeenCalledTimes(1);
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('calls the latest onEscape after a re-render without redoing the initial focus', async () => {
    const user = userEvent.setup();
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = render(<Dialog onEscape={first} useInitialFocus />);

    btn('last').focus();
    rerender(<Dialog onEscape={second} useInitialFocus />);
    expect(btn('last')).toHaveFocus();

    await user.keyboard('{Escape}');
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });

  it('returns focus to the opener when isOpen becomes false', async () => {
    const user = userEvent.setup();
    render(<ToggleHarness />);

    await user.click(btn('open'));
    expect(btn('inside')).toHaveFocus();

    await user.click(btn('close'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(btn('open')).toHaveFocus();
  });

  it('returns focus to the opener on unmount', () => {
    const Harness: React.FC<{ open: boolean }> = ({ open }) => (
      <>
        <button type="button">opener</button>
        {open && <Dialog onEscape={vi.fn()} />}
      </>
    );
    const { rerender } = render(<Harness open={false} />);
    btn('opener').focus();

    rerender(<Harness open />);
    expect(btn('first')).toHaveFocus();

    rerender(<Harness open={false} />);
    expect(btn('opener')).toHaveFocus();
  });

  it('returns focus to the fallback when the opener was removed from the DOM', () => {
    const Harness: React.FC<{ open: boolean; showOpener: boolean }> = ({ open, showOpener }) => {
      const fallbackRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button type="button" ref={fallbackRef}>
            fallback
          </button>
          {showOpener && <button type="button">opener</button>}
          {open && <Dialog onEscape={vi.fn()} fallbackRef={fallbackRef} />}
        </>
      );
    };
    const { rerender } = render(<Harness open={false} showOpener />);
    btn('opener').focus();

    rerender(<Harness open showOpener />);
    expect(btn('first')).toHaveFocus();

    rerender(<Harness open={false} showOpener={false} />);
    expect(btn('fallback')).toHaveFocus();
  });

  it('returns focus to the fallback when focus was on body before opening', () => {
    const Harness: React.FC<{ open: boolean }> = ({ open }) => {
      const fallbackRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button type="button" ref={fallbackRef}>
            fallback
          </button>
          {open && <Dialog onEscape={vi.fn()} fallbackRef={fallbackRef} />}
        </>
      );
    };
    const { rerender } = render(<Harness open={false} />);
    expect(document.body).toHaveFocus();

    rerender(<Harness open />);
    rerender(<Harness open={false} />);
    expect(btn('fallback')).toHaveFocus();
  });

  // DFLT-00147: the autopilot's confirm dialog closes in the same render that
  // disables the start button that opened it.
  it('returns focus to the fallback when the opener was disabled as the dialog closed', () => {
    const Harness: React.FC<{ open: boolean; openerDisabled: boolean }> = ({ open, openerDisabled }) => {
      const fallbackRef = useRef<HTMLDivElement>(null);
      return (
        <div ref={fallbackRef} tabIndex={-1} data-testid="fallback">
          <button type="button" disabled={openerDisabled}>
            opener
          </button>
          {open && <Dialog onEscape={vi.fn()} fallbackRef={fallbackRef} />}
        </div>
      );
    };
    const { rerender } = render(<Harness open={false} openerDisabled={false} />);
    btn('opener').focus();

    rerender(<Harness open openerDisabled={false} />);
    expect(btn('first')).toHaveFocus();

    rerender(<Harness open={false} openerDisabled />);
    expect(screen.getByTestId('fallback')).toHaveFocus();
    expect(document.body).not.toHaveFocus();
  });

  it('still returns focus to the opener, not the fallback, when the opener can take it', () => {
    const Harness: React.FC<{ open: boolean }> = ({ open }) => {
      const fallbackRef = useRef<HTMLDivElement>(null);
      return (
        <div ref={fallbackRef} tabIndex={-1} data-testid="fallback">
          <button type="button">opener</button>
          {open && <Dialog onEscape={vi.fn()} fallbackRef={fallbackRef} />}
        </div>
      );
    };
    const { rerender } = render(<Harness open={false} />);
    btn('opener').focus();

    rerender(<Harness open />);
    rerender(<Harness open={false} />);
    expect(btn('opener')).toHaveFocus();
  });

  it('leaves focus alone without a fallback when the opener was disabled as the dialog closed', () => {
    const Harness: React.FC<{ open: boolean; openerDisabled: boolean }> = ({ open, openerDisabled }) => (
      <>
        <button type="button" disabled={openerDisabled}>
          opener
        </button>
        {open && <Dialog onEscape={vi.fn()} />}
      </>
    );
    const { rerender } = render(<Harness open={false} openerDisabled={false} />);
    btn('opener').focus();
    rerender(<Harness open openerDisabled={false} />);
    expect(() => rerender(<Harness open={false} openerDisabled />)).not.toThrow();
  });

  it('does not throw when there is neither an opener nor a fallback', () => {
    const { unmount } = render(<Dialog onEscape={vi.fn()} />);
    expect(() => unmount()).not.toThrow();
  });

  it('stops reacting to Escape and Tab after closing', async () => {
    const user = userEvent.setup();
    const onEscape = vi.fn();
    render(<ToggleHarness onEscape={onEscape} />);

    await user.click(btn('open'));
    await user.keyboard('{Escape}');
    expect(onEscape).toHaveBeenCalledTimes(1);
    expect(btn('open')).toHaveFocus();

    await user.keyboard('{Escape}');
    expect(onEscape).toHaveBeenCalledTimes(1);

    const notPrevented = fireEvent.keyDown(btn('open'), { key: 'Tab' });
    expect(notPrevented).toBe(true);
    expect(btn('open')).toHaveFocus();
  });
});

// DFLT-00148: a dialog opened from inside another one (ConfirmDialog over
// SettingsModal). The inner dialog is portalled to document.body, like
// ConfirmDialog, so it sits outside the outer panel in the DOM.
describe('useModalDialog with stacked dialogs', () => {
  const InnerDialog: React.FC<{ onEscape: () => void }> = ({ onEscape }) => {
    const ref = useModalDialog({ onEscape });
    return createPortal(
      <div ref={ref} role="dialog" aria-modal="true" aria-label="inner" tabIndex={-1} data-testid="inner">
        <button type="button">inner-first</button>
        <button type="button" onClick={onEscape}>
          inner-last
        </button>
      </div>,
      document.body
    );
  };

  const NestedHarness: React.FC<{ onOuterEscape: () => void; onInnerEscape?: () => void }> = ({
    onOuterEscape,
    onInnerEscape
  }) => {
    const [innerOpen, setInnerOpen] = useState(false);
    const ref = useModalDialog({ onEscape: onOuterEscape });
    return (
      <div ref={ref} role="dialog" aria-modal="true" aria-label="outer" tabIndex={-1} data-testid="outer">
        <button type="button">outer-first</button>
        <button type="button" onClick={() => setInnerOpen(true)}>
          open-inner
        </button>
        <button type="button">outer-last</button>
        {innerOpen && (
          <InnerDialog
            onEscape={() => {
              onInnerEscape?.();
              setInnerOpen(false);
            }}
          />
        )}
      </div>
    );
  };

  it('lets only the topmost dialog handle Escape', async () => {
    const user = userEvent.setup();
    const onOuterEscape = vi.fn();
    const onInnerEscape = vi.fn();
    render(<NestedHarness onOuterEscape={onOuterEscape} onInnerEscape={onInnerEscape} />);

    await user.click(btn('open-inner'));
    expect(btn('inner-first')).toHaveFocus();

    await user.keyboard('{Escape}');
    expect(onInnerEscape).toHaveBeenCalledTimes(1);
    expect(onOuterEscape).not.toHaveBeenCalled();
    expect(screen.queryByTestId('inner')).not.toBeInTheDocument();
    expect(screen.getByTestId('outer')).toBeInTheDocument();
  });

  it('keeps Tab and Shift+Tab inside the topmost dialog', async () => {
    const user = userEvent.setup();
    render(<NestedHarness onOuterEscape={vi.fn()} />);

    await user.click(btn('open-inner'));
    expect(btn('inner-first')).toHaveFocus();

    await user.tab();
    expect(btn('inner-last')).toHaveFocus();
    await user.tab();
    expect(btn('inner-first')).toHaveFocus();
    await user.tab({ shift: true });
    expect(btn('inner-last')).toHaveFocus();
  });

  it('returns focus to the element inside the dialog underneath, which then handles keys again', async () => {
    const user = userEvent.setup();
    const onOuterEscape = vi.fn();
    render(<NestedHarness onOuterEscape={onOuterEscape} />);

    await user.click(btn('open-inner'));
    await user.click(btn('inner-last'));
    expect(screen.queryByTestId('inner')).not.toBeInTheDocument();
    expect(btn('open-inner')).toHaveFocus();

    btn('outer-last').focus();
    await user.tab();
    expect(btn('outer-first')).toHaveFocus();

    await user.keyboard('{Escape}');
    expect(onOuterEscape).toHaveBeenCalledTimes(1);
  });

  it('leaves the event alone (no preventDefault) in the dialog underneath', async () => {
    const user = userEvent.setup();
    render(<NestedHarness onOuterEscape={vi.fn()} />);
    await user.click(btn('open-inner'));

    // Tab between two elements of the topmost dialog needs no wrapping, so
    // nothing prevents it: the dialog underneath, which would see focus
    // outside its own panel, does not step in.
    const notPrevented = fireEvent.keyDown(btn('inner-first'), { key: 'Tab' });
    expect(notPrevented).toBe(true);
    expect(btn('inner-first')).toHaveFocus();
  });

  it('keeps the stack order under React.StrictMode', async () => {
    const user = userEvent.setup();
    const onOuterEscape = vi.fn();
    const onInnerEscape = vi.fn();
    render(
      <React.StrictMode>
        <NestedHarness onOuterEscape={onOuterEscape} onInnerEscape={onInnerEscape} />
      </React.StrictMode>
    );

    await user.click(btn('open-inner'));
    await user.keyboard('{Escape}');
    expect(onInnerEscape).toHaveBeenCalledTimes(1);
    expect(onOuterEscape).not.toHaveBeenCalled();

    await user.keyboard('{Escape}');
    expect(onOuterEscape).toHaveBeenCalledTimes(1);
  });
});

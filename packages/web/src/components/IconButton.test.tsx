// DFLT-00171: the icon-only button with a visible tooltip that replaces the
// native title attribute.
import { createRef, type FormEvent } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { IconButton, OPEN_DELAY_MS } from './IconButton';
import i18n from '../i18n';
import { submittingName } from '../test/submittingName';
import { allIconButtonTooltips, openIconButtonTooltip, openIconButtonTooltips, waitForHoverOpenDelay } from '../test/iconButtonTooltip';

const Icon = () => <svg aria-hidden="true" data-testid="icon" />;

describe('IconButton', () => {
  it('is named by label through aria-label, with no title', () => {
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Settings' });
    expect(button).toHaveAttribute('aria-label', 'Settings');
    expect(button).toHaveAttribute('type', 'button');
    expect(button).not.toHaveAttribute('title');
    expect(button).not.toHaveAttribute('aria-describedby');
    expect(button).toHaveAccessibleDescription('');
  });

  it('renders no tooltip until it is needed', () => {
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    expect(allIconButtonTooltips()).toHaveLength(0);
  });

  it('shows the tooltip on keyboard focus and hides it on blur', async () => {
    const user = userEvent.setup();
    render(
      <>
        <IconButton label="Settings">
          <Icon />
        </IconButton>
        <button type="button">next</button>
      </>
    );
    await user.tab();
    expect(screen.getByRole('button', { name: 'Settings' })).toHaveFocus();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toBeVisible();
    expect(tooltip).toHaveTextContent('Settings');
    expect(tooltip).toHaveAttribute('aria-hidden', 'true');
    // Outside the button: its text does not leak into the button's content.
    expect(screen.getByRole('button', { name: 'Settings' })).not.toContainElement(tooltip);

    await user.tab();
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('shows the tooltip text when it differs from the label', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Delete ticket: T-1" tooltip="Delete">
        <Icon />
      </IconButton>
    );
    await user.tab();
    expect(openIconButtonTooltip()).toHaveTextContent(/^Delete$/);
    expect(screen.getByRole('button', { name: 'Delete ticket: T-1' })).toHaveAccessibleDescription('');
  });

  it('shows the tooltip on hover and hides it shortly after the pointer leaves', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Refresh">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Refresh' });
    await user.hover(button);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toBeVisible();
    await user.unhover(button);
    // The close is delayed so the pointer can move onto the tooltip.
    await new Promise(r => setTimeout(r, 150));
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('stays open while the pointer moves from the button onto the tooltip', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Refresh">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Refresh' });
    await user.hover(button);
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    await user.hover(tooltip);
    await new Promise(r => setTimeout(r, 150));
    expect(openIconButtonTooltip()).toBe(tooltip);
    await user.unhover(tooltip);
    await new Promise(r => setTimeout(r, 150));
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('shows the tooltip when a disabled button is hovered', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Delete" tooltip="Defaults cannot be deleted" disabled wrapperClassName="shrink-0">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Delete' });
    const wrapper = button.parentElement as HTMLElement;
    expect(wrapper).toHaveClass('inline-flex', 'shrink-0');
    await user.hover(wrapper);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toHaveTextContent('Defaults cannot be deleted');
  });

  // DFLT-00203: aria-disabled keeps the button focusable, so its tooltip
  // and description are reachable from the keyboard, while IconButton
  // swallows every click -- including the ones Enter and Space turn into.
  describe('aria-disabled', () => {
    it.each([true, 'true'] as const)('takes Tab focus and shows the tooltip and description (aria-disabled=%s)', async value => {
      const user = userEvent.setup();
      render(
        <IconButton label="Delete type: plan" tooltip="Defaults cannot be deleted" describeWithTooltip aria-disabled={value}>
          <Icon />
        </IconButton>
      );
      const button = screen.getByRole('button', { name: 'Delete type: plan' });
      expect(button).toBeEnabled();
      expect(button).toHaveAttribute('aria-disabled', 'true');

      await user.tab();
      expect(button).toHaveFocus();
      expect(openIconButtonTooltip()).toHaveTextContent('Defaults cannot be deleted');
      expect(button).toHaveAccessibleDescription('Defaults cannot be deleted');
    });

    it.each([true, 'true'] as const)('calls onClick on neither a click nor Enter nor Space (aria-disabled=%s)', async value => {
      const user = userEvent.setup();
      const onClick = vi.fn();
      const onSubmit = vi.fn((e: FormEvent) => e.preventDefault());
      render(
        <form onSubmit={onSubmit}>
          <IconButton label="Delete" aria-disabled={value} type="submit" onClick={onClick}>
            <Icon />
          </IconButton>
        </form>
      );
      const button = screen.getByRole('button', { name: 'Delete' });

      await user.click(button);
      expect(button).toHaveFocus();
      await user.keyboard('{Enter}');
      await user.keyboard(' ');

      expect(onClick).not.toHaveBeenCalled();
      expect(onSubmit).not.toHaveBeenCalled();
    });

    it.each([false, 'false', undefined] as const)('calls onClick as usual when aria-disabled is %s', async value => {
      const user = userEvent.setup();
      const onClick = vi.fn();
      render(
        <IconButton label="Delete" aria-disabled={value} onClick={onClick}>
          <Icon />
        </IconButton>
      );
      await user.click(screen.getByRole('button', { name: 'Delete' }));
      expect(onClick).toHaveBeenCalledTimes(1);
    });
  });

  it('closes the tooltip on Escape, marking only that Escape as handled', async () => {
    const user = userEvent.setup();
    const seen: boolean[] = [];
    const listener = (e: KeyboardEvent) => {
      if (e.key === 'Escape') seen.push(e.defaultPrevented);
    };
    document.addEventListener('keydown', listener);
    try {
      render(
        <IconButton label="Settings">
          <Icon />
        </IconButton>
      );
      await user.tab();
      expect(openIconButtonTooltip()).toBeVisible();

      await user.keyboard('{Escape}');
      expect(openIconButtonTooltips()).toHaveLength(0);
      expect(screen.getByRole('button', { name: 'Settings' })).toHaveFocus();

      // With the tooltip closed, Escape is left for whoever else wants it
      // (e.g. the enclosing modal).
      await user.keyboard('{Escape}');
      expect(seen).toEqual([true, false]);
    } finally {
      document.removeEventListener('keydown', listener);
    }
  });

  it('does not stop Escape from propagating', async () => {
    const user = userEvent.setup();
    const onKeyDown = vi.fn();
    render(
      <div onKeyDown={e => onKeyDown(e.key, e.defaultPrevented)}>
        <IconButton label="Settings">
          <Icon />
        </IconButton>
      </div>
    );
    await user.tab();
    await user.keyboard('{Escape}');
    expect(onKeyDown).toHaveBeenCalledWith('Escape', true);
  });

  it('keeps clicks on the tooltip from reaching the ancestors of the button', async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    const onMouseDown = vi.fn();
    const onMouseUp = vi.fn();
    const onPointerDown = vi.fn();
    render(
      <div onClick={onClick} onMouseDown={onMouseDown} onMouseUp={onMouseUp} onPointerDown={onPointerDown}>
        <IconButton label="Delete">
          <Icon />
        </IconButton>
      </div>
    );
    // Opened by keyboard focus: a press on the tooltip ends only a
    // hover-opened state (DFLT-00322), so this tooltip stays in place and the
    // whole press lands on it. (A hover-opened one closes on the pointerdown,
    // and the rest of the press no longer reaches it -- see "closes a
    // hover-opened tooltip on a pointerdown on it" below.) The events are
    // fired one by one because user.click's mousedown would also move focus
    // off the button, which closes the tooltip before the mouseup and the
    // click; each of the four has its own stopPropagation to guard.
    await user.tab();
    const tooltip = openIconButtonTooltip();
    fireEvent.pointerDown(tooltip);
    fireEvent.mouseDown(tooltip);
    fireEvent.mouseUp(tooltip);
    fireEvent.click(tooltip);
    expect(openIconButtonTooltip()).toBe(tooltip);
    expect(onClick).not.toHaveBeenCalled();
    expect(onMouseDown).not.toHaveBeenCalled();
    expect(onMouseUp).not.toHaveBeenCalled();
    expect(onPointerDown).not.toHaveBeenCalled();

    // The button's own click still bubbles as before.
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('keeps a whole click on a describeWithTooltip tooltip from the ancestors, though the press closes it', async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    const onMouseDown = vi.fn();
    const onMouseUp = vi.fn();
    const onPointerDown = vi.fn();
    render(
      <div onClick={onClick} onMouseDown={onMouseDown} onMouseUp={onMouseUp} onPointerDown={onPointerDown}>
        <IconButton label="Delete type: plan" tooltip="Defaults cannot be deleted" describeWithTooltip>
          <Icon />
        </IconButton>
      </div>
    );
    await user.hover(screen.getByRole('button', { name: 'Delete type: plan' }));
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    // The pointerdown ends the hover-opened state, but the element stays
    // (hidden) for aria-describedby, so the rest of the press still lands on
    // it and has to be stopped there as well.
    await user.click(tooltip);
    expect(openIconButtonTooltips()).toHaveLength(0);
    expect(allIconButtonTooltips()).toEqual([tooltip]);
    expect(onClick).not.toHaveBeenCalled();
    expect(onMouseDown).not.toHaveBeenCalled();
    expect(onMouseUp).not.toHaveBeenCalled();
    expect(onPointerDown).not.toHaveBeenCalled();
  });

  it('exposes the tooltip as the description only with describeWithTooltip, even while it is closed', async () => {
    const user = userEvent.setup();
    render(
      <>
        <IconButton label="Delete type: plan" tooltip="Defaults cannot be deleted" describeWithTooltip>
          <Icon />
        </IconButton>
        <IconButton label="Delete type: custom" tooltip="Delete">
          <Icon />
        </IconButton>
      </>
    );
    const described = screen.getByRole('button', { name: 'Delete type: plan' });
    const plain = screen.getByRole('button', { name: 'Delete type: custom' });

    // Rendered (hidden) while closed so the reference always resolves.
    const tooltips = allIconButtonTooltips();
    expect(tooltips).toHaveLength(1);
    expect(tooltips[0]).not.toBeVisible();
    expect(tooltips[0]).toHaveAttribute('aria-hidden', 'true');
    expect(described).toHaveAttribute('aria-describedby', tooltips[0].id);
    expect(described).toHaveAccessibleDescription('Defaults cannot be deleted');
    expect(plain).not.toHaveAttribute('aria-describedby');
    expect(plain).toHaveAccessibleDescription('');

    await user.tab();
    expect(described).toHaveFocus();
    expect(tooltips[0]).toBeVisible();
    expect(described).toHaveAccessibleDescription('Defaults cannot be deleted');
  });

  it('keeps a caller-given aria-describedby next to the tooltip reference', () => {
    render(
      <>
        <p id="extra">extra</p>
        <IconButton label="Delete" tooltip="Cannot delete" describeWithTooltip aria-describedby="extra">
          <Icon />
        </IconButton>
      </>
    );
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveAccessibleDescription('extra Cannot delete');
  });

  it('follows a label change while the tooltip is open', async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <IconButton label="Copy ID">
        <Icon />
      </IconButton>
    );
    await user.tab();
    expect(openIconButtonTooltip()).toHaveTextContent('Copy ID');
    rerender(
      <IconButton label="Copied">
        <Icon />
      </IconButton>
    );
    expect(screen.getByRole('button', { name: 'Copied' })).toHaveFocus();
    expect(openIconButtonTooltip()).toHaveTextContent('Copied');
  });

  it('forwards the ref and the other button props to the button', () => {
    const ref = createRef<HTMLButtonElement>();
    const onClick = vi.fn();
    render(
      <IconButton ref={ref} label="Delete" data-focus-key="k" data-testid="del" className="p-1" onClick={onClick}>
        <Icon />
      </IconButton>
    );
    const button = screen.getByTestId('del');
    expect(ref.current).toBe(button);
    expect(button).toHaveAttribute('data-focus-key', 'k');
    expect(button).toHaveClass('p-1');
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('does not show the tooltip on a focus that is not :focus-visible', () => {
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Settings' });
    const matches = vi.spyOn(button, 'matches').mockImplementation(sel => sel !== ':focus-visible');
    try {
      act(() => button.focus());
      expect(openIconButtonTooltips()).toHaveLength(0);
    } finally {
      matches.mockRestore();
    }
  });

  it('shows the tooltip on focus when :focus-visible cannot be evaluated', () => {
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Settings' });
    const matches = vi.spyOn(button, 'matches').mockImplementation(() => {
      throw new SyntaxError('unsupported selector');
    });
    try {
      act(() => button.focus());
      expect(openIconButtonTooltip()).toBeVisible();
    } finally {
      matches.mockRestore();
    }
  });

  describe('hover and focus are tracked separately', () => {
    it('keeps a focus-opened tooltip open while the pointer passes over the button', async () => {
      const user = userEvent.setup();
      render(
        <IconButton label="Settings">
          <Icon />
        </IconButton>
      );
      const button = screen.getByRole('button', { name: 'Settings' });
      await user.tab();
      expect(button).toHaveFocus();
      expect(openIconButtonTooltip()).toBeVisible();

      await user.hover(button);
      await waitForHoverOpenDelay();
      await user.unhover(button);
      await new Promise(r => setTimeout(r, 150));
      expect(button).toHaveFocus();
      expect(openIconButtonTooltip()).toBeVisible();
    });

    it('keeps a hover-opened tooltip open when focus leaves the button, until the pointer leaves', async () => {
      const user = userEvent.setup();
      render(
        <>
          <IconButton label="Settings">
            <Icon />
          </IconButton>
          <button type="button">next</button>
        </>
      );
      const button = screen.getByRole('button', { name: 'Settings' });
      await user.tab();
      await user.hover(button);
      await waitForHoverOpenDelay();
      await user.tab();
      expect(screen.getByRole('button', { name: 'next' })).toHaveFocus();
      expect(openIconButtonTooltip()).toHaveTextContent('Settings');

      await user.unhover(button);
      await new Promise(r => setTimeout(r, 150));
      expect(openIconButtonTooltips()).toHaveLength(0);
    });

    it('closes a tooltip open by both hover and focus on one Escape, and keeps it closed', async () => {
      const user = userEvent.setup();
      render(
        <IconButton label="Settings">
          <Icon />
        </IconButton>
      );
      const button = screen.getByRole('button', { name: 'Settings' });
      await user.tab();
      await user.hover(button);
      await waitForHoverOpenDelay();
      await user.keyboard('{Escape}');
      expect(openIconButtonTooltips()).toHaveLength(0);
      await new Promise(r => setTimeout(r, 150));
      expect(openIconButtonTooltips()).toHaveLength(0);
      expect(button).toHaveFocus();
    });
  });

  describe('Escape with focus outside the button', () => {
    it('dismisses a hover-opened tooltip without preventDefault, so the focused element still gets the key', async () => {
      const user = userEvent.setup();
      const onInputKeyDown = vi.fn();
      const seen: boolean[] = [];
      const listener = (e: KeyboardEvent) => {
        if (e.key === 'Escape') seen.push(e.defaultPrevented);
      };
      document.addEventListener('keydown', listener);
      try {
        render(
          <>
            <input aria-label="field" onKeyDown={e => onInputKeyDown(e.key)} />
            <IconButton label="Refresh">
              <Icon />
            </IconButton>
          </>
        );
        const input = screen.getByRole('textbox', { name: 'field' });
        await user.click(input);
        await user.hover(screen.getByRole('button', { name: 'Refresh' }));
        await waitForHoverOpenDelay();
        expect(openIconButtonTooltip()).toBeVisible();

        await user.keyboard('{Escape}');
        expect(openIconButtonTooltips()).toHaveLength(0);
        expect(input).toHaveFocus();
        expect(onInputKeyDown).toHaveBeenCalledWith('Escape');
        expect(seen).toEqual([false]);
      } finally {
        document.removeEventListener('keydown', listener);
      }
    });

    it('dismisses the tooltip of a hovered disabled button', async () => {
      const user = userEvent.setup();
      render(
        <>
          <input aria-label="field" />
          <IconButton label="Delete" tooltip="Defaults cannot be deleted" disabled>
            <Icon />
          </IconButton>
        </>
      );
      await user.click(screen.getByRole('textbox', { name: 'field' }));
      await user.hover(screen.getByRole('button', { name: 'Delete' }).parentElement as HTMLElement);
      await waitForHoverOpenDelay();
      expect(openIconButtonTooltip()).toHaveTextContent('Defaults cannot be deleted');

      await user.keyboard('{Escape}');
      expect(openIconButtonTooltips()).toHaveLength(0);
    });

    it('dismisses the tooltip even when the focused element stops the key from propagating', async () => {
      const user = userEvent.setup();
      render(
        <>
          <input aria-label="field" onKeyDown={e => e.stopPropagation()} />
          <IconButton label="Refresh">
            <Icon />
          </IconButton>
        </>
      );
      await user.click(screen.getByRole('textbox', { name: 'field' }));
      await user.hover(screen.getByRole('button', { name: 'Refresh' }));
      await waitForHoverOpenDelay();
      await user.keyboard('{Escape}');
      expect(openIconButtonTooltips()).toHaveLength(0);
    });

    it('stops listening once the tooltip is closed', async () => {
      const user = userEvent.setup();
      const add = vi.spyOn(document, 'addEventListener');
      const remove = vi.spyOn(document, 'removeEventListener');
      try {
        render(
          <IconButton label="Refresh">
            <Icon />
          </IconButton>
        );
        const button = screen.getByRole('button', { name: 'Refresh' });
        await user.hover(button);
        await waitForHoverOpenDelay();
        const registered = add.mock.calls.filter(([type, , capture]) => type === 'keydown' && capture === true);
        expect(registered).toHaveLength(1);
        await user.unhover(button);
        await new Promise(r => setTimeout(r, 150));
        expect(remove).toHaveBeenCalledWith('keydown', registered[0][1], true);
      } finally {
        add.mockRestore();
        remove.mockRestore();
      }
    });
  });

  describe('Escape during IME composition', () => {
    it.each([
      ['isComposing', { key: 'Escape', isComposing: true }],
      ['keyCode 229', { key: 'Escape', keyCode: 229 }]
    ])('leaves a hover-opened tooltip open (%s), with focus elsewhere', async (_name, init) => {
      const user = userEvent.setup();
      render(
        <>
          <input aria-label="field" />
          <IconButton label="Refresh">
            <Icon />
          </IconButton>
        </>
      );
      const input = screen.getByRole('textbox', { name: 'field' });
      await user.click(input);
      await user.hover(screen.getByRole('button', { name: 'Refresh' }));
      await waitForHoverOpenDelay();
      fireEvent.keyDown(input, init);
      expect(openIconButtonTooltip()).toBeVisible();
    });

    it.each([
      ['isComposing', { key: 'Escape', isComposing: true }],
      ['keyCode 229', { key: 'Escape', keyCode: 229 }]
    ])('leaves a focus-opened tooltip open and the key unhandled (%s)', async (_name, init) => {
      const user = userEvent.setup();
      render(
        <IconButton label="Settings">
          <Icon />
        </IconButton>
      );
      await user.tab();
      const button = screen.getByRole('button', { name: 'Settings' });
      const notCancelled = fireEvent.keyDown(button, init);
      expect(notCancelled).toBe(true);
      expect(openIconButtonTooltip()).toBeVisible();
    });
  });

  describe('tooltip position', () => {
    const VIEWPORT = { width: 800, height: 600 };
    const TOOLTIP = { width: 70, height: 20 };

    const rect = (left: number, top: number, width: number, height: number) =>
      ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON: () => ({}) }) as DOMRect;

    function stubLayout(button: { left: number; top: number }) {
      Object.defineProperty(document.documentElement, 'clientWidth', { configurable: true, value: VIEWPORT.width });
      Object.defineProperty(document.documentElement, 'clientHeight', { configurable: true, value: VIEWPORT.height });
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
        if (this.hasAttribute('data-icon-button-tooltip')) return rect(0, 0, TOOLTIP.width, TOOLTIP.height);
        if (this.tagName === 'BUTTON') return rect(button.left, button.top, 16, 16);
        return rect(0, 0, 0, 0);
      });
    }

    afterEach(() => {
      vi.restoreAllMocks();
      // Drop the own properties so the prototype getters apply again.
      delete (document.documentElement as unknown as Record<string, unknown>).clientWidth;
      delete (document.documentElement as unknown as Record<string, unknown>).clientHeight;
    });

    async function openAt(button: { left: number; top: number }, side?: 'top' | 'bottom') {
      stubLayout(button);
      const user = userEvent.setup();
      render(
        <IconButton label="Delete" tooltipSide={side}>
          <Icon />
        </IconButton>
      );
      await user.hover(screen.getByRole('button', { name: 'Delete' }));
      await waitForHoverOpenDelay();
      const tooltip = openIconButtonTooltip();
      expect(tooltip).toHaveStyle({ visibility: 'visible' });
      return tooltip;
    }

    it('centres the tooltip below the button when there is room', async () => {
      const tooltip = await openAt({ left: 400, top: 100 });
      // 408 (button centre) - 35; 116 (button bottom) + 6.
      expect(tooltip).toHaveStyle({ left: '373px', top: '122px' });
    });

    it('keeps the tooltip inside the right edge of the viewport', async () => {
      const tooltip = await openAt({ left: 780, top: 10 });
      // viewport width - margin 4 - tooltip width.
      expect(tooltip).toHaveStyle({ left: `${VIEWPORT.width - 4 - TOOLTIP.width}px`, top: '32px' });
    });

    it('keeps the tooltip inside the left edge of the viewport', async () => {
      const tooltip = await openAt({ left: 0, top: 10 });
      expect(tooltip).toHaveStyle({ left: '4px' });
    });

    it('flips a bottom tooltip above the button when there is no room below', async () => {
      const tooltip = await openAt({ left: 400, top: 570 }, 'bottom');
      // 570 (button top) - 6 - 20.
      expect(tooltip).toHaveStyle({ top: '544px' });
    });

    it('shows a top tooltip above the button when there is room', async () => {
      const tooltip = await openAt({ left: 400, top: 100 }, 'top');
      expect(tooltip).toHaveStyle({ top: '74px' });
    });

    it('flips a top tooltip below the button when there is no room above', async () => {
      const tooltip = await openAt({ left: 400, top: 2 }, 'top');
      // 18 (button bottom) + 6.
      expect(tooltip).toHaveStyle({ top: '24px' });
    });

    // DFLT-00322: a resize re-measures at once and again in the next frame.
    describe('after a resize', () => {
      // Collects requestAnimationFrame callbacks so a test runs them itself.
      function controlFrames() {
        const frames = new Map<number, FrameRequestCallback>();
        let next = 1;
        vi.spyOn(window, 'requestAnimationFrame').mockImplementation(cb => {
          const id = next++;
          frames.set(id, cb);
          return id;
        });
        vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(id => {
          frames.delete(id);
        });
        return frames;
      }

      it('follows the button to where the resize moved it', async () => {
        const button = { left: 400, top: 100 };
        const tooltip = await openAt(button);
        expect(tooltip).toHaveStyle({ left: '373px', top: '122px' });
        button.left = 200;
        button.top = 300;
        act(() => {
          window.dispatchEvent(new Event('resize'));
        });
        // 208 - 35; 316 + 6.
        expect(tooltip).toHaveStyle({ left: '173px', top: '322px' });
      });

      it('measures again in the next frame, for a layout that settles only after the resize event', async () => {
        const button = { left: 400, top: 100 };
        const tooltip = await openAt(button);
        const frames = controlFrames();
        act(() => {
          window.dispatchEvent(new Event('resize'));
        });
        // The layout had not changed yet when the resize event came.
        expect(tooltip).toHaveStyle({ left: '373px', top: '122px' });
        button.left = 200;
        button.top = 300;
        act(() => {
          for (const cb of [...frames.values()]) cb(performance.now());
        });
        expect(tooltip).toHaveStyle({ left: '173px', top: '322px' });
      });

      // Found in the browser: a fixed tooltip that a narrowing window has
      // left near its right edge shrinks to the room left there, so it was
      // measured as a narrow column and placed off the button.
      it('measures the tooltip at full width even where the old position left it little room', async () => {
        const button = { left: 700, top: 100 };
        const tooltip = await openAt(button);
        // 708 - 35.
        expect(tooltip).toHaveStyle({ left: '673px', top: '122px' });
        // From here on the tooltip shrinks to the room right of its left edge
        // in a 400px-wide viewport, as a fixed element in a browser does.
        const viewportWidth = 400;
        Object.defineProperty(document.documentElement, 'clientWidth', { configurable: true, value: viewportWidth });
        vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockImplementation(function (this: HTMLElement) {
          if (this.hasAttribute('data-icon-button-tooltip')) {
            const left = parseFloat(this.style.left) || 0;
            const width = Math.max(10, Math.min(TOOLTIP.width, viewportWidth - left));
            return rect(left, 0, width, TOOLTIP.height * Math.ceil(TOOLTIP.width / width));
          }
          if (this.tagName === 'BUTTON') return rect(button.left, button.top, 16, 16);
          return rect(0, 0, 0, 0);
        });
        button.left = 100;
        act(() => {
          window.dispatchEvent(new Event('resize'));
        });
        // 108 - 35: centred with the full 70px width, not the 10px left at 673.
        expect(tooltip).toHaveStyle({ left: '73px', top: '122px' });
      });

      it('keeps one pending frame for consecutive resizes', async () => {
        await openAt({ left: 400, top: 100 });
        const frames = controlFrames();
        act(() => {
          window.dispatchEvent(new Event('resize'));
          window.dispatchEvent(new Event('resize'));
          window.dispatchEvent(new Event('resize'));
        });
        expect(frames.size).toBe(1);
      });

      it.each(['close', 'unmount'] as const)('runs a frame left pending by a resize without error after the tooltip is gone (%s)', async how => {
        const errors = vi.spyOn(console, 'error');
        const user = userEvent.setup();
        stubLayout({ left: 400, top: 100 });
        const { unmount } = render(
          <IconButton label="Delete">
            <Icon />
          </IconButton>
        );
        const button = screen.getByRole('button', { name: 'Delete' });
        await user.hover(button);
        await waitForHoverOpenDelay();
        openIconButtonTooltip();
        const frames = controlFrames();
        const pending: FrameRequestCallback[] = [];
        act(() => {
          window.dispatchEvent(new Event('resize'));
        });
        pending.push(...frames.values());
        expect(pending).toHaveLength(1);
        if (how === 'close') {
          await user.keyboard('{Escape}');
          expect(openIconButtonTooltips()).toHaveLength(0);
        } else {
          unmount();
        }
        // Closing cancelled it; run it anyway, as a frame already underway would.
        expect(frames.size).toBe(0);
        expect(() => act(() => pending.forEach(cb => cb(performance.now())))).not.toThrow();
        expect(errors).not.toHaveBeenCalled();
      });
    });
  });
});

// DFLT-00285: options for a button that shows its own name as text (the
// header's project switcher).
describe('IconButton text-named options', () => {
  it('leaves an existing call unchanged: named by aria-label, one-line tooltip', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Settings' });
    expect(button).toHaveAttribute('aria-label', 'Settings');
    await user.hover(button);
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toHaveTextContent(/^Settings$/);
    expect(tooltip.children).toHaveLength(0);
  });

  it('takes the accessible name from the content with nameFromContent', () => {
    render(
      <IconButton nameFromContent>
        <Icon />
        <span>Alpha</span>
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Alpha' });
    expect(button).not.toHaveAttribute('aria-label');
    expect(button).toHaveAccessibleName('Alpha');
  });

  it('sets no aria-label with nameFromContent even when label is given, and shows label as the tooltip', async () => {
    const user = userEvent.setup();
    render(
      <IconButton nameFromContent label="Switch project">
        <Icon />
        <span>Alpha</span>
      </IconButton>
    );
    const button = screen.getByRole('button', { name: 'Alpha' });
    expect(button).not.toHaveAttribute('aria-label');
    expect(button).toHaveAccessibleName('Alpha');
    await user.hover(button);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toHaveTextContent(/^Switch project$/);
  });

  it('opens no tooltip with neither label nor tooltip', async () => {
    const user = userEvent.setup();
    render(
      <IconButton nameFromContent>
        <span>Alpha</span>
      </IconButton>
    );
    await user.hover(screen.getByRole('button', { name: 'Alpha' }));
    await waitForHoverOpenDelay();
    await user.tab();
    expect(allIconButtonTooltips()).toHaveLength(0);
  });

  it('shows a tooltip of two lines', async () => {
    const user = userEvent.setup();
    render(
      <IconButton
        nameFromContent
        tooltip={
          <>
            <span className="block">Alpha</span>
            <span className="block">/work/alpha</span>
          </>
        }
      >
        <span>Alpha</span>
      </IconButton>
    );
    await user.hover(screen.getByRole('button', { name: 'Alpha' }));
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    const lines = Array.from(tooltip.children);
    expect(lines.map(l => l.textContent)).toEqual(['Alpha', '/work/alpha']);
    for (const line of lines) expect(line).toHaveClass('block');
    expect(tooltip).toHaveAttribute('aria-hidden', 'true');
  });

  // A long unbroken name ('A' x 80) or path in a narrow window: the tooltip
  // is at most 20rem and never wider than the viewport less the 4px margin
  // on each side (a fixed element's % resolves against the viewport, which
  // excludes a classic scrollbar, unlike 100vw), and it breaks anywhere so
  // its min-content width cannot push it past that cap. overflow-wrap:
  // break-word would not lower min-content, so the unbroken word would widen
  // the tooltip to 20rem and clip it off-screen. For text that already fits
  // the look is unchanged. jsdom computes no layout, so the classes are
  // pinned; the real-browser measurements are in the implementation notes.
  it('caps the tooltip width by the viewport and lets it break anywhere', async () => {
    const user = userEvent.setup();
    const longName = 'A'.repeat(80);
    render(
      <IconButton nameFromContent tooltip={<span className="block">{longName}</span>}>
        <span>{longName}</span>
      </IconButton>
    );
    await user.hover(screen.getByRole('button', { name: longName }));
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toHaveClass('max-w-[min(20rem,calc(100%-8px))]', 'wrap-anywhere', 'whitespace-normal', 'fixed');
    expect(tooltip).not.toHaveClass('max-w-xs', 'wrap-break-word');
  });

  // DFLT-00293: the tooltip's text size is in rem, so it grows with a 200%
  // root or browser font size like the rest of the page (a px size would
  // stay 11px). 0.6875rem is 11px at a 16px root, so the look at 100% is
  // unchanged.
  it('sizes the tooltip text in rem, not px', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    await user.hover(screen.getByRole('button', { name: 'Settings' }));
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toHaveClass('text-[0.6875rem]');
    expect(Array.from(tooltip.classList).filter(c => /^text-\[[\d.]+px\]$/.test(c))).toEqual([]);
  });

  it('gives an existing one-line tooltip the same width cap and wrapping', async () => {
    const user = userEvent.setup();
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    await user.hover(screen.getByRole('button', { name: 'Settings' }));
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toHaveClass('max-w-[min(20rem,calc(100%-8px))]', 'wrap-anywhere');
  });

  it('opens no tooltip with tooltipDisabled, even on keyboard focus, and leaves Escape alone', async () => {
    const user = userEvent.setup();
    const seen: boolean[] = [];
    const listener = (e: KeyboardEvent) => {
      if (e.key === 'Escape') seen.push(e.defaultPrevented);
    };
    document.addEventListener('keydown', listener);
    try {
      render(
        <IconButton label="Settings" tooltipDisabled>
          <Icon />
        </IconButton>
      );
      await user.tab();
      const button = screen.getByRole('button', { name: 'Settings' });
      expect(button).toHaveFocus();
      expect(button.matches(':focus-visible')).toBe(true);
      expect(allIconButtonTooltips()).toHaveLength(0);
      await user.hover(button);
      await waitForHoverOpenDelay();
      expect(allIconButtonTooltips()).toHaveLength(0);
      await user.keyboard('{Escape}');
      expect(seen).toEqual([false]);
    } finally {
      document.removeEventListener('keydown', listener);
    }
  });

  it('opens the tooltip again once tooltipDisabled is cleared while focus stays', async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <IconButton label="Settings" tooltipDisabled>
        <Icon />
      </IconButton>
    );
    await user.tab();
    expect(allIconButtonTooltips()).toHaveLength(0);
    rerender(
      <IconButton label="Settings" tooltipDisabled={false}>
        <Icon />
      </IconButton>
    );
    expect(openIconButtonTooltip()).toHaveTextContent('Settings');
  });
});

// DFLT-00206: `busy` marks the button as sending the user's own action.
describe('IconButton busy', () => {
  it('adds aria-busy and the "(submitting)" suffix to the name while busy, keeping the tooltip text', async () => {
    const { rerender } = render(
      <IconButton label="Delete ticket" tooltip="Delete" busy disabled>
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button).toHaveAttribute('aria-label', `Delete ticket${i18n.t('common.submitting')}`);
    expect(button).toHaveAccessibleName(submittingName('Delete ticket'));
    await userEvent.setup().hover(button.parentElement!);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toHaveTextContent(/^Delete$/);

    rerender(
      <IconButton label="Delete ticket" tooltip="Delete" busy={false}>
        <Icon />
      </IconButton>
    );
    expect(button).not.toHaveAttribute('aria-busy');
    expect(button).toHaveAccessibleName('Delete ticket');
  });

  it('keeps the plain label as the tooltip when no tooltip is given', async () => {
    render(
      <IconButton label="Reopen" busy disabled>
        <Icon />
      </IconButton>
    );
    const button = screen.getByRole('button');
    expect(button).toHaveAccessibleName(submittingName('Reopen'));
    await userEvent.setup().hover(button.parentElement!);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toHaveTextContent(/^Reopen$/);
  });

  it('is not busy by default', () => {
    render(
      <IconButton label="Settings">
        <Icon />
      </IconButton>
    );
    expect(screen.getByRole('button', { name: 'Settings' })).not.toHaveAttribute('aria-busy');
  });
});

// DFLT-00322: a hover tooltip must not stay open after a tap, nor open on a
// pointer that merely crosses the button, and one press on the tooltip
// pushes it away.
describe('IconButton hover tooltip after touch and crossing pointers', () => {
  type PointerKind = 'mouse' | 'pen' | 'touch';

  // fireEvent's init sets pointerType when jsdom's PointerEvent supports it;
  // otherwise it is defined on the event here, so React reads it either way.
  function firePointer(
    el: Element,
    type: 'pointerover' | 'pointerenter' | 'pointerdown' | 'pointerup' | 'pointerleave' | 'pointerout' | 'pointermove',
    pointerType: PointerKind
  ) {
    const bubbles = type !== 'pointerenter' && type !== 'pointerleave';
    const Ctor = typeof PointerEvent === 'function' ? PointerEvent : MouseEvent;
    const event = new Ctor(type, { bubbles, cancelable: true, pointerType } as PointerEventInit);
    if ((event as PointerEvent).pointerType !== pointerType) {
      Object.defineProperty(event, 'pointerType', { value: pointerType });
    }
    act(() => {
      el.dispatchEvent(event);
    });
  }

  function fireMouse(el: Element, type: 'mouseover' | 'mouseenter' | 'mouseout' | 'mouseleave' | 'mousedown' | 'mouseup' | 'mousemove' | 'click') {
    const bubbles = type !== 'mouseenter' && type !== 'mouseleave';
    act(() => {
      el.dispatchEvent(new MouseEvent(type, { bubbles, cancelable: true }));
    });
  }

  // React derives enter / leave from over / out, so these send those.
  const mouseIn = (el: Element) => fireMouse(el, 'mouseover');
  const mouseOut = (el: Element, to: Element = document.body) =>
    act(() => {
      el.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, cancelable: true, relatedTarget: to }));
    });
  const pointerIn = (el: Element, kind: PointerKind) => firePointer(el, 'pointerover', kind);

  function tap(el: Element) {
    for (const type of ['pointerover', 'pointerenter', 'pointerdown', 'pointerup', 'pointerout', 'pointerleave'] as const) {
      firePointer(el, type, 'touch');
    }
    // The compatibility mouse events a browser sends after a tap.
    for (const type of ['mouseover', 'mouseenter', 'mousemove', 'mousedown', 'mouseup', 'click'] as const) {
      fireMouse(el, type);
    }
  }

  const advance = (ms: number) =>
    act(() => {
      vi.advanceTimersByTime(ms);
    });

  function renderButton(props: Partial<React.ComponentProps<typeof IconButton>> = {}) {
    render(
      <>
        <IconButton label="Language" {...props}>
          <Icon />
        </IconButton>
        <button type="button">outside</button>
      </>
    );
    return {
      button: screen.getByRole('button', { name: 'Language' }),
      outside: screen.getByRole('button', { name: 'outside' })
    };
  }

  describe('with fake timers', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it('reads pointerType from the dispatched pointer events', () => {
      const seen: string[] = [];
      render(<div data-testid="probe" onPointerDown={e => seen.push(e.pointerType)} />);
      firePointer(screen.getByTestId('probe'), 'pointerdown', 'touch');
      expect(seen).toEqual(['touch']);
    });

    it('opens no tooltip on the compatibility mouse events after a tap', () => {
      const { button } = renderButton();
      tap(button);
      expect(openIconButtonTooltips()).toHaveLength(0);
      advance(OPEN_DELAY_MS + 200);
      expect(allIconButtonTooltips()).toHaveLength(0);
    });

    it('opens no tooltip after a long press either', () => {
      const { button } = renderButton();
      firePointer(button, 'pointerover', 'touch');
      firePointer(button, 'pointerenter', 'touch');
      firePointer(button, 'pointerdown', 'touch');
      advance(1500);
      firePointer(button, 'pointerup', 'touch');
      fireMouse(button, 'mouseover');
      fireMouse(button, 'mouseenter');
      expect(openIconButtonTooltips()).toHaveLength(0);
      advance(OPEN_DELAY_MS + 200);
      expect(allIconButtonTooltips()).toHaveLength(0);
    });

    it('still opens on a mouse hover after a tap', () => {
      const { button } = renderButton();
      tap(button);
      mouseOut(button);
      pointerIn(button, 'mouse');
      mouseIn(button);
      advance(OPEN_DELAY_MS);
      expect(openIconButtonTooltip()).toHaveTextContent('Language');
    });

    it('lets a mouse pointermove inside the wrapper overwrite the touch record', () => {
      const { button } = renderButton();
      tap(button);
      mouseOut(button);
      // No pointerover / pointerenter: only a move, then the mouse event.
      firePointer(button, 'pointermove', 'mouse');
      mouseIn(button);
      advance(OPEN_DELAY_MS);
      expect(openIconButtonTooltip()).toHaveTextContent('Language');
    });

    it('does not open on a bare mouseenter after a tap (control for the pointermove case)', () => {
      const { button } = renderButton();
      tap(button);
      mouseOut(button);
      mouseIn(button);
      advance(OPEN_DELAY_MS + 200);
      expect(allIconButtonTooltips()).toHaveLength(0);
    });

    it('closes a mouse-opened tooltip on a touch pointerdown, and no pending timer opens it again', () => {
      const { button } = renderButton();
      pointerIn(button, 'mouse');
      mouseIn(button);
      advance(OPEN_DELAY_MS);
      expect(openIconButtonTooltips()).toHaveLength(1);
      firePointer(button, 'pointerdown', 'touch');
      expect(openIconButtonTooltips()).toHaveLength(0);
      advance(OPEN_DELAY_MS + 200);
      expect(openIconButtonTooltips()).toHaveLength(0);
    });

    it.each(['mouse', 'pen'] as const)('opens on a %s hover after the delay and closes shortly after it leaves', kind => {
      const { button } = renderButton();
      pointerIn(button, kind);
      mouseIn(button);
      expect(openIconButtonTooltips()).toHaveLength(0);
      advance(OPEN_DELAY_MS);
      expect(openIconButtonTooltips()).toHaveLength(1);
      mouseOut(button);
      advance(150);
      expect(openIconButtonTooltips()).toHaveLength(0);
    });

    it('never opens for a pointer that crosses the button faster than the delay', () => {
      const { button, outside } = renderButton();
      pointerIn(button, 'mouse');
      mouseIn(button);
      advance(OPEN_DELAY_MS - 100);
      mouseOut(button, outside);
      advance(OPEN_DELAY_MS + 200);
      // Never rendered at all, so it never opened in between either.
      expect(allIconButtonTooltips()).toHaveLength(0);
    });

    it('stays open without a new delay when the pointer moves from the button onto the tooltip', () => {
      const { button } = renderButton();
      pointerIn(button, 'mouse');
      mouseIn(button);
      advance(OPEN_DELAY_MS);
      const tooltip = openIconButtonTooltip();
      mouseOut(button, tooltip);
      pointerIn(tooltip, 'mouse');
      mouseIn(tooltip);
      advance(150);
      expect(openIconButtonTooltip()).toBe(tooltip);
      mouseOut(tooltip);
      advance(150);
      expect(openIconButtonTooltips()).toHaveLength(0);
    });

    it('closes a hover-opened tooltip on a pointerdown on it, keeping the press from the ancestors', () => {
      const onPointerDown = vi.fn();
      const onClick = vi.fn();
      render(
        <div onPointerDown={onPointerDown} onClick={onClick}>
          <IconButton label="Language">
            <Icon />
          </IconButton>
        </div>
      );
      const button = screen.getByRole('button', { name: 'Language' });
      pointerIn(button, 'mouse');
      mouseIn(button);
      advance(OPEN_DELAY_MS);
      const tooltip = openIconButtonTooltip();
      mouseOut(button, tooltip);
      pointerIn(tooltip, 'mouse');
      mouseIn(tooltip);
      firePointer(tooltip, 'pointerdown', 'mouse');
      expect(openIconButtonTooltips()).toHaveLength(0);
      expect(onPointerDown).not.toHaveBeenCalled();
      advance(OPEN_DELAY_MS + 200);
      expect(openIconButtonTooltips()).toHaveLength(0);
    });
  });

  describe('keyboard focus', () => {
    it('opens on :focus-visible focus after a tap, without delay', async () => {
      const user = userEvent.setup();
      const { button } = renderButton();
      tap(button);
      act(() => button.blur());
      expect(openIconButtonTooltips()).toHaveLength(0);
      await user.tab();
      expect(button).toHaveFocus();
      expect(openIconButtonTooltip()).toHaveTextContent('Language');
    });

    it('opens on :focus-visible focus without the hover delay', async () => {
      const user = userEvent.setup();
      const { button } = renderButton();
      await user.tab();
      expect(button).toHaveFocus();
      expect(openIconButtonTooltip()).toHaveTextContent('Language');
    });

    it('keeps a focus-opened tooltip open on a pointerdown on it', async () => {
      const user = userEvent.setup();
      renderButton();
      await user.tab();
      const tooltip = openIconButtonTooltip();
      firePointer(tooltip, 'pointerdown', 'mouse');
      await new Promise(r => setTimeout(r, 150));
      expect(openIconButtonTooltip()).toBe(tooltip);
    });
  });

  it('opens no tooltip after a mouse click that leaves the button, once the delays have run out', async () => {
    const user = userEvent.setup();
    const { button, outside } = renderButton();
    // A browser does not match :focus-visible on a mouse click's focus;
    // jsdom does, so it is stubbed as in the test above.
    const matches = vi.spyOn(button, 'matches').mockImplementation(sel => sel !== ':focus-visible');
    try {
      await user.click(button);
      await user.hover(outside);
      await waitForHoverOpenDelay();
      expect(openIconButtonTooltips()).toHaveLength(0);
    } finally {
      matches.mockRestore();
    }
  });
});

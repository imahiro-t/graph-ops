// DFLT-00171: the icon-only button with a visible tooltip that replaces the
// native title attribute.
import { createRef, type FormEvent } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { IconButton } from './IconButton';
import i18n from '../i18n';
import { submittingName } from '../test/submittingName';
import { allIconButtonTooltips, openIconButtonTooltip, openIconButtonTooltips } from '../test/iconButtonTooltip';

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
    await user.hover(screen.getByRole('button', { name: 'Delete' }));
    await user.click(openIconButtonTooltip());
    expect(onClick).not.toHaveBeenCalled();
    expect(onMouseDown).not.toHaveBeenCalled();
    expect(onMouseUp).not.toHaveBeenCalled();
    expect(onPointerDown).not.toHaveBeenCalled();

    // The button's own click still bubbles as before.
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onClick).toHaveBeenCalledTimes(1);
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

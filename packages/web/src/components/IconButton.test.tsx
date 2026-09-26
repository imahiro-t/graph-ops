// DFLT-00171: the icon-only button with a visible tooltip that replaces the
// native title attribute.
import { createRef } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { IconButton } from './IconButton';
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
});

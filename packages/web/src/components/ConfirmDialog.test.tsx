// DFLT-00147: the in-app confirmation dialog that replaces window.confirm.
import React, { useRef, useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDialog } from './ConfirmDialog';

interface HarnessProps {
  onConfirm?: () => void;
  onCancel?: () => void;
  onOuterClick?: () => void;
  tone?: 'default' | 'danger';
  children?: React.ReactNode;
}

// An opener that mounts the dialog while it is pending, inside a clickable
// ancestor (like a ticket card's header).
const Harness: React.FC<HarnessProps> = ({ onConfirm, onCancel, onOuterClick, tone, children }) => {
  const [open, setOpen] = useState(false);
  const fallbackRef = useRef<HTMLDivElement>(null);
  return (
    <div ref={fallbackRef} tabIndex={-1} data-testid="outer" onClick={onOuterClick}>
      <button type="button" onClick={() => setOpen(true)}>
        open
      </button>
      {open && (
        <ConfirmDialog
          title="Title"
          message="Really?"
          confirmLabel="Yes"
          cancelLabel="No"
          tone={tone}
          returnFocusFallbackRef={fallbackRef}
          onConfirm={() => {
            onConfirm?.();
            setOpen(false);
          }}
          onCancel={() => {
            onCancel?.();
            setOpen(false);
          }}
        >
          {children}
        </ConfirmDialog>
      )}
    </div>
  );
};

async function openDialog(props: HarnessProps = {}) {
  const user = userEvent.setup();
  render(<Harness {...props} />);
  await user.click(screen.getByRole('button', { name: 'open' }));
  return user;
}

describe('ConfirmDialog', () => {
  it('is a labelled, described modal dialog with test ids, focused on cancel', async () => {
    await openDialog();
    const dialog = screen.getByRole('dialog', { name: 'Title' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleDescription('Really?');
    expect(dialog).toHaveAttribute('data-testid', 'confirm-dialog');
    expect(screen.getByTestId('confirm-dialog-cancel')).toHaveFocus();
    expect(screen.getByTestId('confirm-dialog-confirm')).toHaveTextContent('Yes');
  });

  it('uses role="alertdialog" for the danger tone', async () => {
    await openDialog({ tone: 'danger' });
    expect(screen.getByRole('alertdialog', { name: 'Title' })).toBeInTheDocument();
  });

  it('calls onConfirm from the confirm button', async () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    const user = await openDialog({ onConfirm, onCancel });
    await user.click(screen.getByTestId('confirm-dialog-confirm'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it.each([
    ['the cancel button', async (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('confirm-dialog-cancel'))],
    ['Escape', async (user: ReturnType<typeof userEvent.setup>) => user.keyboard('{Escape}')],
    ['a click on the overlay', async (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('confirm-dialog-overlay'))]
  ])('calls onCancel from %s and returns focus to the opener', async (_, act) => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    const user = await openDialog({ onConfirm, onCancel });
    await act(user);
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'open' })).toHaveFocus();
  });

  it('does not close on a click inside the panel', async () => {
    const onCancel = vi.fn();
    const user = await openDialog({ onCancel });
    await user.click(screen.getByText('Really?'));
    expect(onCancel).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('keeps its clicks from reaching the ancestor that rendered it', async () => {
    const onOuterClick = vi.fn();
    const user = await openDialog({ onOuterClick });
    onOuterClick.mockClear();
    await user.click(screen.getByText('Really?'));
    await user.click(screen.getByTestId('confirm-dialog-cancel'));
    expect(onOuterClick).not.toHaveBeenCalled();
  });

  it('wraps Tab between its two buttons', async () => {
    const user = await openDialog();
    expect(screen.getByTestId('confirm-dialog-cancel')).toHaveFocus();
    await user.tab();
    expect(screen.getByTestId('confirm-dialog-confirm')).toHaveFocus();
    await user.tab();
    expect(screen.getByTestId('confirm-dialog-cancel')).toHaveFocus();
    await user.tab({ shift: true });
    expect(screen.getByTestId('confirm-dialog-confirm')).toHaveFocus();
  });

  it('renders no extra content without children', async () => {
    await openDialog();
    const dialog = screen.getByRole('dialog');
    expect(Array.from(dialog.children).map(c => c.tagName)).toEqual(['H2', 'P', 'DIV']);
  });

  it('renders children between the message and the buttons, outside the description', async () => {
    await openDialog({ children: <fieldset data-testid="extra"><legend>Pick</legend></fieldset> });
    const dialog = screen.getByRole('dialog', { name: 'Title' });
    const extra = screen.getByTestId('extra');
    expect(dialog).toContainElement(extra);
    expect(extra.previousElementSibling).toBe(screen.getByText('Really?'));
    expect(extra.nextElementSibling).toContainElement(screen.getByTestId('confirm-dialog-cancel'));
    expect(dialog).toHaveAccessibleDescription('Really?');
  });

  it('is rendered outside the ancestor, directly under body', async () => {
    await openDialog();
    expect(screen.getByTestId('outer')).not.toContainElement(screen.getByRole('dialog'));
  });
});

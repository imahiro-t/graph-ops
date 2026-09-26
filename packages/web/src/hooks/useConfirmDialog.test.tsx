// DFLT-00148: Promise-based confirmation through the in-app ConfirmDialog.
import React from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { ConfirmOptions, useConfirmDialog } from './useConfirmDialog';

type ConfirmFn = (options: ConfirmOptions) => Promise<boolean>;

// Exposes confirm() to the test and records every result.
const Harness: React.FC<{ onResult: (value: boolean) => void; exposeConfirm?: (fn: ConfirmFn) => void }> = ({
  onResult,
  exposeConfirm
}) => {
  const { confirm, confirmDialog } = useConfirmDialog();
  exposeConfirm?.(confirm);
  return (
    <div>
      <button
        type="button"
        onClick={async () => {
          onResult(await confirm({ title: 'Delete it', message: 'Really?', tone: 'danger', testIdPrefix: 'test-confirm' }));
        }}
      >
        ask
      </button>
      {confirmDialog}
    </div>
  );
};

async function openDialog() {
  const user = userEvent.setup();
  const onResult = vi.fn();
  let confirmFn: ConfirmFn | undefined;
  const utils = render(<Harness onResult={onResult} exposeConfirm={fn => (confirmFn = fn)} />);
  await user.click(screen.getByRole('button', { name: 'ask' }));
  return { user, onResult, utils, confirm: () => confirmFn! };
}

describe('useConfirmDialog', () => {
  it('renders nothing until confirm() is called', () => {
    render(<Harness onResult={vi.fn()} />);
    expect(screen.queryByTestId('test-confirm')).not.toBeInTheDocument();
  });

  it('opens a ConfirmDialog with the given texts and the default button labels', async () => {
    await openDialog();
    const dialog = screen.getByRole('alertdialog', { name: 'Delete it' });
    expect(dialog).toHaveAccessibleDescription('Really?');
    expect(screen.getByTestId('test-confirm-confirm')).toHaveTextContent(i18n.t('common.confirmDialog.confirm'));
    expect(screen.getByTestId('test-confirm-cancel')).toHaveTextContent(i18n.t('common.confirmDialog.cancel'));
    expect(screen.getByTestId('test-confirm-cancel')).toHaveFocus();
  });

  it('resolves to true on the confirm button and closes, returning focus to the opener', async () => {
    const { user, onResult } = await openDialog();
    await user.click(screen.getByTestId('test-confirm-confirm'));
    expect(onResult).toHaveBeenCalledWith(true);
    expect(screen.queryByTestId('test-confirm')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'ask' })).toHaveFocus();
  });

  it('resolves to false on the cancel button', async () => {
    const { user, onResult } = await openDialog();
    await user.click(screen.getByTestId('test-confirm-cancel'));
    expect(onResult).toHaveBeenCalledWith(false);
    expect(screen.queryByTestId('test-confirm')).not.toBeInTheDocument();
  });

  it('resolves to false on Escape', async () => {
    const { user, onResult } = await openDialog();
    await user.keyboard('{Escape}');
    expect(onResult).toHaveBeenCalledWith(false);
    expect(screen.queryByTestId('test-confirm')).not.toBeInTheDocument();
  });

  it('resolves to false on a click on the overlay', async () => {
    const { user, onResult } = await openDialog();
    await user.click(screen.getByTestId('test-confirm-overlay'));
    expect(onResult).toHaveBeenCalledWith(false);
    expect(screen.queryByTestId('test-confirm')).not.toBeInTheDocument();
  });

  it('resolves a second call made while one is pending to false at once, keeping the first dialog', async () => {
    const { user, onResult, confirm } = await openDialog();
    let second: boolean | undefined;
    await act(async () => {
      second = await confirm()({ title: 'Other', message: 'Other?' });
    });
    expect(second).toBe(false);
    expect(screen.getByRole('alertdialog', { name: 'Delete it' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Other' })).not.toBeInTheDocument();

    await user.click(screen.getByTestId('test-confirm-confirm'));
    expect(onResult).toHaveBeenCalledTimes(1);
    expect(onResult).toHaveBeenCalledWith(true);
  });

  it('can be asked again after a confirmation settles', async () => {
    const { user, onResult } = await openDialog();
    await user.click(screen.getByTestId('test-confirm-cancel'));
    await user.click(screen.getByRole('button', { name: 'ask' }));
    await user.click(screen.getByTestId('test-confirm-confirm'));
    expect(onResult.mock.calls).toEqual([[false], [true]]);
  });

  it('resolves a pending confirmation to false on unmount', async () => {
    const { onResult, utils } = await openDialog();
    await act(async () => {
      utils.unmount();
    });
    expect(onResult).toHaveBeenCalledWith(false);
  });
});

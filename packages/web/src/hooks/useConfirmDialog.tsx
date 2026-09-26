// Promise-based confirmation through the in-app ConfirmDialog (DFLT-00148),
// in place of the synchronous window.confirm the app used to call:
//
//   const { confirm, confirmDialog } = useConfirmDialog();
//   ...
//   if (!(await confirm({ title, message, tone: 'danger' }))) return;
//   ...
//   return <>{...}{confirmDialog}</>;
//
// - confirm() opens the dialog and resolves to true when the confirm button
//   is pressed, and to false on the cancel button, Escape or a click on the
//   overlay. Either way the dialog closes and focus goes back to the element
//   that had it before (see useModalDialog).
// - confirmDialog is the element to render somewhere in the caller's JSX
//   (null while nothing is pending). ConfirmDialog portals itself to
//   document.body, so where it is placed does not matter for layout.
// - While a confirmation is pending another confirm() call resolves to false
//   at once instead of opening a second dialog.
// - If the component unmounts with a confirmation pending, the promise
//   resolves to false, so whatever the caller would have done next (a delete
//   request, say) does not run.
//
// Code after `await confirm(...)` runs with the values the caller's closure
// had when it opened the dialog. That is safe for the current callers: while
// the dialog is open its overlay and Tab wrap keep the rest of the page out
// of reach, so nothing the caller reads can be edited in the meantime.
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '../components/ConfirmDialog';

export interface ConfirmOptions {
  title: string;
  message: string;
  // Default to the generic common.confirmDialog.confirm / .cancel labels.
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: 'default' | 'danger';
  testIdPrefix?: string;
  returnFocusFallbackRef?: React.RefObject<HTMLElement>;
}

interface Pending {
  options: ConfirmOptions;
  resolve: (confirmed: boolean) => void;
}

export function useConfirmDialog(): {
  confirm: (options: ConfirmOptions) => Promise<boolean>;
  confirmDialog: React.ReactElement | null;
} {
  const { t } = useTranslation();
  const [pending, setPending] = useState<Pending | null>(null);
  // Mirrors `pending` synchronously, so a second confirm() in the same tick
  // sees the first one and settle() never resolves the same promise twice.
  const pendingRef = useRef<Pending | null>(null);

  const confirm = useCallback((options: ConfirmOptions) => {
    if (pendingRef.current) return Promise.resolve(false);
    return new Promise<boolean>(resolve => {
      const next = { options, resolve };
      pendingRef.current = next;
      setPending(next);
    });
  }, []);

  const settle = useCallback((confirmed: boolean) => {
    const current = pendingRef.current;
    if (!current) return;
    pendingRef.current = null;
    setPending(null);
    current.resolve(confirmed);
  }, []);

  // Resolve a confirmation left pending at unmount. (Under React.StrictMode
  // this cleanup also runs once right after mounting, when nothing is
  // pending yet, so it does nothing then.)
  useEffect(
    () => () => {
      const current = pendingRef.current;
      pendingRef.current = null;
      current?.resolve(false);
    },
    []
  );

  const options = pending?.options;
  const confirmDialog = options ? (
    <ConfirmDialog
      title={options.title}
      message={options.message}
      confirmLabel={options.confirmLabel ?? t('common.confirmDialog.confirm')}
      cancelLabel={options.cancelLabel ?? t('common.confirmDialog.cancel')}
      tone={options.tone}
      testIdPrefix={options.testIdPrefix}
      returnFocusFallbackRef={options.returnFocusFallbackRef}
      onConfirm={() => settle(true)}
      onCancel={() => settle(false)}
    />
  ) : null;

  return { confirm, confirmDialog };
}

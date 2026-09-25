// A yes/no confirmation in an in-app modal dialog (DFLT-00147), in place of
// the browser's native window.confirm: a native dialog stops browser
// automation and cannot be driven from Testing Library, while this one is
// plain DOM with data-testid hooks ({testIdPrefix}, {testIdPrefix}-confirm,
// {testIdPrefix}-cancel).
//
// Mount it only while the confirmation is pending; it holds no text of its
// own -- the caller passes every string, already translated.
//
// Behaviour, shared with the app's other modals through useModalDialog:
//
// - Focus starts on the cancel button, not the confirm button, so an Enter
//   pressed out of habit does not trigger what is being confirmed.
// - Tab / Shift+Tab stay inside the dialog; Escape, a click on the overlay
//   and the cancel button all call onCancel; the confirm button calls
//   onConfirm.
// - On close focus returns to the element that had it before opening, or to
//   returnFocusFallbackRef when that element is gone or cannot take focus
//   any more (for example a button the caller disabled as the dialog closed).
//
// It is rendered into document.body through a portal so that no ancestor's
// overflow, transform or stacking context can clip or cover it. React events
// still bubble through the React tree (i.e. to the component that rendered
// the dialog, such as a ticket card whose header toggles on click), so the
// overlay stops click propagation.
import React, { useId, useRef } from 'react';
import { createPortal } from 'react-dom';
import { useModalDialog } from '../hooks/useModalDialog';

interface Props {
  title: string;
  message: string;
  confirmLabel: string;
  cancelLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  returnFocusFallbackRef?: React.RefObject<HTMLElement>;
  // 'danger' is for a destructive action: role="alertdialog" and a red
  // confirm button.
  tone?: 'default' | 'danger';
  testIdPrefix?: string;
}

export const ConfirmDialog: React.FC<Props> = ({
  title,
  message,
  confirmLabel,
  cancelLabel,
  onConfirm,
  onCancel,
  returnFocusFallbackRef,
  tone = 'default',
  testIdPrefix = 'confirm-dialog'
}) => {
  const idBase = useId();
  const titleId = `${idBase}-title`;
  const messageId = `${idBase}-message`;
  const cancelRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useModalDialog<HTMLDivElement>({
    onEscape: onCancel,
    initialFocusRef: cancelRef,
    returnFocusFallbackRef
  });

  return createPortal(
    <div
      className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4"
      data-testid={`${testIdPrefix}-overlay`}
      onClick={e => {
        e.stopPropagation();
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <div
        ref={dialogRef}
        role={tone === 'danger' ? 'alertdialog' : 'dialog'}
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={messageId}
        tabIndex={-1}
        data-testid={testIdPrefix}
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-md p-6 shadow-2xl focus:outline-none"
      >
        <h2 id={titleId} className="text-lg font-bold text-slate-900 dark:text-slate-100 mb-4">
          {title}
        </h2>
        <p id={messageId} className="text-sm text-slate-700 dark:text-slate-300 mb-4 whitespace-pre-wrap break-words">
          {message}
        </p>
        <div className="flex justify-end gap-2 pt-2">
          <button
            ref={cancelRef}
            type="button"
            data-testid={`${testIdPrefix}-cancel`}
            onClick={onCancel}
            className="px-4 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-xs font-medium text-slate-700 dark:text-slate-300 transition"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            data-testid={`${testIdPrefix}-confirm`}
            onClick={onConfirm}
            className={`px-4 py-2 rounded-lg text-xs font-semibold text-white shadow-xs transition ${
              tone === 'danger' ? 'bg-red-600 hover:bg-red-700' : 'bg-blue-600 hover:bg-blue-700'
            }`}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>,
    document.body
  );
};

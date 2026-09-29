import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Tag } from 'lucide-react';
import { Label } from '../types';
import { setTicketLabels } from '../lib/labelsApi';
import { renderWbr, splitWbr } from '../lib/wbr';
import { errorMessage } from '../lib/apiError';
import { submittingProps, useSubmittingLabel } from './Submitting';
import { fitPopupHorizontally, rootFontSizePx } from '../lib/popupPlacement';

// The panel's own width: its w-56 (14rem). Keep the two in step --
// LabelSelect.narrowPopup.test.tsx checks that the panel still has w-56.
export const LABEL_PANEL_WIDTH_REM = 14;
// The space kept between the panel and the edges it must stay inside.
const LABEL_PANEL_MARGIN_REM = 0.25;
// Sub-pixel slack for the narrow test, so rounding never turns a panel that
// has its full 14rem into a narrow one.
const NARROW_SLACK_PX = 0.5;

interface PanelPlacement {
  left: number;
  maxWidth: number;
  narrow: boolean;
}

// The edges the panel must stay inside, in viewport coordinates: the inside
// of the nearest ancestor that clips horizontally (the ticket card's
// overflow-clip), intersected with the window.
function panelBounds(anchor: HTMLElement): { left: number; right: number; viewportWidth: number } {
  const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
  let left = 0;
  let right = viewportWidth;
  for (let el = anchor.parentElement; el && el !== document.documentElement; el = el.parentElement) {
    // '' where the style is not computed (jsdom) counts as visible.
    const overflowX = getComputedStyle(el).overflowX;
    if (!overflowX || overflowX === 'visible') continue;
    const rect = el.getBoundingClientRect();
    const innerLeft = rect.left + el.clientLeft;
    left = Math.max(left, innerLeft);
    right = Math.min(right, innerLeft + el.clientWidth);
    break;
  }
  return { left, right, viewportWidth };
}

interface Props {
  ticketId: string;
  // The ticket's current labels.
  labels: Label[];
  // The project's registered labels (the choices).
  projectLabels: Label[];
  // Called after a successful save so the parent re-fetches the ticket.
  onSaved: () => void | Promise<void>;
}

// A ticket's label picker (DFLT-00084): an edit button opening a checkbox
// panel with the project's labels, structured like the toolbar filters
// (Escape closes it and returns focus to the button). Every check/uncheck
// immediately PATCHes the ticket's complete label set. While that request is
// in flight the checkboxes are only aria-disabled (and dimmed), and toggle
// ignores them -- never natively `disabled`, because disabling the focused
// checkbox drops keyboard focus to <body>: the next label could then only be
// reached by tabbing in from the top of the page, and Escape (handled on the
// container) would no longer close the panel. Clicks never reach the ticket
// row, so picking labels can't expand/collapse it.
export const LabelSelect: React.FC<Props> = ({ ticketId, labels, projectLabels, onSaved }) => {
  const { t } = useTranslation();
  // The trigger stays usable while labels save (it only toggles the panel),
  // but it shows the spinner, so it carries the submitting state (DFLT-00206).
  const submittingLabel = useSubmittingLabel();
  const [isOpen, setIsOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const buttonRef = useRef<HTMLButtonElement>(null);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [placement, setPlacement] = useState<PanelPlacement | null>(null);

  // Local copy of the selection so a check shows at once, before the parent's
  // re-fetch brings the new labels back in; re-synced whenever they change.
  const labelIdsKey = labels.map(l => l.id).join(',');
  const [selectedIds, setSelectedIds] = useState<string[]>(() => labels.map(l => l.id));
  useEffect(() => {
    setSelectedIds(labelIdsKey === '' ? [] : labelIdsKey.split(','));
  }, [labelIdsKey]);

  const toggle = async (id: string) => {
    if (saving) return;
    // Adding keeps every current id, including one not (yet) in a stale
    // projectLabels; the server decides the display order.
    const next = selectedIds.includes(id) ? selectedIds.filter(x => x !== id) : [...selectedIds, id];
    setSaving(true);
    setError('');
    try {
      await setTicketLabels(t, ticketId, next);
      setSelectedIds(next);
      await onSaved();
    } catch (err) {
      setError(t('ticket.labels.saveError', { message: errorMessage(err, t('errors.UNKNOWN')) }));
    } finally {
      setSaving(false);
    }
  };

  const panelId = `ticket-label-panel-${ticketId}`;

  // DFLT-00293: the panel lines up with the button's left edge and is w-56
  // (14rem), as before, but it is kept inside the ticket card and the
  // window: it is never wider than they leave (less 0.25rem on each side),
  // and moves left when it would end past their right edge (see
  // lib/popupPlacement.ts). In a 160px window at a 200% text size the card
  // is about 110px wide and the card's overflow clip cut the panel off.
  // Worked out before paint when the panel opens, and again while it is
  // open when the window is resized or zoomed (resize) or the wrapper or the
  // page changes size (ResizeObserver, where there is one).
  //
  // Only when the width left is short of 14rem (by more than a sub-pixel)
  // is the panel narrow: it then carries data-narrow, and only then do the
  // group-data-narrow: classes below let the rows wrap and the names wrap
  // in full instead of truncating. The switch is this computed width, not a
  // container query: a container query tests the content box, which in the
  // bordered w-56 panel is 14rem - 2px even at full width, so a 14rem
  // threshold would always match and change the normal-width look. Without
  // data-narrow every class works as before. w-56 stays for the same
  // reason: at full width the panel's look is fixed by it, as before; when
  // narrow, the inline max-width (which wins over width) caps it and the
  // inline left (which wins over left-0) moves it.
  useLayoutEffect(() => {
    if (!isOpen) return;
    const anchor = wrapperRef.current;
    if (!anchor) return;
    const update = () => {
      const rem = rootFontSizePx();
      const preferredWidth = LABEL_PANEL_WIDTH_REM * rem;
      const bounds = panelBounds(anchor);
      const { left, maxWidth } = fitPopupHorizontally({
        viewportWidth: bounds.viewportWidth,
        anchorLeft: anchor.getBoundingClientRect().left,
        preferredWidth,
        margin: LABEL_PANEL_MARGIN_REM * rem,
        boundsLeft: bounds.left,
        boundsRight: bounds.right
      });
      const narrow = maxWidth < preferredWidth - NARROW_SLACK_PX;
      setPlacement(prev =>
        prev && prev.left === left && prev.maxWidth === maxWidth && prev.narrow === narrow ? prev : { left, maxWidth, narrow }
      );
    };
    update();
    window.addEventListener('resize', update);
    const observer = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(update) : null;
    observer?.observe(anchor);
    observer?.observe(document.documentElement);
    return () => {
      window.removeEventListener('resize', update);
      observer?.disconnect();
    };
  }, [isOpen]);

  return (
    // DFLT-00292: the metadata bar's labels item no longer passes
    // wrap-anywhere down, so the button and the save error each carry their
    // own wrapping. flex-wrap sends the error to the next line when it does
    // not fit beside the button (rows are laid out from max-content widths),
    // so the button is never squeezed into mid-word breaks by the error;
    // without an error the button is the only child and nothing changes.
    // min-w-0 max-w-full keep the wrapper itself inside the card.
    <div
      ref={wrapperRef}
      className="relative inline-flex flex-wrap items-center gap-x-2 gap-y-1 min-w-0 max-w-full"
      onClick={e => e.stopPropagation()}
      onKeyDown={e => {
        if (e.key === 'Escape' && isOpen) {
          e.stopPropagation();
          setIsOpen(false);
          buttonRef.current?.focus();
        }
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setIsOpen(v => !v)}
        aria-expanded={isOpen}
        aria-controls={panelId}
        aria-label={submittingLabel(`${t('ticket.labels.edit')}: ${ticketId}`, saving)}
        {...submittingProps(saving)}
        // DFLT-00292: the name wraps between words only ("Edit / labels",
        // "ラベルを / 編集"). wrap-break-word breaks inside a word only when
        // that one word is wider than the button can be (160px / 200%);
        // unlike wrap-anywhere it leaves the min-content width alone, so the
        // flex row cannot squeeze the name into "Edit / labe / ls". The icons
        // are shrink-0 so they keep their size, and flex-wrap moves the name
        // below the icon when the two do not fit on one line: at 160px / 200%
        // the padding and the (rem-sized) icon leave the name about 12px
        // beside the icon, which broke it into single letters. Whenever they
        // fit on one line nothing changes. upto-15rem:rounded-xl (as the
        // status chip does below 80rem) keeps a two- or three-line button a
        // rounded rectangle rather than an ellipse; on one line its radius
        // (0.75rem) is at least half the button's height, so it looks the
        // same as rounded-full.
        className="px-2 py-0.5 rounded-full upto-15rem:rounded-xl border border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-300 hover:text-indigo-600 dark:hover:text-indigo-400 hover:border-indigo-300 dark:hover:border-indigo-700 text-[11px] font-semibold flex flex-wrap items-center gap-1 transition min-w-0 max-w-full wrap-break-word text-left"
      >
        {saving ? <Loader2 className="w-3 h-3 shrink-0 animate-spin" aria-hidden="true" /> : <Tag className="w-3 h-3 shrink-0" aria-hidden="true" />}
        {/* ticket.labels.editVisible is ticket.labels.edit with its break
            opportunities marked: "ラベルを<wbr/>編集" in Japanese, where
            break-keep (word-break: keep-all) otherwise allows no break
            between the characters, so it wraps at "ラベルを / 編集" only.
            The mark is drawn by lib/wbr, as the metadata bar's labels are,
            never rendered as is. The aria-label above still comes from
            ticket.labels.edit. */}
        <span className="min-w-0 break-keep">{renderWbr(splitWbr(t('ticket.labels.editVisible')))}</span>
      </button>

      {/* DFLT-00292: the error is a value -- its message comes from the
          server and may hold one long word (a label or ticket id) -- so it
          breaks inside a word when it cannot fit, instead of running past the
          card. It used to inherit this from the metadata bar's labels item. */}
      {error && (
        <span role="alert" className="text-red-600 dark:text-red-400 font-medium text-[11px] min-w-0 max-w-full wrap-anywhere">
          {error}
        </span>
      )}

      {isOpen && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setIsOpen(false)} />
          <div
            id={panelId}
            role="group"
            aria-label={t('ticket.labels.groupLabel', { id: ticketId })}
            aria-busy={saving}
            data-narrow={placement?.narrow ? '' : undefined}
            style={placement ? { left: placement.left, maxWidth: placement.maxWidth } : undefined}
            className="group absolute left-0 top-full mt-1.5 w-56 max-h-80 overflow-y-auto bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
          >
            {/* DFLT-00293: every class for the narrow panel is behind
                group-data-narrow:, so at full width nothing changes. There
                a row wraps and pads less, the checkbox keeps its size
                (shrink-0, which changes nothing on a one-line row), and the
                name is shown in full, wrapped (breaking inside a word only
                where it has to), instead of truncated. */}
            {projectLabels.length === 0 ? (
              <div className="px-3 py-1.5 text-slate-500 dark:text-slate-400 group-data-narrow:px-2 group-data-narrow:wrap-anywhere">{t('ticket.labels.noRegistered')}</div>
            ) : (
              projectLabels.map(l => (
                <label
                  key={l.id}
                  className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap group-data-narrow:px-2 group-data-narrow:whitespace-normal"
                >
                  <input
                    type="checkbox"
                    checked={selectedIds.includes(l.id)}
                    aria-disabled={saving}
                    onChange={() => toggle(l.id)}
                    className="shrink-0 rounded-sm border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0 aria-disabled:opacity-50"
                  />
                  <span className="truncate group-data-narrow:min-w-0 group-data-narrow:overflow-visible group-data-narrow:whitespace-normal group-data-narrow:text-clip group-data-narrow:wrap-anywhere">{l.name}</span>
                </label>
              ))
            )}
          </div>
        </>
      )}
    </div>
  );
};

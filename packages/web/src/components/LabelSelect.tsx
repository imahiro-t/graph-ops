import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Tag } from 'lucide-react';
import { Label } from '../types';
import { setTicketLabels } from '../lib/labelsApi';
import { withBreaks } from '../lib/wbr';
import { errorMessage } from '../lib/apiError';
import { submittingProps, useSubmittingLabel } from './Submitting';

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
//
// DFLT-00295: the panel is positioned from an anchor that wraps the button
// only, and a save error goes on a line of its own below the labels, so the
// error never moves the button or the panel. placePanel fits the open panel
// into the card (at 160px / 200% its 14rem, 448px, ran far past the card's
// clip), and fits it again whenever the button may have moved while it is
// open: see the layout effect below.
export const LabelSelect: React.FC<Props> = ({ ticketId, labels, projectLabels, onSaved }) => {
  const { t } = useTranslation();
  // The trigger stays usable while labels save (it only toggles the panel),
  // but it shows the spinner, so it carries the submitting state (DFLT-00206).
  const submittingLabel = useSubmittingLabel();
  const [isOpen, setIsOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const buttonRef = useRef<HTMLButtonElement>(null);
  const anchorRef = useRef<HTMLSpanElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const wrapperRef = useRef<HTMLDivElement>(null);

  // Local copy of the selection so a check shows at once, before the parent's
  // re-fetch brings the new labels back in; re-synced whenever they change.
  const labelIdsKey = labels.map(l => l.id).join(',');

  // DFLT-00295: fit the open panel into the card. Recomputed when it opens,
  // and while it is open whenever the button may have moved: when the
  // ticket's labels or the save error change (a label saved with the panel
  // open adds a chip, which can push the button onto the next line -- at
  // 320px the panel kept its old left and ran past the card's left edge), on
  // window resizes, and when the button, the labels item or the clip changes
  // size (ResizeObserver, where there is one: that also catches a change of
  // the browser's text size). Sets the panel's style directly, and only when
  // it has to move or shrink; the panel unmounts on close.
  useLayoutEffect(() => {
    if (!isOpen) return;
    const anchor = anchorRef.current;
    const panel = panelRef.current;
    if (!anchor || !panel) return;
    const place = () => placePanel(anchor, panel);
    place();
    window.addEventListener('resize', place);
    let observer: ResizeObserver | undefined;
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(place);
      // The button's anchor, the row it sits in (the wrapper is display:
      // contents, so its parent) and the clip. The panel is absolute, so
      // fitting it never resizes any of them.
      for (const el of [anchor, wrapperRef.current?.parentElement, clippingAncestor(anchor)]) {
        if (el) observer.observe(el);
      }
    }
    return () => {
      window.removeEventListener('resize', place);
      observer?.disconnect();
    };
  }, [isOpen, labelIdsKey, error]);
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

  return (
    // DFLT-00292: the metadata bar's labels item no longer passes
    // wrap-anywhere down, so the button and the save error each carry their
    // own wrapping, and the button is never squeezed into mid-word breaks by
    // the error.
    // DFLT-00295: the wrapper is display: contents, so the button's anchor
    // and the save error are flex items of the metadata bar's labels item
    // (flex-wrap) on their own. When the wrapper was one inline-flex item,
    // an error widened it, and the whole wrapper -- button included --
    // wrapped to the next line, taking the open panel with it (from 12px to
    // 212px down at 160px / 200%). An item added after the button never
    // moves it. The wrapper only keeps its event handlers (they work
    // through contents).
    <div
      ref={wrapperRef}
      className="contents"
      onClick={e => e.stopPropagation()}
      onKeyDown={e => {
        if (e.key === 'Escape' && isOpen) {
          e.stopPropagation();
          setIsOpen(false);
          buttonRef.current?.focus();
        }
      }}
    >
      {/* DFLT-00295: the panel's anchor (relative) holds the button, the
          overlay and the panel, but not the save error: top-full is the
          button's bottom edge, so an error on the next line leaves the panel
          where it was (it moved from 12px to 212px down). min-w-0 max-w-full
          let the button shrink inside the card as before. */}
      <span ref={anchorRef} className="relative flex min-w-0 max-w-full">
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
              ticket.labels.edit. DFLT-00295: withBreaks draws the mark as
              U+200B in one string, as the metadata bar's labels do. */}
          <span className="min-w-0 break-keep">{withBreaks(t('ticket.labels.editVisible'))}</span>
        </button>

        {isOpen && (
          <>
            <div className="fixed inset-0 z-40" onClick={() => setIsOpen(false)} />
            {/* DFLT-00295: placePanel may narrow the panel below w-56 and move
                it left of left-0. Rows stay on one line; in a narrowed panel a
                long name ends in an ellipsis (min-w-0 lets span.truncate shrink
                below its text) and the checkbox keeps its size (shrink-0).
                With large text on a narrow screen (upto-15rem, where the
                panel is always narrowed) the rows pad with px-2 / gap-1.5:
                at 160px / 200% that leaves a name 43px instead of 23px (one
                letter and the ellipsis). */}
            <div
              ref={panelRef}
              id={panelId}
              role="group"
              aria-label={t('ticket.labels.groupLabel', { id: ticketId })}
              aria-busy={saving}
              className="absolute left-0 top-full mt-1.5 w-56 max-h-80 overflow-y-auto bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
            >
              {projectLabels.length === 0 ? (
                <div className="px-3 upto-15rem:px-2 py-1.5 text-slate-500 dark:text-slate-400">{t('ticket.labels.noRegistered')}</div>
              ) : (
                projectLabels.map(l => (
                  <label
                    key={l.id}
                    className="flex items-center gap-2 px-3 upto-15rem:gap-1.5 upto-15rem:px-2 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
                  >
                    <input
                      type="checkbox"
                      checked={selectedIds.includes(l.id)}
                      aria-disabled={saving}
                      onChange={() => toggle(l.id)}
                      className="shrink-0 rounded-sm border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0 aria-disabled:opacity-50"
                    />
                    <span className="truncate min-w-0">{l.name}</span>
                  </label>
                ))
              )}
            </div>
          </>
        )}
      </span>

      {/* DFLT-00292: the error is a value -- its message comes from the
          server and may hold one long word (a label or ticket id) -- so it
          breaks inside a word when it cannot fit, instead of running past the
          card. It used to inherit this from the metadata bar's labels item.
          DFLT-00295: w-0 min-w-full puts it on a line of its own below the
          button, as wide as the labels item, and adds nothing to the labels
          item's own (max-content) width. Beside the button it widened the
          labels item, which then wrapped onto the next line of the metadata
          bar at 1280px -- moving the button and the open panel about 560px
          left and 24px down under the pointer. The labels item grows into
          the rest of its line while there is an error (TicketItem), so the
          message is not squeezed to the width of the labels. */}
      {error && (
        <span role="alert" className="text-red-600 dark:text-red-400 font-medium text-[11px] w-0 min-w-full max-w-full wrap-anywhere">
          {error}
        </span>
      )}
    </div>
  );
};

// The gap kept between a narrowed or moved panel and the card's clip.
const PANEL_INSET_PX = 4;
// The panel's own width, w-56.
const PANEL_WIDTH_REM = 14;

// The first ancestor of `el` that clips horizontally (overflow-x other than
// visible), or null.
const clippingAncestor = (el: HTMLElement): HTMLElement | null => {
  let clip = el.parentElement;
  while (clip && ['', 'visible'].includes(getComputedStyle(clip).overflowX)) clip = clip.parentElement;
  return clip;
};

// DFLT-00295: fits `panel` (absolute, left-0 w-56 in `anchor`) into the first
// ancestor of `anchor` that clips horizontally -- the ticket card's
// overflow-clip; if another clipping or scrolling element is ever put
// between them, that one becomes the limit. The panel keeps its place and
// width (no inline style) when it fits; otherwise it gets the width of the
// clip's inside less an inset on both sides at most, moved left until its
// right edge is inside, but never past the clip's left edge. Nothing happens
// when there is nothing to measure (no clipping ancestor, or a zero width, as
// in jsdom).
const placePanel = (anchor: HTMLElement, panel: HTMLElement) => {
  panel.style.removeProperty('width');
  panel.style.removeProperty('left');
  const clip = clippingAncestor(anchor);
  if (!clip || clip.clientWidth <= 0) return;
  // The clip's inside (within its borders), less the inset on both sides.
  const min = clip.getBoundingClientRect().left + clip.clientLeft + PANEL_INSET_PX;
  const max = min + clip.clientWidth - 2 * PANEL_INSET_PX;
  const rootFont = parseFloat(getComputedStyle(document.documentElement).fontSize);
  const natural = PANEL_WIDTH_REM * (rootFont > 0 ? rootFont : 16);
  const width = Math.max(0, Math.min(natural, max - min));
  const anchorLeft = anchor.getBoundingClientRect().left;
  const left = Math.max(min, Math.min(anchorLeft, max - width));
  if (width === natural && left === anchorLeft) return;
  panel.style.width = `${width}px`;
  panel.style.left = `${left - anchorLeft}px`;
};

import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Tag } from 'lucide-react';
import { Label } from '../types';
import { setTicketLabels } from '../lib/labelsApi';
import { plainCopyProps } from '../lib/plainCopy';
import { withBreaks } from '../lib/wbr';
import { errorMessage, hasApiErrorCode } from '../lib/apiError';
import { isLaterTimestamp } from '../lib/timestamp';
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

interface Props {
  ticketId: string;
  // The ticket's current labels.
  labels: Label[];
  // The project's registered labels (the choices).
  projectLabels: Label[];
  // The displayed ticket's updated_at, sent as if_updated_at with every
  // label change (DFLT-00330).
  updatedAt: string;
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
// into the card (at 320px / 200% its 14rem, 448px, runs far past the card's
// clip), and fits it again whenever the button may have moved while it is
// open: see the layout effect below. DFLT-00311: that includes a move with
// no change of size (an item before the labels only getting wider), which
// only the check on every animation frame catches.
//
// DFLT-00330: each save sends the full set together with the updated_at of
// the ticket it was built from, so a set built from a stale view can never
// silently undo another member's label change. The value sent is the later
// (as a time) of the ticket's updated_at as displayed and the one this
// component's own last save returned: a second toggle with the panel still
// open must not conflict with the first, and a parent re-fetch that is
// older than that save must not take it back. A 409 TICKET_CHANGED says so,
// reloads the ticket and shows its labels as they are now, so the change
// can be made again on top of them. The priority and the assignee are not
// conditioned (TicketItem): each is a single value the click sets outright.
export const LabelSelect: React.FC<Props> = ({ ticketId, labels, projectLabels, updatedAt, onSaved }) => {
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
  // the browser's text size). placePanel sets the panel's style directly,
  // and only the values that change; the panel unmounts on close.
  //
  // DFLT-00311: none of those fire when the button only moves -- an item
  // before the labels in the metadata bar gets wider (or appears), and the
  // labels item slides right without changing size, so the panel kept its
  // old left and could run past the card's clip. So while it is open, every
  // animation frame compares where the button sits in the clip (the
  // anchor's left edge less the clip's inside left edge) and the clip's
  // inside width -- all that placePanel's result depends on, apart from the
  // root font size -- with their values at the last fit, and fits the panel
  // again only when one of them changed. They are relative, so the page
  // scrolling (the anchor and the clip moving together) rewrites nothing.
  // The root font size is not compared: a change of the text size resizes
  // the button and its row, which the ResizeObserver catches. The fits
  // above record the values too, so the frame after them does not fit a
  // second time. Each frame reads two rects and the clip's clientLeft /
  // clientWidth, no computed style; the loop is cancelled on close and
  // unmount, and browsers pause it in hidden tabs.
  useLayoutEffect(() => {
    if (!isOpen) return;
    const anchor = anchorRef.current;
    const panel = panelRef.current;
    if (!anchor || !panel) return;
    // Found once: the clip (the ticket card) is not expected to change while
    // the panel is open, and looking it up takes a computed style per
    // ancestor, too much for every frame. It is also the one observed below.
    const clip = clippingAncestor(anchor);
    let last = '';
    const place = () => {
      placePanel(anchor, panel, clip);
      if (clip) last = panelFrame(anchor, clip);
    };
    place();
    window.addEventListener('resize', place);
    let observer: ResizeObserver | undefined;
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(place);
      // The button's anchor, the row it sits in (the wrapper is display:
      // contents, so its parent) and the clip. The panel is absolute, so
      // fitting it never resizes any of them.
      for (const el of [anchor, wrapperRef.current?.parentElement, clip]) {
        if (el) observer.observe(el);
      }
    }
    // Without a clip the panel is fitted to the window alone, which the
    // resize listener follows. The panel is not among the compared values,
    // so a fit never sets off another one.
    let frame: number | undefined;
    if (clip && typeof requestAnimationFrame !== 'undefined') {
      const tick = () => {
        if (panelFrame(anchor, clip) !== last) place();
        frame = requestAnimationFrame(tick);
      };
      frame = requestAnimationFrame(tick);
    }
    return () => {
      window.removeEventListener('resize', place);
      observer?.disconnect();
      if (frame !== undefined) cancelAnimationFrame(frame);
    };
  }, [isOpen, labelIdsKey, error]);
  const [selectedIds, setSelectedIds] = useState<string[]>(() => labels.map(l => l.id));
  useEffect(() => {
    setSelectedIds(labelIdsKey === '' ? [] : labelIdsKey.split(','));
  }, [labelIdsKey]);

  // DFLT-00330: the updated_at the next save is conditioned on -- see the
  // component's comment. Only ever moved forward.
  const baseUpdatedAtRef = useRef(updatedAt);
  useEffect(() => {
    if (isLaterTimestamp(updatedAt, baseUpdatedAtRef.current)) baseUpdatedAtRef.current = updatedAt;
  }, [updatedAt]);

  const toggle = async (id: string) => {
    if (saving) return;
    // Adding keeps every current id, including one not (yet) in a stale
    // projectLabels; the server decides the display order.
    const next = selectedIds.includes(id) ? selectedIds.filter(x => x !== id) : [...selectedIds, id];
    setSaving(true);
    setError('');
    try {
      const saved = await setTicketLabels(t, ticketId, next, baseUpdatedAtRef.current);
      if (saved?.updated_at && isLaterTimestamp(saved.updated_at, baseUpdatedAtRef.current)) {
        baseUpdatedAtRef.current = saved.updated_at;
      }
      setSelectedIds(next);
      await onSaved();
    } catch (err) {
      if (hasApiErrorCode(err, 'TICKET_CHANGED')) {
        // Nothing was written. Drop the attempted set and load the ticket
        // as it is now; its labels (and updated_at) come back through the
        // props.
        setSelectedIds(labelIdsKey === '' ? [] : labelIdsKey.split(','));
        setError(t('errors.TICKET_CHANGED'));
        try {
          await onSaved();
        } catch {
          // The reload failing leaves the message; polling reloads later.
        }
      } else {
        setError(t('ticket.labels.saveError', { message: errorMessage(err, t('errors.UNKNOWN')) }));
      }
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
    // wrapped to the next line, taking the open panel with it. An item
    // added after the button never moves it. The wrapper only keeps its
    // event handlers (they work through contents).
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
          // that one word is wider than the button can be;
          // unlike wrap-anywhere it leaves the min-content width alone, so the
          // flex row cannot squeeze the name into "Edit / labe / ls". The icons
          // are shrink-0 so they keep their size, and flex-wrap moves the name
          // below the icon when the two do not fit on one line, rather than
          // breaking it into single letters beside the icon. Whenever they
          // fit on one line nothing changes. upto-15rem:rounded-xl (as the
          // status chip does below 80rem) keeps a two- or three-line button a
          // rounded rectangle rather than an ellipse; on one line its radius
          // (0.75rem) is at least half the button's height, so it looks the
          // same as rounded-full.
          className="px-2 py-0.5 rounded-full upto-15rem:rounded-xl border border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-300 hover:text-indigo-600 dark:hover:text-indigo-400 hover:border-indigo-300 dark:hover:border-indigo-700 text-[0.6875rem] font-semibold flex flex-wrap items-center gap-1 transition min-w-0 max-w-full wrap-break-word text-left"
        >
          {saving ? <Loader2 className="w-3 h-3 shrink-0 animate-spin" aria-hidden="true" /> : <Tag className="w-3 h-3 shrink-0" aria-hidden="true" />}
          {/* ticket.labels.editVisible is ticket.labels.edit with its break
              opportunities marked: "ラベルを<wbr/>編集" in Japanese, where
              break-keep (word-break: keep-all) otherwise allows no break
              between the characters, so it wraps at "ラベルを / 編集" only.
              The mark is drawn by lib/wbr, as the metadata bar's labels are,
              never rendered as is. The aria-label above still comes from
              ticket.labels.edit. DFLT-00295: withBreaks draws the mark as
              U+200B in one string, as the metadata bar's labels do.
              DFLT-00310: plainCopyProps has a copy take it to the clipboard
              without the U+200B (lib/plainCopy). */}
          <span {...plainCopyProps} className="min-w-0 break-keep">{withBreaks(t('ticket.labels.editVisible'))}</span>
        </button>

        {isOpen && (
          <>
            <div className="fixed inset-0 z-40" onClick={() => setIsOpen(false)} />
            {/* DFLT-00295: placePanel fits the panel into the card and the
                window: it sets the inline max-width (which wins over w-56) and
                left (which wins over left-0), and data-narrow when the width
                left is short of 14rem. */}
            <div
              ref={panelRef}
              id={panelId}
              role="group"
              aria-label={t('ticket.labels.groupLabel', { id: ticketId })}
              aria-busy={saving}
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
        <span role="alert" className="text-red-600 dark:text-red-400 font-medium text-[0.6875rem] w-0 min-w-full max-w-full wrap-anywhere">
          {error}
        </span>
      )}
    </div>
  );
};

// The first ancestor of `el` that clips horizontally (overflow-x other than
// visible), or null.
const clippingAncestor = (el: HTMLElement): HTMLElement | null => {
  let clip = el.parentElement;
  while (clip && ['', 'visible'].includes(getComputedStyle(clip).overflowX)) clip = clip.parentElement;
  return clip;
};

// DFLT-00311: what placePanel's result depends on, apart from the root font
// size: the anchor's left edge relative to the clip's inside left edge, and
// the clip's inside width. Compared exactly: the same layout gives the same
// numbers.
const panelFrame = (anchor: HTMLElement, clip: HTMLElement): string => {
  const inside = clip.getBoundingClientRect().left + clip.clientLeft;
  return `${anchor.getBoundingClientRect().left - inside} ${clip.clientWidth}`;
};

// DFLT-00293: the panel lines up with the button's left edge and is w-56
// (14rem), as before, but it is kept inside the ticket card and the window:
// it is never wider than they leave (less 0.25rem on each side), and moves
// left when it would end past their right edge (see lib/popupPlacement.ts).
// In a 320px window at a 200% text size the card is far narrower than 14rem
// (448px), and the card's overflow clip would cut the panel off.
//
// Only when the width left is short of 14rem (by more than a sub-pixel) is
// the panel narrow: it then carries data-narrow, and only then do the
// group-data-narrow: classes let the rows wrap and the names wrap in full
// instead of truncating. The switch is this computed width, not a container
// query: a container query tests the content box, which in the bordered
// w-56 panel is 14rem - 2px even at full width, so a 14rem threshold would
// always match and change the normal-width look. w-56 stays for the same
// reason: at full width the panel's look is fixed by it; when narrow, the
// inline max-width (which wins over width) caps it and the inline left
// (which wins over left-0) moves it.
//
// DFLT-00295: `clip` is the first ancestor of `anchor` (the span holding the
// button and the panel) that clips horizontally -- the ticket card's
// overflow-clip; if another clipping or scrolling element is ever put
// between them, that one becomes the limit. Without one the window alone is
// the limit. The styles are set directly on the panel, and only the ones
// that change. When the clip measures 0 wide there is nothing to measure,
// and the panel keeps its classes' place and width.
const placePanel = (anchor: HTMLElement, panel: HTMLElement, clip: HTMLElement | null) => {
  if (clip && clip.clientWidth <= 0) {
    for (const prop of ['left', 'max-width']) if (panel.style.getPropertyValue(prop)) panel.style.removeProperty(prop);
    panel.removeAttribute('data-narrow');
    return;
  }
  const rem = rootFontSizePx();
  const preferredWidth = LABEL_PANEL_WIDTH_REM * rem;
  const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
  let boundsLeft = 0;
  let boundsRight = viewportWidth;
  if (clip) {
    const inside = clip.getBoundingClientRect().left + clip.clientLeft;
    boundsLeft = Math.max(boundsLeft, inside);
    boundsRight = Math.min(boundsRight, inside + clip.clientWidth);
  }
  const { left, maxWidth } = fitPopupHorizontally({
    viewportWidth,
    anchorLeft: anchor.getBoundingClientRect().left,
    preferredWidth,
    margin: LABEL_PANEL_MARGIN_REM * rem,
    boundsLeft,
    boundsRight
  });
  const narrow = maxWidth < preferredWidth - NARROW_SLACK_PX;
  for (const [prop, value] of [['left', `${left}px`], ['max-width', `${maxWidth}px`]]) {
    if (panel.style.getPropertyValue(prop) !== value) panel.style.setProperty(prop, value);
  }
  panel.toggleAttribute('data-narrow', narrow);
};

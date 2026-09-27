import { ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';

// The one filter control the toolbar's four filters (status / assignee /
// priority / label) are all built from (DFLT-00086). Before this, the label
// filter lived in LabelFilter.tsx while status/assignee/priority were three
// near-identical inline JSX blocks in App.tsx, and their interaction models
// had drifted apart: status/priority started fully checked, so unchecking
// everything meant "match nothing" and emptied the list with no way back,
// and assignee was a radio group with an explicit "All" entry.
//
// The model kept here is the label filter's, because it is the only one
// without a dead end:
//   - an EMPTY selection means "don't filter by this" (every ticket passes),
//     so unchecking everything always widens the list rather than emptying
//     it, and "clear the selection" is the safe exit from any state;
//   - a non-empty selection matches a ticket carrying ANY of the selected
//     values (OR), while separate filters still combine with AND (App.tsx);
//   - the trigger reads "<filter>: All" or "<filter>: N selected" -- the
//     branch lives here, once, so the four filters cannot word it
//     differently (the old code had a third "None selected" state that only
//     status/priority could reach).
//
// The panel is a labelled group of checkboxes, not an ARIA menu, so the
// trigger exposes aria-expanded/aria-controls but not aria-haspopup.
// Toggling a checkbox deliberately keeps the panel open so several values
// can be changed in a row -- including for the assignee filter, which used
// to close on every pick because a radio group only ever takes one.
//
// Next to "clear the selection" the panel has "select all" (DFLT-00264), for
// when almost every value is wanted -- e.g. every status except Done and
// Closed: press it, then uncheck the few that are not wanted. Selecting
// every option deliberately does NOT fold the trigger back to "All": the
// label filter is OR over the selected labels, so checking every label
// still hides the tickets that carry no label at all, which "All" (no
// filtering) shows; and the assignee options follow the 15-second poll, so
// "every option" is only a snapshot of the moment it was pressed -- a new
// assignee showing up later is not in the selection (and "select all"
// becomes pressable again). The trigger keeps saying "N selected" so it
// never claims more than the selection really does.
//
// Class names are written out literally (no `w-${size}` style composition):
// Tailwind only generates classes it can find verbatim in the source.

export interface MultiSelectFilterOption<T extends string> {
  // The stored value; also the React key, so it must be unique per filter.
  value: T;
  // What the panel row shows. A node, not a string, because the label
  // filter draws a colored chip here.
  label: ReactNode;
  // An explicit accessible name for the checkbox, for options whose `label`
  // is not plain text (the label filter's chip). Deliberately optional: for
  // status/priority/assignee the visible text is already a perfectly good
  // name, and adding aria-label there would define the accessible name
  // twice and risk it disagreeing with what is on screen (WCAG 2.5.3,
  // Label in Name). When given, it must contain the visible text verbatim.
  optionLabel?: string;
}

interface Props<T extends string> {
  // Both the panel's id and the trigger's aria-controls (while open). Must be
  // unique on the page -- four of these render side by side in the toolbar --
  // and is also the stem of the overlay's and the trigger's data-testid:
  // tests click "outside" the panel through the former and find the trigger
  // through the latter.
  panelId: string;
  // i18n keys rather than resolved strings: the "All" / "N selected" choice
  // is made here (see above), so callers cannot get the two out of step.
  allKey: string;
  selectedKey: string;
  groupLabelKey: string;
  // Message shown instead of the option rows when there are none (only the
  // label filter can be in that state: a project with no labels yet).
  emptyKey?: string;
  options: readonly MultiSelectFilterOption<T>[];
  // [] means "no filtering by this"; never null/undefined.
  selected: readonly T[];
  onChange: (next: T[]) => void;
}

// How the open panel is positioned against its trigger (DFLT-00220): hung
// from the trigger's left or right edge, or -- when neither fits the window
// -- shifted by `left` px from the trigger's left edge.
type PanelPlacement = { edge: 'left' } | { edge: 'right' } | { edge: 'shift'; left: number };

const HANG_LEFT: PanelPlacement = { edge: 'left' };

// The panel's two footer buttons ("select all" and "clear", DFLT-00264),
// laid out by the footer row's `flex flex-wrap`. flex-auto (flex: 1 1 auto)
// makes each button's flex-basis its own text width, so the two share one
// line while their texts fit side by side, and otherwise the second wraps to
// a line of its own -- where flex-auto's grow stretches it full width. Do not
// use flex-1 or min-w-0 here: flex-1's basis is 0, so the row never wraps and
// each button is squeezed into half the width, breaking its text.
// break-keep [overflow-wrap:anywhere] is the trigger's policy (DFLT-00239):
// keep-all leaves no break point inside "すべて選択" / "選択を解除" (they
// contain no spaces), so a button that does not fit moves to the next line
// whole; only if a single button is still wider than the panel (200% text on
// a 320px screen) does overflow-wrap:anywhere break it between characters,
// instead of widening the panel into a horizontal scroll -- and because
// `anywhere` also lowers the min-content width, the default min-width: auto
// lets the button shrink to the line.
const FOOTER_BUTTON_CLASS =
  'flex-auto break-keep [overflow-wrap:anywhere] text-left px-2 py-1.5 rounded hover:bg-slate-50 dark:hover:bg-slate-800 text-blue-700 dark:text-blue-400 font-medium disabled:opacity-50 disabled:hover:bg-transparent';

// The gap a shifted panel keeps from the window's right edge: the same 1rem
// per side that max-w-[calc(100vw-2rem)] leaves, so a panel capped by that
// max-width still fits with the gap on both sides.
const SHIFT_MARGIN = 16;

// `trigger` is the trigger's viewport box, `panelWidth` the panel's rendered
// width (w-56, capped by max-w), `viewportWidth` the window width excluding
// any scrollbar.
function decidePlacement(
  trigger: { left: number; right: number },
  panelWidth: number,
  viewportWidth: number
): PanelPlacement {
  if (trigger.left + panelWidth <= viewportWidth) return HANG_LEFT;
  if (trigger.right - panelWidth >= 0) return { edge: 'right' };
  // Neither edge works: put the panel's right edge SHIFT_MARGIN inside the
  // window, but never past the window's left edge. Both bounds hold because
  // max-w keeps the panel at most viewportWidth - 2 * SHIFT_MARGIN wide.
  const panelLeft = Math.max(0, viewportWidth - SHIFT_MARGIN - panelWidth);
  return { edge: 'shift', left: panelLeft - trigger.left };
}

function samePlacement(a: PanelPlacement, b: PanelPlacement): boolean {
  if (a.edge !== b.edge) return false;
  return a.edge !== 'shift' || b.edge !== 'shift' || a.left === b.left;
}

export function MultiSelectFilter<T extends string>({
  panelId,
  allKey,
  selectedKey,
  groupLabelKey,
  emptyKey,
  options,
  selected,
  onChange
}: Props<T>) {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  // Where the panel sits relative to its trigger (DFLT-00220). Hung from the
  // trigger's left edge by default; from its right edge when a left-hung
  // panel would run past the right edge of the window; and, when neither
  // fits, shifted by an explicit `left` offset so it lies inside the window.
  // On a narrow screen the toolbar wraps, so a filter can end up near the
  // right edge, and its panel then widened the page and brought in a
  // horizontal scrollbar for as long as it was open.
  const [placement, setPlacement] = useState<PanelPlacement>(HANG_LEFT);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  // Reads only refs and the state setter, so it is stable across renders and
  // the resize listener below can be attached once per opening.
  const place = useCallback(() => {
    const trigger = buttonRef.current?.getBoundingClientRect();
    const panel = panelRef.current?.getBoundingClientRect();
    if (!trigger || !panel) return;
    setPlacement(prev => {
      const next = decidePlacement(trigger, panel.width, document.documentElement.clientWidth);
      return samePlacement(prev, next) ? prev : next;
    });
  }, []);

  // Re-checked after every render while open, not only when opening:
  // checking a box changes the trigger's text ("All" -> "1 selected"), which
  // can re-wrap the toolbar and move the trigger to another line. The result
  // depends only on where the trigger is and how wide the panel is (neither
  // of which the placement changes), so it settles after one pass.
  useLayoutEffect(() => {
    if (isOpen) place();
  });

  useEffect(() => {
    if (!isOpen) return;
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [isOpen, place]);

  const optionValues = options.map(o => o.value);
  // Selected values that are NOT among the current options, in their
  // existing relative order. The assignee options are derived from the loaded
  // tickets and change with the 15-second poll, so a selected name can drop
  // out of the panel; assigneeFilter.ts promises such a selection stays until
  // cleared (DFLT-00087). Values without a checkbox can only be removed by the
  // clear button, so both `toggle` and `selectAll` build their result as
  // "options, in the panel's display order" followed by these -- the one
  // ordering rule, defined here once.
  const outsideOptions = selected.filter(v => !optionValues.includes(v));

  const toggle = (value: T) => {
    if (selected.includes(value)) {
      // Removing already leaves every other value alone, including ones that
      // are no longer among the options.
      onChange(selected.filter(x => x !== value));
      return;
    }
    // Re-derive from the options when adding so the selection always stays
    // in the panel's display order, whatever order the user clicked in, then
    // append `outsideOptions`. Rebuilding from the options alone silently
    // dropped a selected assignee who had left the panel the moment any other
    // box was checked (DFLT-00087).
    onChange([...optionValues.filter(v => v === value || selected.includes(v)), ...outsideOptions]);
  };

  // True when every option is already checked -- and, because `every` of an
  // empty list is true, also when there are no options at all (an empty label
  // filter). Both are the states in which "select all" has nothing to add,
  // so this one expression is its `disabled` condition. Selected values that
  // are not among the options do not count either way.
  const allSelected = optionValues.every(v => selected.includes(v));

  const selectAll = () => {
    // Every option in the panel's display order, then `outsideOptions` --
    // the same ordering rule as `toggle`.
    onChange([...optionValues, ...outsideOptions]);
    // The panel stays open (no setIsOpen): the point is to go on and
    // uncheck the unwanted values. Pressing it always disables it (every
    // option is now checked), and a disabled button drops focus to <body>,
    // outside this component's onKeyDown, where Escape no longer closes the
    // panel (the same trap as the clear button, DFLT-00087). So focus moves,
    // here in the click handler and before React re-renders with
    // `disabled`, to the panel's first checkbox: that is where the next step
    // -- unchecking -- happens, with Tab / Space, and Escape still closes the
    // panel from there. Focusing it scrolls a scrolled panel (max-h-80) back
    // to the top, which suits "now go through the list" too. The button is
    // only enabled with at least one option, so the checkbox exists; the
    // trigger is a fallback that should never be needed.
    const firstCheckbox = panelRef.current?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    (firstCheckbox ?? buttonRef.current)?.focus();
  };

  return (
    // max-w-full / min-w-0 (DFLT-00239): lets this item -- and the trigger
    // inside it -- be narrower than its text when the toolbar line is
    // narrower than the text (200% text size on a 320px screen), instead of
    // pushing the page wider than the window. flex-wrap still moves items to
    // a new line at their full width first, so at 100% nothing shrinks.
    <div
      className="relative max-w-full min-w-0"
      onKeyDown={e => {
        if (e.key === 'Escape' && isOpen) {
          // Closing unmounts the checkbox the user was on, so focus would
          // fall back to <body> unless it is moved somewhere deliberately.
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
        // Only while the panel is mounted: it is rendered conditionally, so
        // a closed trigger pointing at panelId would reference an element
        // that does not exist (DFLT-00087). Tests find the trigger by the
        // testid below instead, named like the overlay's.
        aria-controls={isOpen ? panelId : undefined}
        data-testid={`${panelId}-trigger`}
        className="px-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs font-medium text-slate-700 dark:text-slate-300 flex items-center gap-1.5 max-w-full min-w-0"
      >
        {/* DFLT-00239: the text wraps inside the trigger rather than widening
            the page (WCAG 1.4.10). It used to be whitespace-nowrap, and at
            200% text size on a 320px screen "Assignee: 1 selected" ran up to
            18px past the window. Wrapping rather than truncating keeps the
            count visible, and the button's text stays its accessible name
            (no aria-label, WCAG 2.5.3). break-keep (word-break: keep-all)
            makes Japanese break at the space, like English, instead of
            between any two characters: "ステータス: 1件選択" wraps as
            "ステータス:" / "1件選択" rather than "ステータス: 1" / "件選択".
            overflow-wrap:anywhere then breaks inside a word only when a
            single word is still too wide for the line. */}
        <span className="min-w-0 break-keep [overflow-wrap:anywhere] text-left">
          {selected.length === 0 ? t(allKey) : t(selectedKey, { count: selected.length })}
        </span>
        {/* DFLT-00163: WCAG 1.4.11 (3:1). The arrow shows the button opens a list. slate-500 is
            4.55:1 on the slate-50 button; slate-400 is 5.71:1 on the slate-800 button. */}
        <ChevronDown className="w-3.5 h-3.5 text-slate-500 dark:text-slate-400 shrink-0" aria-hidden="true" />
      </button>

      {isOpen && (
        <>
          {/* Transparent full-screen overlay: an outside click closes the
              panel and is swallowed here, so it can't also activate whatever
              sits underneath. jsdom has no hit testing, so tests have to
              click this element itself -- hence the per-filter testid. */}
          <div
            className="fixed inset-0 z-40"
            data-testid={`${panelId}-overlay`}
            onClick={() => setIsOpen(false)}
          />
          <div
            ref={panelRef}
            id={panelId}
            role="group"
            aria-label={t(groupLabelKey)}
            style={placement.edge === 'shift' ? { left: placement.left } : undefined}
            className={`absolute ${placement.edge === 'right' ? 'right-0' : placement.edge === 'left' ? 'left-0' : ''} mt-1.5 w-56 max-w-[calc(100vw-2rem)] max-h-80 overflow-y-auto bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs`}
          >
            {options.length === 0 && emptyKey && (
              <div className="px-3 py-1.5 text-slate-500 dark:text-slate-400">{t(emptyKey)}</div>
            )}
            {options.map(o => (
              <label
                key={o.value}
                className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
              >
                <input
                  type="checkbox"
                  checked={selected.includes(o.value)}
                  onChange={() => toggle(o.value)}
                  aria-label={o.optionLabel}
                  className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                />
                {o.label}
              </label>
            ))}
            <div className="border-t border-slate-100 dark:border-slate-800 mt-1 pt-1 px-1 flex flex-wrap gap-1">
              {/* "Select all" first, "clear" after it (DFLT-00264); what
                  pressing it does, and where focus goes, is in selectAll. */}
              <button type="button" onClick={selectAll} disabled={allSelected} className={FOOTER_BUTTON_CLASS}>
                {t('toolbar.filterSelectAll')}
              </button>
              {/* The clear button is always rendered (the "select all" button
                  above has its own notes in selectAll), disabled while nothing
                  is selected, even when there are no options at all (an empty
                  label filter). The old LabelFilter hid it in that case;
                  rendering it unconditionally is what makes "every filter's
                  panel has a clear button at the bottom" true by construction
                  instead of per-filter, and a disabled control under "this
                  project has no labels" states the same thing the hidden one
                  left implicit. Covered by MultiSelectFilter.test.tsx.

                  Pressing the clear button disables it (the selection is now
                  empty), and a disabled button drops focus to <body> --
                  outside this component's onKeyDown, so Escape stopped closing
                  the panel (WCAG 2.4.3 / 3.2.2, DFLT-00087). Focus therefore
                  moves to the trigger, which also announces the new "<filter>:
                  All". This happens inside the click handler, before React 18
                  re-renders the batched state update that sets `disabled`, so
                  focus never passes through <body>. aria-disabled with an
                  early return was the alternative; it was not taken because it
                  would keep an inert button in the tab order and break the
                  existing "the clear button is disabled while empty" contract
                  (Gherkin + toBeDisabled() tests). */}
              <button
                type="button"
                onClick={() => {
                  onChange([]);
                  buttonRef.current?.focus();
                }}
                disabled={selected.length === 0}
                className={FOOTER_BUTTON_CLASS}
              >
                {t('toolbar.filterClear')}
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

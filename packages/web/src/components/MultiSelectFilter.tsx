import { ReactNode, useRef, useState } from 'react';
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
  // Both the panel's id and the trigger's aria-controls. Must be unique on
  // the page -- four of these render side by side in the toolbar -- and is
  // also the stem of the overlay's data-testid, which is how tests click
  // "outside" the panel.
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
  const buttonRef = useRef<HTMLButtonElement>(null);

  const toggle = (value: T) => {
    // Re-derive from `options` when adding so the selection always stays in
    // the panel's display order, whatever order the user clicked in.
    onChange(
      selected.includes(value)
        ? selected.filter(x => x !== value)
        : options.map(o => o.value).filter(v => v === value || selected.includes(v))
    );
  };

  return (
    <div
      className="relative"
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
        aria-controls={panelId}
        className="px-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs font-medium text-slate-700 dark:text-slate-300 flex items-center gap-1.5 whitespace-nowrap"
      >
        {selected.length === 0 ? t(allKey) : t(selectedKey, { count: selected.length })}
        <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" aria-hidden="true" />
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
            id={panelId}
            role="group"
            aria-label={t(groupLabelKey)}
            className="absolute left-0 mt-1.5 w-56 max-h-80 overflow-y-auto bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
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
            {/* Always rendered, disabled while nothing is selected, even when
                there are no options at all (an empty label filter). The old
                LabelFilter hid the button in that case; rendering it
                unconditionally is what makes "every filter's panel has a
                clear button at the bottom" true by construction instead of
                per-filter, and a disabled control under "this project has no
                labels" states the same thing the hidden one left implicit.
                Covered by MultiSelectFilter.test.tsx. */}
            <div className="border-t border-slate-100 dark:border-slate-800 mt-1 pt-1 px-1">
              <button
                type="button"
                onClick={() => onChange([])}
                disabled={selected.length === 0}
                className="w-full text-left px-2 py-1.5 rounded hover:bg-slate-50 dark:hover:bg-slate-800 text-blue-700 dark:text-blue-400 font-medium disabled:opacity-50 disabled:hover:bg-transparent"
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

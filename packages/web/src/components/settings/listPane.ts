// Shared classes for the two-column "list + editor" layout used by the node
// types, skills and templates editors (DFLT-00287). The three editors render
// different elements here (a <div> with a ref, a plain <div>, a <nav> with
// aria-labelledby), so what they share is the class strings, not a component.
//
// Keep every value a complete string literal: Tailwind finds classes by
// scanning the source text, so a class assembled at runtime would never be
// generated.
//
// The list's heading is sticky, so the list's scroll padding (scroll-pt-12,
// 3rem -- taller than the heading's 1rem padding + one 0.6875rem line + its
// 1px border; all but the border scale with the root font size, so this
// holds at any font size) keeps an item
// that keyboard focus scrolls into view -- e.g. by Shift+Tab -- below the
// heading instead of under it, and z-10 keeps scrolled rows (and, in the node
// types list, their delete buttons) painted beneath the heading. It matters
// most below 48rem, where the list is capped at max-h-40 and scrolls
// (DFLT-00261 A-1); the padding only affects scroll-into-view, not the layout.

// Root of the editor: list and editor side by side, stacked below 48rem.
export const LIST_LAYOUT_CLASS = 'flex h-full min-h-0 gap-4 narrow:flex-col narrow:h-auto';

// The scrolling list container. The node types and templates lists add
// "flex flex-col" themselves; the skills list does not, so it is left out
// here to keep each list's layout as it was.
export const LIST_PANE_CLASS =
  'w-56 shrink-0 border border-slate-200 dark:border-slate-800 rounded-lg overflow-y-auto scroll-pt-12 bg-slate-50 dark:bg-slate-800 narrow:w-full narrow:max-h-40';

// The list's sticky heading (see the scroll-pt-12 / z-10 note above).
export const LIST_HEADING_CLASS =
  'px-3 py-2 text-[0.6875rem] font-semibold text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700 sticky top-0 z-10 bg-slate-50 dark:bg-slate-800';

// Focus indicator for a list item button. ring-inset draws the ring inside
// the button, so the list's overflow-y-auto does not clip its left and right
// edges (DFLT-00287).
export const LIST_ITEM_FOCUS_CLASS = 'focus:outline-hidden focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-blue-500';

// DFLT-00321: the focus indicator for the native checkboxes in the label
// panel (LabelSelect), the filter panels (MultiSelectFilter) and the review
// gate rows (ReviewGatesEditor). They used to carry a ring-0 focus
// override, which left them with a different (browser default) focus look
// from every other control. Now they show the same 2px blue-500 /
// dark:blue-400 line, only on keyboard focus, as the buttons'
// focus-visible:ring-2 -- drawn as an outline rather than a ring: a native
// checkbox (appearance: auto) does not paint a box-shadow in every browser,
// while an outline is always drawn, and with no offset it sits where ring-2
// would (right on the box's edge). All three rows have padding around the
// box, so the 2px line is not clipped by the panels' overflow.
// focus-visible:outline-solid is needed because focus:outline-hidden (which
// hides the browser's own outline on a mouse click) sets the outline style
// to none, and outline-2 only sets the width.
export const CHECKBOX_FOCUS_CLASS =
  'focus:outline-hidden focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-blue-500 dark:focus-visible:outline-blue-400';

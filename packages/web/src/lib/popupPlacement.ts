// DFLT-00285: where a popup anchored under a button goes horizontally, so
// that it never runs past either edge of the window.
//
// The popup is positioned absolutely against a wrapper (the anchor) and, by
// default, lines up with the anchor's left edge. Its width is capped at the
// window's width less a margin on both sides. Only when the popup would then
// end past the window's right edge (less the margin) does it move left, just
// far enough to end there -- but never so far that its left edge goes past
// the margin on the left.
//
// The result depends on the inputs alone, never on an earlier result: the
// caller passes the popup's own (uncapped) width, not a measurement of the
// popup, which would return the capped width once maxWidth is applied and
// so never grow back when the window is widened.

// The project switcher's popup is w-64, i.e. 16rem. Keep the two in step:
// App.largeTextReflow.test.tsx checks that the popup still has w-64.
export const PROJECT_MENU_WIDTH_REM = 16;

// 0.5rem: the header's padding in its narrowest layout
// ([@media(max-width:200px)]:px-2), so a 160px window with a 32px root font
// still leaves the popup 128px.
export const POPUP_VIEWPORT_MARGIN_REM = 0.5;

export interface PopupPlacementInput {
  // The window's width (document.documentElement.clientWidth), px.
  viewportWidth: number;
  // The anchor's left edge in viewport coordinates, px.
  anchorLeft: number;
  // The popup's own width before any cap, px.
  preferredWidth: number;
  // The space to keep between the popup and each edge of the window, px.
  margin: number;
}

export interface PopupPlacement {
  // The popup's max-width, px (never negative).
  maxWidth: number;
  // The popup's left, relative to the anchor's left edge, px.
  left: number;
}

export function fitPopupHorizontally({ viewportWidth, anchorLeft, preferredWidth, margin }: PopupPlacementInput): PopupPlacement {
  const maxWidth = Math.max(0, viewportWidth - 2 * margin);
  const width = Math.min(preferredWidth, maxWidth);
  let left = 0;
  const rightLimit = viewportWidth - margin;
  if (anchorLeft + width > rightLimit) left = rightLimit - width - anchorLeft;
  if (anchorLeft + left < margin) left = margin - anchorLeft;
  return { maxWidth, left };
}

// The root font size in px (what 1rem is), read fresh on every call so a
// change of the browser's text size or of html's font-size is picked up.
export function rootFontSizePx(): number {
  const size = parseFloat(getComputedStyle(document.documentElement).fontSize);
  return Number.isFinite(size) && size > 0 ? size : 16;
}

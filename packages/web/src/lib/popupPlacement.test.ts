import { describe, expect, it } from 'vitest';
import { fitPopupHorizontally } from './popupPlacement';

// DFLT-00285: the project switcher's popup placement, as a pure function.
describe('fitPopupHorizontally', () => {
  it.each([
    // viewport, anchorLeft, preferred, margin, maxWidth, left
    [1024, 444, 256, 8, 1008, 0],
    [160, 16, 512, 16, 128, 0],
    [400, 300, 256, 8, 384, -164],
    [200, 16, 256, 8, 184, -8],
    [400, 100, 256, 8, 384, 0],
    [400, 100, 512, 16, 368, -84]
  ])('viewport %i, anchor at %i, width %i, margin %i -> maxWidth %i, left %i', (viewportWidth, anchorLeft, preferredWidth, margin, maxWidth, left) => {
    const result = fitPopupHorizontally({ viewportWidth, anchorLeft, preferredWidth, margin });
    expect(result).toEqual({ maxWidth, left });
    const width = Math.min(preferredWidth, result.maxWidth);
    expect(anchorLeft + result.left).toBeGreaterThanOrEqual(margin);
    expect(anchorLeft + result.left + width).toBeLessThanOrEqual(viewportWidth - margin);
  });

  it('keeps the left edge at the margin and the width non-negative for an anchor past the window and a tiny window', () => {
    const wide = fitPopupHorizontally({ viewportWidth: 100, anchorLeft: 500, preferredWidth: 256, margin: 8 });
    expect(wide.maxWidth).toBe(84);
    expect(500 + wide.left).toBe(8);
    const tiny = fitPopupHorizontally({ viewportWidth: 10, anchorLeft: 500, preferredWidth: 256, margin: 8 });
    expect(tiny.maxWidth).toBe(0);
    expect(500 + tiny.left).toBe(8);
  });

  it('does not depend on an earlier result: widening the window gives the full width back', () => {
    const input = { anchorLeft: 16, preferredWidth: 256, margin: 8 };
    const narrow = fitPopupHorizontally({ ...input, viewportWidth: 200 });
    expect(Math.min(256, narrow.maxWidth)).toBe(184);
    expect(narrow.left).toBe(-8);
    const wide = fitPopupHorizontally({ ...input, viewportWidth: 1024 });
    expect(Math.min(256, wide.maxWidth)).toBe(256);
    expect(wide.left).toBe(0);
  });

  // DFLT-00293: bounds other than the window (LabelSelect keeps its popup
  // inside the ticket card, whose overflow clip would cut it off).
  describe('with bounds', () => {
    it('leaving the bounds out is the same as passing the window', () => {
      const input = { viewportWidth: 200, anchorLeft: 16, preferredWidth: 256, margin: 8 };
      expect(fitPopupHorizontally(input)).toEqual(fitPopupHorizontally({ ...input, boundsLeft: 0, boundsRight: 200 }));
    });

    it('caps the width at the bounds less the margin on both sides', () => {
      // 160px window at a 32px root: card inner 25..135, anchor at 41,
      // w-56 = 448px, margin 0.25rem = 8px.
      const p = fitPopupHorizontally({ viewportWidth: 160, anchorLeft: 41, preferredWidth: 448, margin: 8, boundsLeft: 25, boundsRight: 135 });
      expect(p.maxWidth).toBe(94);
      // The popup (94px) ends at 135 - 8 = 127, so it starts at 33.
      expect(41 + p.left).toBe(33);
    });

    it('stays at the anchor when the popup fits inside the bounds', () => {
      const p = fitPopupHorizontally({ viewportWidth: 1280, anchorLeft: 300, preferredWidth: 224, margin: 4, boundsLeft: 100, boundsRight: 1100 });
      expect(p.left).toBe(0);
      expect(p.maxWidth).toBe(992);
    });

    it('moves left to end at the right bound (less the margin), keeping the full width when there is room', () => {
      const p = fitPopupHorizontally({ viewportWidth: 1280, anchorLeft: 900, preferredWidth: 224, margin: 4, boundsLeft: 100, boundsRight: 1000 });
      expect(p.maxWidth).toBeGreaterThanOrEqual(224);
      expect(900 + p.left + 224).toBe(996);
    });

    it('never puts the left edge before the left bound (plus the margin)', () => {
      const p = fitPopupHorizontally({ viewportWidth: 1280, anchorLeft: 90, preferredWidth: 224, margin: 4, boundsLeft: 100, boundsRight: 1000 });
      expect(90 + p.left).toBe(104);
    });

    it('uses the tighter of the card and the window when the caller intersects them', () => {
      // The caller passes max(card left, 0) and min(card right, window width).
      const p = fitPopupHorizontally({ viewportWidth: 300, anchorLeft: 200, preferredWidth: 224, margin: 4, boundsLeft: 20, boundsRight: Math.min(400, 300) });
      expect(p.maxWidth).toBe(272);
      expect(200 + p.left + 224).toBe(296);
    });
  });
});

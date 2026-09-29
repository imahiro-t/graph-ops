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
});

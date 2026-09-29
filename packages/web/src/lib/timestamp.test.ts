// DFLT-00330: updated_at values are compared as times, not strings.
import { describe, expect, it } from 'vitest';
import { isLaterTimestamp } from './timestamp';

describe('isLaterTimestamp', () => {
  it.each([
    ['2026-09-30T00:00:01Z', '2026-09-30T00:00:00Z', true],
    ['2026-09-30T00:00:00Z', '2026-09-30T00:00:01Z', false],
    ['2026-09-30T00:00:01Z', '2026-09-30T00:00:01Z', false],
    // Fewer digits: "…01Z" sorts after "…01.5Z" as text but is earlier.
    ['2026-09-30T00:00:01Z', '2026-09-30T00:00:01.5Z', false],
    ['2026-09-30T00:00:01.5Z', '2026-09-30T00:00:01Z', true],
    ['2026-09-30T00:00:01.5Z', '2026-09-30T00:00:01.25Z', true],
    // Within one millisecond, which a Date cannot tell apart.
    ['2026-09-30T00:00:01.000000002Z', '2026-09-30T00:00:01.000000001Z', true],
    ['2026-09-30T00:00:01.000000001Z', '2026-09-30T00:00:01.000000002Z', false],
    // Offsets are honoured.
    ['2026-09-30T09:00:02+09:00', '2026-09-30T00:00:01Z', true],
    // Something that does not parse loses to one that does.
    ['garbage', '2026-09-30T00:00:01Z', false],
    ['2026-09-30T00:00:01Z', 'garbage', true],
    ['', '', false]
  ])('isLaterTimestamp(%s, %s) = %s', (a, b, want) => {
    expect(isLaterTimestamp(a, b)).toBe(want);
  });
});

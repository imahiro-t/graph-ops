// DFLT-00294: rendered spot checks for the rem text sizes that src/remText.test.ts
// checks in the source. The pending-approval badge is 0.625rem and a label
// chip 0.6875rem -- 10px / 11px at the default 16px, so the default size looks
// as before, and they follow the browser's default font size (WCAG 1.4.4).
// jsdom does no layout, so this checks the classes. The px classes are
// assembled at run time so that Tailwind does not pick them up as
// candidates (a second guard: index.css leaves the test files out since
// DFLT-00323).
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import '../i18n';
import { LabelChip } from './LabelChip';
import { PendingApprovalBadge } from './PendingApprovalBadge';

const pxText = (n: number) => ['text-[', String(n), 'px]'].join('');

afterEach(() => cleanup());

describe('rem text sizes (DFLT-00294)', () => {
  it('sizes the pending-approval badge in rem, not px', () => {
    render(<PendingApprovalBadge count={3} />);
    const badge = screen.getByRole('img');
    expect(badge).toHaveClass('text-[0.625rem]', 'leading-4');
    expect(badge).not.toHaveClass(pxText(10));
  });

  it('sizes a label chip in rem, not px', () => {
    render(<LabelChip name="UI" color="blue" />);
    const chip = screen.getByTestId('label-chip');
    expect(chip).toHaveClass('text-[0.6875rem]', 'leading-4');
    expect(chip).not.toHaveClass(pxText(11));
  });
});

// DFLT-00321: the one spinner every loading / saving indicator uses. It
// keeps the animate-spin class (existing .animate-spin selectors still find
// it), stops turning under prefers-reduced-motion: reduce
// (motion-reduce:animate-none), and is hidden from assistive tech.
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SPIN_CLASS, Spinner } from './Spinner';

describe('Spinner', () => {
  it('spins, but not under reduced motion', () => {
    expect(SPIN_CLASS.split(' ')).toEqual(['animate-spin', 'motion-reduce:animate-none']);
  });

  it('draws a Loader2 icon hidden from assistive tech with the spin classes', () => {
    const { container } = render(<Spinner />);
    const svg = container.querySelector('svg') as SVGElement;
    expect(svg).not.toBeNull();
    expect(svg).toHaveAttribute('aria-hidden', 'true');
    expect(svg).toHaveClass('animate-spin', 'motion-reduce:animate-none', 'lucide-loader-circle');
  });

  it('keeps the size, layout and colour classes it is given', () => {
    const { container } = render(<Spinner className="w-3.5 h-3.5 shrink-0 text-slate-500" />);
    const svg = container.querySelector('svg') as SVGElement;
    expect(svg).toHaveClass('w-3.5', 'h-3.5', 'shrink-0', 'text-slate-500', 'animate-spin', 'motion-reduce:animate-none');
    // Assembled so Tailwind does not generate the unused motion-safe rule.
    expect(svg).not.toHaveClass(['motion-safe', 'animate-spin'].join(':'));
  });
});

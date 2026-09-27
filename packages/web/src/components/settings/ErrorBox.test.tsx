// DFLT-00223: the shared red box the settings editors show errors and
// warnings in. The colors come from ERROR_BOX_CLASS; id, role, aria-hidden
// and the per-box className are passed through unchanged.
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ERROR_BOX_CLASS, ErrorBox } from './ErrorBox';

const COLOR_CLASSES = [
  'bg-red-50',
  'dark:bg-red-950',
  'text-red-700',
  'dark:text-red-300',
  'rounded-lg',
  'border',
  'border-red-200',
  'dark:border-red-800',
];

describe('ErrorBox', () => {
  it('renders its children', () => {
    render(<ErrorBox>Something failed</ErrorBox>);
    expect(screen.getByText('Something failed')).toBeInTheDocument();
  });

  it('carries every shared color class, and only those, without a className', () => {
    render(<ErrorBox>Something failed</ErrorBox>);
    const box = screen.getByText('Something failed');
    for (const cls of COLOR_CLASSES) expect(box).toHaveClass(cls);
    expect(box.className).toBe(ERROR_BOX_CLASS);
  });

  it('keeps padding, size and layout out of the shared classes', () => {
    // Without tailwind-merge, a padding or size in the constant could clash
    // with the caller's; those belong to each box's className alone.
    expect(ERROR_BOX_CLASS).not.toMatch(/(^|\s)(p-|text-\[|text-xs|whitespace-|flex)/);
  });

  it('adds the given className after the shared classes', () => {
    render(<ErrorBox className="p-2.5 text-[11px] whitespace-pre-wrap">Something failed</ErrorBox>);
    const box = screen.getByText('Something failed');
    for (const cls of COLOR_CLASSES) expect(box).toHaveClass(cls);
    expect(box).toHaveClass('p-2.5', 'text-[11px]', 'whitespace-pre-wrap');
    expect(box.className).toBe(`${ERROR_BOX_CLASS} p-2.5 text-[11px] whitespace-pre-wrap`);
  });

  it('passes id and role="alert" through', () => {
    render(
      <ErrorBox id="create-error" role="alert">
        Something failed
      </ErrorBox>
    );
    const box = screen.getByRole('alert');
    expect(box).toHaveTextContent('Something failed');
    expect(box).toHaveAttribute('id', 'create-error');
  });

  it('passes role="note" through', () => {
    render(<ErrorBox role="note">Careful</ErrorBox>);
    expect(screen.getByRole('note')).toHaveTextContent('Careful');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('passes aria-hidden="true" through', () => {
    render(<ErrorBox aria-hidden="true">Announced elsewhere</ErrorBox>);
    expect(screen.getByText('Announced elsewhere')).toHaveAttribute('aria-hidden', 'true');
  });

  it('renders no id, role or aria-hidden attribute when they are omitted', () => {
    render(<ErrorBox>Something failed</ErrorBox>);
    const box = screen.getByText('Something failed');
    expect(box).not.toHaveAttribute('id');
    expect(box).not.toHaveAttribute('role');
    expect(box).not.toHaveAttribute('aria-hidden');
  });
});

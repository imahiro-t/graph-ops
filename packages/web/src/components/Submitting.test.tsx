// DFLT-00206: the shared pieces that make a sending button say so to
// assistive technology -- aria-busy plus a visually hidden "(submitting)" in
// the accessible name, only while busy.
import { act, render, renderHook, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { SubmittingText, submittingProps, useSubmittingLabel } from './Submitting';
import { submittingName } from '../test/submittingName';

afterEach(async () => {
  await act(async () => {
    await i18n.changeLanguage('ja');
  });
});

const Button = ({ busy }: { busy: boolean }) => (
  <button type="button" disabled={busy} {...submittingProps(busy)}>
    <svg aria-hidden="true" />
    Save
    <SubmittingText busy={busy} />
  </button>
);

describe('submittingProps', () => {
  it('sets aria-busy="true" only while busy and leaves the attribute out otherwise', () => {
    expect(submittingProps(true)).toEqual({ 'aria-busy': true });
    expect(submittingProps(false)).toEqual({});
  });
});

describe('SubmittingText', () => {
  it('adds the visually hidden suffix to the name only while busy', () => {
    const { rerender } = render(<Button busy />);
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button).toHaveAccessibleName(submittingName('Save'));
    const hidden = button.querySelector('.sr-only');
    expect(hidden).toHaveTextContent(i18n.t('common.submitting'));

    rerender(<Button busy={false} />);
    expect(button).not.toHaveAttribute('aria-busy');
    expect(button).toHaveAccessibleName('Save');
    expect(button.querySelector('.sr-only')).toBeNull();
  });
});

describe('useSubmittingLabel', () => {
  it('joins the suffix to an aria-label while busy, in ja and en', async () => {
    const { result, rerender } = renderHook(() => useSubmittingLabel());
    expect(result.current('削除', true)).toBe('削除（送信中）');
    expect(result.current('削除', false)).toBe('削除');

    await act(async () => {
      await i18n.changeLanguage('en');
    });
    rerender();
    expect(result.current('Delete', true)).toBe('Delete(submitting)');
    expect(result.current('Delete', false)).toBe('Delete');
  });
});

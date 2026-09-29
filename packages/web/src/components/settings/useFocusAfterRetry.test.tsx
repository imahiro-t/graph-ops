import { useCallback, useRef, useState } from 'react';
import { describe, expect, it } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { useFocusAfterRetry } from './LoadFailure';

// DFLT-00350: the move waits for the target to appear and to be enabled, and
// it does so whether the caller passes getTarget inline (a new function on
// every render) or memoized (the same function on every render).
function Harness({ memoized }: { memoized: boolean }) {
  const [shown, setShown] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const stable = useCallback(() => inputRef.current, []);
  const focusAfterRetry = useFocusAfterRetry(memoized ? stable : () => inputRef.current);
  return (
    <div>
      <button type="button" onClick={focusAfterRetry}>request</button>
      <button type="button" onClick={() => setShown(true)}>show</button>
      <button type="button" onClick={() => setEnabled(true)}>enable</button>
      {shown && <input ref={inputRef} aria-label="target" disabled={!enabled} />}
    </div>
  );
}

describe('useFocusAfterRetry', () => {
  it.each([
    ['inline', false],
    ['memoized', true]
  ])('moves focus once the target is there and enabled (getTarget %s)', (_how, memoized) => {
    render(<Harness memoized={memoized} />);
    act(() => { screen.getByRole('button', { name: 'request' }).click(); });
    // The clicks below do not focus anything (element.click() does not move
    // focus), so focus is still on <body>, as after a retry button unmounts.
    act(() => { screen.getByRole('button', { name: 'show' }).click(); });
    const input = screen.getByRole('textbox', { name: 'target' });
    expect(input).not.toHaveFocus();
    act(() => { screen.getByRole('button', { name: 'enable' }).click(); });
    expect(input).toHaveFocus();
  });
});

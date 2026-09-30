import { useRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { SelectedText, useSelectedTextLoader } from './useSelectedTextLoader';
import { Deferred, deferred } from '../../test/deferred';

// DFLT-00356: the hook's rules tested directly, not only through
// NodeTypesEditor's and SkillsEditor's tests.

type Text = SelectedText & { key: string };

interface HarnessProps {
  selected: string;
  fetchText: (t: unknown, key: string) => Promise<Text>;
  onLoaded: (res: Text) => void;
  onLoadStart?: () => void;
}

function Harness({ selected, fetchText, onLoaded, onLoadStart }: HarnessProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const { pane, failure } = useSelectedTextLoader<Text>({
    selected,
    fetchText,
    onLoaded,
    onLoadStart,
    getFocusTarget: () => textareaRef.current
  });
  return (
    <div>
      <output data-testid="pane">{pane}</output>
      {failure && (
        <div data-testid="failure" data-retrying={String(failure.retrying)} data-failure-key={failure.failureKey}>
          <span data-testid="message">{failure.message}</span>
          <button type="button" onClick={failure.onRetry}>retry</button>
        </div>
      )}
      {pane === 'ready' && <textarea ref={textareaRef} aria-label="text" />}
    </div>
  );
}

// Every fetch of a key gets its own deferred, in call order, so the test
// decides when (and how) each request answers.
function controlledFetch() {
  const calls: { key: string; d: Deferred<Text> }[] = [];
  const fetchText = vi.fn((_t: unknown, key: string) => {
    const d = deferred<Text>();
    calls.push({ key, d });
    return d.promise;
  });
  const text = (key: string): Text => ({ key, tier_text: `${key}-tier`, merged_text: `${key}-merged` });
  const succeed = async (i: number) => {
    await act(async () => {
      calls[i].d.resolve(text(calls[i].key));
      await calls[i].d.promise;
    });
  };
  const fail = async (i: number, message: string) => {
    await act(async () => {
      calls[i].d.reject(new Error(message));
      await calls[i].d.promise.catch(() => {});
    });
  };
  return { calls, fetchText, text, succeed, fail };
}

const pane = () => screen.getByTestId('pane').textContent;
const failure = () => screen.queryByTestId('failure');

describe('useSelectedTextLoader', () => {
  afterEach(() => vi.restoreAllMocks());

  it('does not fetch when nothing is selected', () => {
    const { fetchText } = controlledFetch();
    const onLoaded = vi.fn();
    const onLoadStart = vi.fn();
    render(<Harness selected="" fetchText={fetchText} onLoaded={onLoaded} onLoadStart={onLoadStart} />);
    expect(fetchText).not.toHaveBeenCalled();
    expect(onLoadStart).not.toHaveBeenCalled();
    expect(pane()).toBe('ready');
    expect(failure()).toBeNull();
  });

  it('calls onLoadStart as each fetch starts and onLoaded with the result', async () => {
    const f = controlledFetch();
    const onLoaded = vi.fn();
    const onLoadStart = vi.fn();
    const { rerender } = render(<Harness selected="a" fetchText={f.fetchText} onLoaded={onLoaded} onLoadStart={onLoadStart} />);
    expect(onLoadStart).toHaveBeenCalledTimes(1);
    expect(f.fetchText).toHaveBeenCalledWith(expect.anything(), 'a');
    expect(pane()).toBe('loading');
    await f.succeed(0);
    expect(onLoaded).toHaveBeenCalledExactlyOnceWith(f.text('a'));
    expect(pane()).toBe('ready');
    rerender(<Harness selected="b" fetchText={f.fetchText} onLoaded={onLoaded} onLoadStart={onLoadStart} />);
    expect(onLoadStart).toHaveBeenCalledTimes(2);
    expect(pane()).toBe('loading');
  });

  it('drops a successful answer for an item switched away from', async () => {
    const f = controlledFetch();
    const onLoaded = vi.fn();
    const { rerender } = render(<Harness selected="a" fetchText={f.fetchText} onLoaded={onLoaded} />);
    rerender(<Harness selected="b" fetchText={f.fetchText} onLoaded={onLoaded} />);
    expect(f.calls.map(c => c.key)).toEqual(['a', 'b']);
    await f.succeed(0);
    expect(onLoaded).not.toHaveBeenCalled();
    expect(pane()).toBe('loading');
    await f.succeed(1);
    expect(onLoaded).toHaveBeenCalledExactlyOnceWith(f.text('b'));
    expect(pane()).toBe('ready');
  });

  it('drops a failed answer for an item switched away from', async () => {
    const f = controlledFetch();
    const onLoaded = vi.fn();
    const { rerender } = render(<Harness selected="a" fetchText={f.fetchText} onLoaded={onLoaded} />);
    rerender(<Harness selected="b" fetchText={f.fetchText} onLoaded={onLoaded} />);
    await f.fail(0, 'a failed late');
    expect(failure()).toBeNull();
    expect(pane()).toBe('loading');
    await f.succeed(1);
    expect(pane()).toBe('ready');
    expect(failure()).toBeNull();
  });

  it('shows a failure only while the item it belongs to is selected', async () => {
    const f = controlledFetch();
    const onLoaded = vi.fn();
    const { rerender } = render(<Harness selected="a" fetchText={f.fetchText} onLoaded={onLoaded} />);
    await f.fail(0, 'a failed');
    expect(pane()).toBe('failed');
    expect(screen.getByTestId('message')).toHaveTextContent('a failed');

    // Switching away without clearFailure: a's failure is not shown for b,
    // not even in the render before b's fetch has started.
    rerender(<Harness selected="b" fetchText={f.fetchText} onLoaded={onLoaded} />);
    expect(failure()).toBeNull();
    expect(pane()).toBe('loading');
    await f.succeed(1);
    expect(pane()).toBe('ready');

    // Back on a: a new fetch starts, which clears the old failure.
    rerender(<Harness selected="a" fetchText={f.fetchText} onLoaded={onLoaded} />);
    expect(f.calls.map(c => c.key)).toEqual(['a', 'b', 'a']);
    expect(failure()).toBeNull();
    expect(pane()).toBe('loading');
    await f.succeed(2);
    expect(pane()).toBe('ready');
  });

  it('keeps the failure up while a retry runs, then is ready and moves focus on success', async () => {
    const f = controlledFetch();
    const onLoaded = vi.fn();
    render(<Harness selected="a" fetchText={f.fetchText} onLoaded={onLoaded} />);
    await f.fail(0, 'a failed');
    const retry = screen.getByRole('button', { name: 'retry' });
    retry.focus();
    act(() => { retry.click(); });
    expect(f.calls).toHaveLength(2);
    expect(pane()).toBe('failed');
    expect(failure()).toHaveAttribute('data-retrying', 'true');
    expect(screen.getByTestId('message')).toHaveTextContent('a failed');

    await f.succeed(1);
    expect(pane()).toBe('ready');
    expect(failure()).toBeNull();
    expect(onLoaded).toHaveBeenCalledExactlyOnceWith(f.text('a'));
    expect(screen.getByRole('textbox', { name: 'text' })).toHaveFocus();
  });

  it('updates the message and raises failureKey when a retry fails', async () => {
    const f = controlledFetch();
    render(<Harness selected="a" fetchText={f.fetchText} onLoaded={vi.fn()} />);
    await f.fail(0, 'first failure');
    const key = Number(failure()!.getAttribute('data-failure-key'));
    act(() => { screen.getByRole('button', { name: 'retry' }).click(); });
    await f.fail(1, 'second failure');
    expect(pane()).toBe('failed');
    expect(screen.getByTestId('message')).toHaveTextContent('second failure');
    expect(failure()).toHaveAttribute('data-retrying', 'false');
    expect(Number(failure()!.getAttribute('data-failure-key'))).toBe(key + 1);
  });
});

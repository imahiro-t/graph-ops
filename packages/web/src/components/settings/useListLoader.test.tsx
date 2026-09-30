import { useRef, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { useListLoader } from './useListLoader';
import { Deferred, deferred } from '../../test/deferred';

// DFLT-00356: the list-loading rules NodeTypesEditor and SkillsEditor share,
// tested directly.

function Harness({ fetchList, onReloadError }: { fetchList: () => Promise<string[]>; onReloadError: (m: string) => void }) {
  const [items, setItems] = useState<string[]>([]);
  const paneRef = useRef<HTMLDivElement>(null);
  const [reloadResult, setReloadResult] = useState('');
  // A new inline onLoaded on every render: must not re-fetch the list.
  const list = useListLoader({
    fetchList,
    onLoaded: l => setItems(l),
    onReloadError,
    getFocusTarget: () => paneRef.current
  });
  return (
    <div>
      <button
        type="button"
        onClick={async () => {
          const r = await list.reload();
          setReloadResult(r === null ? 'null' : r.join(','));
        }}
      >
        reload
      </button>
      <output data-testid="reload-result">{reloadResult}</output>
      <div ref={paneRef} tabIndex={-1} data-testid="pane">
        {list.failure ? (
          <div data-testid="failure" data-retrying={String(list.failure.retrying)} data-failure-key={list.failure.failureKey}>
            <span data-testid="message">{list.failure.message}</span>
            <button type="button" onClick={list.failure.onRetry}>retry</button>
          </div>
        ) : !list.loaded ? (
          <span>loading</span>
        ) : (
          <span data-testid="items">{items.join(',')}</span>
        )}
      </div>
    </div>
  );
}

function controlledFetch() {
  const calls: Deferred<string[]>[] = [];
  const fetchList = vi.fn(() => {
    const d = deferred<string[]>();
    calls.push(d);
    return d.promise;
  });
  const succeed = async (i: number, list: string[]) => {
    await act(async () => {
      calls[i].resolve(list);
      await calls[i].promise;
    });
  };
  const fail = async (i: number, message: string) => {
    await act(async () => {
      calls[i].reject(new Error(message));
      await calls[i].promise.catch(() => {});
    });
  };
  return { calls, fetchList, succeed, fail };
}

const failure = () => screen.queryByTestId('failure');

describe('useListLoader', () => {
  it('fetches once on mount and shows the list once loaded', async () => {
    const f = controlledFetch();
    render(<Harness fetchList={f.fetchList} onReloadError={vi.fn()} />);
    expect(screen.getByText('loading')).toBeInTheDocument();
    await f.succeed(0, ['a', 'b']);
    expect(screen.getByTestId('items')).toHaveTextContent('a,b');
    expect(f.fetchList).toHaveBeenCalledTimes(1);
  });

  it('keeps the first failure up while a retry runs, then loads and moves focus to the pane', async () => {
    const f = controlledFetch();
    const onReloadError = vi.fn();
    render(<Harness fetchList={f.fetchList} onReloadError={onReloadError} />);
    await f.fail(0, 'list failed');
    expect(screen.getByTestId('message')).toHaveTextContent('list failed');
    expect(onReloadError).not.toHaveBeenCalled();
    const key = Number(failure()!.getAttribute('data-failure-key'));

    const retry = screen.getByRole('button', { name: 'retry' });
    retry.focus();
    act(() => { retry.click(); });
    expect(failure()).toHaveAttribute('data-retrying', 'true');
    expect(screen.getByTestId('message')).toHaveTextContent('list failed');

    // A failed retry updates the message and raises failureKey.
    await f.fail(1, 'still failing');
    expect(screen.getByTestId('message')).toHaveTextContent('still failing');
    expect(failure()).toHaveAttribute('data-retrying', 'false');
    expect(Number(failure()!.getAttribute('data-failure-key'))).toBe(key + 1);

    const retryAgain = screen.getByRole('button', { name: 'retry' });
    retryAgain.focus();
    act(() => { retryAgain.click(); });
    await f.succeed(2, ['a']);
    expect(failure()).toBeNull();
    expect(screen.getByTestId('items')).toHaveTextContent('a');
    expect(screen.getByTestId('pane')).toHaveFocus();
    expect(onReloadError).not.toHaveBeenCalled();
  });

  it('sends a failed re-fetch after the list has loaded to onReloadError, leaving the list up', async () => {
    const f = controlledFetch();
    const onReloadError = vi.fn();
    render(<Harness fetchList={f.fetchList} onReloadError={onReloadError} />);
    await f.succeed(0, ['a']);
    act(() => { screen.getByRole('button', { name: 'reload' }).click(); });
    await f.fail(1, 'reload failed');
    expect(onReloadError).toHaveBeenCalledExactlyOnceWith('reload failed');
    expect(failure()).toBeNull();
    expect(screen.getByTestId('items')).toHaveTextContent('a');
    expect(screen.getByTestId('reload-result')).toHaveTextContent('null');
  });

  it('resolves reload to the refreshed list', async () => {
    const f = controlledFetch();
    render(<Harness fetchList={f.fetchList} onReloadError={vi.fn()} />);
    await f.succeed(0, ['a']);
    act(() => { screen.getByRole('button', { name: 'reload' }).click(); });
    await f.succeed(1, ['a', 'b']);
    expect(screen.getByTestId('items')).toHaveTextContent('a,b');
    expect(screen.getByTestId('reload-result')).toHaveTextContent('a,b');
    expect(f.fetchList).toHaveBeenCalledTimes(2);
  });
});

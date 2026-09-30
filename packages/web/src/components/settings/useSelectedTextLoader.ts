// Loads the text of the item selected in a list-and-editor settings tab
// (NodeTypesEditor, SkillsEditor) and tells the right-hand pane what to show
// for it (DFLT-00343, DFLT-00350). Both editors share it so that the rules
// below -- easy to get subtly wrong -- live in one place.
//
// - The fetch starts whenever `selected` changes (not for '', an empty
//   list). Only the latest request's answer is used, success or failure: a
//   slower answer for an item switched away from must not land in the next
//   item's editor, where saving it would write one item's text over
//   another's.
// - What the pane shows is derived at render time: `pane` is 'failed' only
//   while the failure belongs to the selected item, and 'loading' while the
//   selected item's text is not in yet. So no frame -- including the one
//   right after a switch, before the effect has started the next fetch --
//   shows the previous item's text or error under the new one.
// - A retry is the same item's fetch again after a failure. It keeps the
//   failure on screen (with its retry button busy) until its own result is
//   in; any other fetch clears the failure when it starts. On success focus
//   moves to getFocusTarget's element (see useFocusAfterRetry); on failure
//   `failure.failureKey` goes up so the alert is announced again.
import { useCallback, useEffect, useRef, useState } from 'react';
import { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { LoadFailureState, useFocusAfterRetry } from './LoadFailure';

export interface SelectedText {
  tier_text: string;
  merged_text: string;
}

interface Options<T extends SelectedText> {
  /** The selected item's key, '' when none is (an empty list). */
  selected: string;
  /** Fetches one item's text. */
  fetchText: (t: TFunction, key: string) => Promise<T>;
  /** Puts a fetched text (the latest request's only) into the editor. */
  onLoaded: (res: T) => void;
  /** Called as each fetch starts, e.g. to clear the editor's own error. */
  onLoadStart?: () => void;
  /** Where focus goes after a successful retry (see useFocusAfterRetry). */
  getFocusTarget: () => HTMLElement | null | undefined;
}

export type SelectedTextFailure = LoadFailureState;

interface Result {
  /** What the pane shows for the selected item. 'ready' also for ''. */
  pane: 'failed' | 'loading' | 'ready';
  /** The selected item's failure, while pane is 'failed'. */
  failure: SelectedTextFailure | null;
  /** Drops any failure; call it when switching to another item. */
  clearFailure: () => void;
}

export function useSelectedTextLoader<T extends SelectedText>({
  selected,
  fetchText,
  onLoaded,
  onLoadStart,
  getFocusTarget
}: Options<T>): Result {
  const { t } = useTranslation();
  // Kept in refs so `load` keeps a stable identity: only a change of
  // `selected` starts a fetch, not a language change or a caller passing
  // new inline callbacks.
  const tRef = useLatest(t);
  const fetchTextRef = useLatest(fetchText);
  const onLoadedRef = useLatest(onLoaded);
  const onLoadStartRef = useLatest(onLoadStart);
  // The item whose text the editor holds, '' while none is. Set only by a
  // successful fetch for the latest request and cleared when a fetch
  // starts, so `selected !== loadedKey` means "not in yet".
  const [loadedKey, setLoadedKey] = useState('');
  // The failure, with the item it belongs to (shown only while that item is
  // selected).
  const [loadError, setLoadError] = useState<{ key: string; message: string } | null>(null);
  const [failures, setFailures] = useState(0);
  // The item a retry is running for, so its busy state is not shown on
  // another item's failure after a switch.
  const [retryingKey, setRetryingKey] = useState<string | null>(null);
  // Numbers the fetches; only the latest one's answer is used.
  const requestRef = useRef(0);
  const focusAfterRetry = useFocusAfterRetry(getFocusTarget);

  // Resolves to whether the text was loaded (and is the latest request's).
  const load = useCallback(async (key: string, retry: boolean): Promise<boolean> => {
    const request = ++requestRef.current;
    setLoadedKey('');
    if (!retry) setLoadError(null);
    onLoadStartRef.current?.();
    try {
      const res = await fetchTextRef.current(tRef.current, key);
      if (request !== requestRef.current) return false;
      onLoadedRef.current(res);
      setLoadedKey(key);
      setLoadError(null);
      return true;
    } catch (e) {
      if (request !== requestRef.current) return false;
      setLoadError({ key, message: errorMessage(e, tRef.current('errors.UNKNOWN')) });
      setFailures(n => n + 1);
      return false;
    }
  }, [tRef, fetchTextRef, onLoadedRef, onLoadStartRef]);

  useEffect(() => { if (selected) load(selected, false); }, [selected, load]);

  const retry = async () => {
    const key = selected;
    setRetryingKey(key);
    const ok = await load(key, true);
    setRetryingKey(prev => (prev === key ? null : prev));
    if (ok) focusAfterRetry();
  };

  const clearFailure = useCallback(() => setLoadError(null), []);

  const current = selected !== '' && loadError?.key === selected ? loadError : null;
  return {
    pane: current ? 'failed' : selected !== '' && selected !== loadedKey ? 'loading' : 'ready',
    failure: current
      ? {
          message: current.message,
          retrying: retryingKey === selected,
          failureKey: failures,
          onRetry: () => void retry()
        }
      : null,
    clearFailure
  };
}

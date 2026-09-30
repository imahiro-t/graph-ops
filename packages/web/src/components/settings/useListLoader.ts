// Loads the item list of a list-and-editor settings tab (NodeTypesEditor,
// SkillsEditor) and tells the right-hand pane whether it can be drawn
// (DFLT-00356; the counterpart of useSelectedTextLoader for the list). Both
// editors share it so that the rules below live in one place.
//
// - The list is fetched once on mount. `loaded` is false until a fetch has
//   succeeded, so the first render already shows the loading line.
// - A fetch that fails while `loaded` is false (the first one, or a retry of
//   it) becomes `failure`, shown with a retry button in place of the editor
//   (see LoadFailure). Once the list has been loaded, a failed re-fetch
//   (`reload`, after a save or delete) goes to onReloadError instead -- the
//   list on screen is still right then, so the editor stays up. Which of the
//   two applies is read from a ref, not from state: a re-fetch that fails
//   in the same render as the first success must still count as a re-fetch.
// - A retry keeps the failure on screen (with its retry button busy) until
//   its own result is in. On success focus moves to getFocusTarget's
//   element (see useFocusAfterRetry); on failure `failure.failureKey` goes
//   up so the alert is announced again.
// - `load` keeps a stable identity (the callbacks and `t` are read through
//   refs), so passing inline callbacks or switching language never re-fetches
//   the list.
// - There is no stale-answer rule, unlike useSelectedTextLoader: the editors
//   never had one for the list, and adding one would change their behavior.
import { useCallback, useEffect, useRef, useState } from 'react';
import { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { LoadFailureState, useFocusAfterRetry } from './LoadFailure';

interface Options<T> {
  /** Fetches the whole list. */
  fetchList: (t: TFunction) => Promise<T[]>;
  /** Puts a fetched list into the editor (and fixes up its selection). */
  onLoaded: (list: T[]) => void;
  /** A failed re-fetch once the list has been loaded (non-blocking). */
  onReloadError: (message: string) => void;
  /** Where focus goes after a successful retry (see useFocusAfterRetry). */
  getFocusTarget: () => HTMLElement | null | undefined;
}

interface Result<T> {
  /** Whether the list has been fetched once. */
  loaded: boolean;
  /** The failure of the first fetch (or its retry), while there is one. */
  failure: LoadFailureState | null;
  /**
   * Fetches the list again (after a save or delete). Resolves to the new
   * list, or null when it could not be fetched (onReloadError has been told,
   * or `failure` set if the list was never loaded).
   */
  reload: () => Promise<T[] | null>;
}

export function useListLoader<T>({ fetchList, onLoaded, onReloadError, getFocusTarget }: Options<T>): Result<T> {
  const { t } = useTranslation();
  const tRef = useLatest(t);
  const fetchListRef = useLatest(fetchList);
  const onLoadedRef = useLatest(onLoaded);
  const onReloadErrorRef = useLatest(onReloadError);
  const [loaded, setLoaded] = useState(false);
  // Mirrors `loaded` for `load`, which must keep a stable identity and must
  // see a success from earlier in the same render (see above).
  const loadedRef = useRef(false);
  const [loadError, setLoadError] = useState('');
  const [failures, setFailures] = useState(0);
  const [retrying, setRetrying] = useState(false);
  const focusAfterRetry = useFocusAfterRetry(getFocusTarget);

  const load = useCallback(async (): Promise<T[] | null> => {
    try {
      const list = await fetchListRef.current(tRef.current);
      onLoadedRef.current(list);
      loadedRef.current = true;
      setLoaded(true);
      setLoadError('');
      return list;
    } catch (e) {
      const message = errorMessage(e, tRef.current('errors.UNKNOWN'));
      if (loadedRef.current) {
        onReloadErrorRef.current(message);
      } else {
        setLoadError(message);
        setFailures(n => n + 1);
      }
      return null;
    }
  }, [tRef, fetchListRef, onLoadedRef, onReloadErrorRef]);

  useEffect(() => { load(); }, [load]);

  const retry = async () => {
    setRetrying(true);
    const list = await load();
    setRetrying(false);
    if (list !== null) focusAfterRetry();
  };

  return {
    loaded,
    failure: loadError
      ? { message: loadError, retrying, failureKey: failures, onRetry: () => void retry() }
      : null,
    reload: load
  };
}

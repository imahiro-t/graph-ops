// DFLT-00359: shared check that a settings editor re-announces a save made
// while its "saved" confirmation is still shown. The live region already
// holds the success text then, so useSavedFlash empties it and puts the text
// back REANNOUNCE_GAP_MS later; setting the same text again would change
// nothing in the DOM and a screen reader would stay silent.
//
// The region's text is recorded with a MutationObserver rather than sampled,
// so the short empty step is seen however the test's timing falls.
import { screen, waitFor } from '@testing-library/react';
import { expect } from 'vitest';
import i18n from '../i18n';

// Records every distinct text the element holds from now on, starting with
// its current text.
export function recordTextHistory(el: Element) {
  const history: string[] = [el.textContent ?? ''];
  const observer = new MutationObserver(() => {
    const text = el.textContent ?? '';
    if (history[history.length - 1] !== text) history.push(text);
  });
  observer.observe(el, { childList: true, characterData: true, subtree: true });
  return { history, stop: () => observer.disconnect() };
}

// Call once the first save has succeeded (its confirmation is on screen).
// `resave` makes the form dirty again and saves; it must not wait for the
// confirmation to go away.
export async function expectSecondSaveReannounced(resave: () => Promise<void>) {
  const success = i18n.t('settings.common.saveSuccess');
  const region = await screen.findByText(success, { selector: '[role="status"]' });
  // It is the always-mounted sr-only region, not the visible notice.
  expect(region).toHaveClass('sr-only');
  const { history, stop } = recordTextHistory(region);
  try {
    await resave();
    // Emptied, then refilled: two changes a screen reader announces.
    await waitFor(() => expect(history).toEqual([success, '', success]));
  } finally {
    stop();
  }
  // The same region, still mounted, and the visible notice stayed up.
  expect(region).toBeInTheDocument();
  expect(screen.getByText(success, { selector: '[aria-hidden="true"]' })).toBeInTheDocument();
}

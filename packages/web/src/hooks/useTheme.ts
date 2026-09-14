import { useEffect, useState } from 'react';

export type ThemePreference = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'graph-ops-theme';

function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function applyTheme(pref: ThemePreference) {
  const isDark = pref === 'dark' || (pref === 'system' && systemPrefersDark());
  document.documentElement.classList.toggle('dark', isDark);
}

function readStoredPreference(): ThemePreference {
  const stored = localStorage.getItem(STORAGE_KEY);
  return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system';
}

// Manages the app's light/dark/system theme preference: persists the choice
// to localStorage and toggles Tailwind's `dark` class (darkMode: 'class' in
// tailwind.config.js) on <html>. index.html runs the same resolution
// synchronously (inline script) before React mounts, so there's no flash of
// the wrong theme on first paint -- this hook keeps the class in sync after
// that and reacts live to OS-level changes while "system" is selected.
export function useTheme() {
  const [preference, setPreferenceState] = useState<ThemePreference>(readStoredPreference);

  useEffect(() => {
    applyTheme(preference);
    if (preference !== 'system') return;
    const mql = window.matchMedia('(prefers-color-scheme: dark)');
    const listener = () => applyTheme('system');
    mql.addEventListener('change', listener);
    return () => mql.removeEventListener('change', listener);
  }, [preference]);

  const setPreference = (next: ThemePreference) => {
    localStorage.setItem(STORAGE_KEY, next);
    setPreferenceState(next);
  };

  return { preference, setPreference };
}

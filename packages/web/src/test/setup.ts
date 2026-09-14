// Vitest setup (see vitest.config.ts's setupFiles). `globals: false` there
// means the jest-dom matchers and afterEach(cleanup) below have to be wired
// up explicitly rather than relying on an auto-injected global environment.
import '@testing-library/jest-dom/vitest';
import { afterEach, beforeAll } from 'vitest';
import { cleanup } from '@testing-library/react';
import i18n from '../i18n';

afterEach(() => {
  cleanup();
});

// Fixes the UI language for every test regardless of this jsdom instance's
// navigator.language/localStorage, so assertions can look up expected text
// via i18n.t('<key>') instead of hardcoding Japanese strings that would
// silently drift from the real translation files.
beforeAll(async () => {
  await i18n.changeLanguage('ja');
});

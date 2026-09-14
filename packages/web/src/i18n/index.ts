import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

// Detects the browser's language (navigator.language) and switches the UI
// between Japanese and English accordingly. Any language other than
// Japanese -- including languages we don't otherwise support -- falls back
// to English (fallbackLng below), per the product's i18n requirements.
//
// A manual choice (the header's language toggle button, see App.tsx) is
// persisted to localStorage (`caches: ['localStorage']`) and takes priority
// on the next load (`order` below checks localStorage before
// querystring/navigator) -- mirroring useTheme's persisted-preference
// pattern -- so switching languages sticks across reloads instead of being
// overridden by the browser's own setting every time.
i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      ja: { translation: ja },
      en: { translation: en }
    },
    fallbackLng: 'en',
    supportedLngs: ['ja', 'en'],
    // Treat "ja-JP" the same as "ja" instead of only matching the exact tag,
    // so regional variants don't fall through to the English fallback.
    nonExplicitSupportedLngs: true,
    load: 'languageOnly',
    detection: {
      // `localStorage` (the header toggle's persisted manual choice, if
      // any) wins over `querystring` (?lng=ja|en, kept for manual/local
      // testing) and `navigator` (the browser's own language setting, the
      // fallback for a first-time visitor who hasn't chosen anything yet).
      order: ['localStorage', 'querystring', 'navigator'],
      lookupQuerystring: 'lng',
      lookupLocalStorage: 'graph-ops-language',
      caches: ['localStorage']
    },
    interpolation: {
      escapeValue: false // React already escapes values
    }
  });

export default i18n;

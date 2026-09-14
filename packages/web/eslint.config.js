import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import globals from 'globals';

// Scope is deliberately narrow (see the `lint` script in package.json,
// which passes explicit paths rather than `.`): this repo's
// tailwind.config.js / postcss.config.js run in a Node/CommonJS context
// that these browser-oriented languageOptions.globals don't match, and
// bringing them into scope here would just add noise unrelated to this
// ticket's subject (react-hooks/exhaustive-deps and no-unused-vars).
export default tseslint.config(
  js.configs.recommended,
  tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}', 'vite.config.ts', 'vitest.config.ts'],
    languageOptions: {
      globals: {
        ...globals.browser
      }
    },
    plugins: {
      'react-hooks': reactHooks
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // ignoreRestSiblings: destructuring out a key to exclude it from the
      // rest object (see ReviewGatesEditor.tsx's `const { id, isOverridden,
      // ...rest } = g`) is an intentional idiom, not an unused binding.
      '@typescript-eslint/no-unused-vars': ['error', { ignoreRestSiblings: true }]
    }
  },
  {
    linterOptions: {
      reportUnusedDisableDirectives: 'error'
    }
  }
);

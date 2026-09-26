// DFLT-00086: the toolbar filters' translation keys, after the four filters
// were unified -- same key shape per filter, one shared clear button, and no
// leftovers from the states that no longer exist.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

const jaToolbar = ja.toolbar as Record<string, string>;
const enToolbar = en.toolbar as Record<string, string>;

describe('toolbar filter translations', () => {
  it('define the same toolbar keys in ja and en', () => {
    expect(Object.keys(jaToolbar).sort()).toEqual(Object.keys(enToolbar).sort());
  });

  it.each(['status', 'assignee', 'priority', 'label'])(
    'gives the %s filter both an "all" and an "N selected" wording',
    prefix => {
      // Capitalized to match the camelCase key spelling (statusAll etc.).
      const suffixed = (s: string) => `${prefix}${s}`;
      for (const key of [suffixed('All'), suffixed('Selected'), suffixed('GroupLabel')]) {
        expect(jaToolbar[key], `ja ${key}`).toBeTruthy();
        expect(enToolbar[key], `en ${key}`).toBeTruthy();
      }
      // Every "N selected" interpolates the count and nothing else, so the
      // four triggers really are the same sentence with a different noun.
      for (const table of [jaToolbar, enToolbar]) {
        expect(table[suffixed('Selected')]).toContain('{{count}}');
      }
    }
  );

  it('shares one clear-button key across the filters and names the unassigned bucket', () => {
    for (const key of ['filterClear', 'assigneeUnassigned']) {
      expect(jaToolbar[key], `ja ${key}`).toBeTruthy();
      expect(enToolbar[key], `en ${key}`).toBeTruthy();
    }
  });

  it('names the search box with a label of its own, distinct from its placeholder', () => {
    // DFLT-00170: the placeholder disappears once the user types, so the
    // search box's accessible name comes from toolbar.searchLabel instead.
    for (const table of [jaToolbar, enToolbar]) {
      expect(table.searchLabel).toBeTruthy();
      expect(table.searchLabel).not.toBe(table.searchPlaceholder);
    }
  });

  it('no longer defines the keys of the removed filter states', () => {
    // statusNone/priorityNone described "nothing checked = match nothing",
    // which no longer happens; labelClear became the shared filterClear.
    for (const key of ['statusNone', 'priorityNone', 'labelClear']) {
      expect(jaToolbar, `ja ${key}`).not.toHaveProperty(key);
      expect(enToolbar, `en ${key}`).not.toHaveProperty(key);
    }
  });

  it('interpolates the assignee trigger by count, not by name', () => {
    // It used to read "Assignee: <name>", which only a single-choice filter
    // can do.
    for (const table of [jaToolbar, enToolbar]) {
      expect(table.assigneeSelected).toContain('{{count}}');
      expect(table.assigneeSelected).not.toContain('{{name}}');
    }
  });
});

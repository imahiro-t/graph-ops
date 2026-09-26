// DFLT-00147: the autopilot start confirmation's translation keys (the
// in-app dialog's title, buttons and text) exist in both languages with the
// same key set.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

type Tree = { [key: string]: string | Tree };

function flatten(tree: Tree, prefix = ''): [string, string][] {
  return Object.entries(tree).flatMap(([key, value]) =>
    typeof value === 'string' ? [[`${prefix}${key}`, value] as [string, string]] : flatten(value, `${prefix}${key}.`)
  );
}

function confirmEntries(tree: Tree): Map<string, string> {
  return new Map(flatten(tree).filter(([key]) => key.startsWith('autopilot.confirm.')));
}

describe('autopilot confirmation translations', () => {
  it('define the same autopilot.confirm keys in ja and en, none of them empty', () => {
    const jaEntries = confirmEntries(ja as Tree);
    const enEntries = confirmEntries(en as Tree);
    expect([...jaEntries.keys()].sort()).toEqual([...enEntries.keys()].sort());
    for (const key of ['title', 'resumeTitle', 'start', 'resumeStart', 'cancel', 'ticket', 'tree', 'resume']) {
      for (const entries of [jaEntries, enEntries]) {
        expect(entries.get(`autopilot.confirm.${key}`)?.trim()).toBeTruthy();
      }
    }
  });
});

// DFLT-00182: the untrusted-folder notice exists in both languages and names
// the folder.
describe('autopilot untrusted-folder translations', () => {
  it('define the notice and its dismiss label in ja and en', () => {
    for (const tree of [ja as Tree, en as Tree]) {
      const entries = new Map(flatten(tree));
      expect(entries.get('autopilot.untrustedFolder')).toContain('{{path}}');
      expect(entries.get('autopilot.untrustedDismiss')?.trim()).toBeTruthy();
    }
  });
});

// DFLT-00084: the label feature's translation keys exist in both languages
// with the same key set.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

type Tree = { [key: string]: string | Tree };

function flatten(tree: Tree, prefix = ''): string[] {
  return Object.entries(tree).flatMap(([key, value]) =>
    typeof value === 'string' ? [`${prefix}${key}`] : flatten(value, `${prefix}${key}.`)
  );
}

function labelKeys(tree: Tree): string[] {
  return flatten(tree)
    .filter(
      key =>
        key === 'settings.tabs.labels' ||
        key.startsWith('settings.labels.') ||
        key.startsWith('labels.colors.') ||
        key.startsWith('ticket.labels.') ||
        key.startsWith('toolbar.label') ||
        ['errors.LABEL_NOT_FOUND', 'errors.LABEL_NAME_TAKEN', 'errors.INVALID_LABEL_NAME', 'errors.INVALID_LABEL_COLOR'].includes(key)
    )
    .sort();
}

describe('label translations', () => {
  it('define the same label keys in ja and en', () => {
    const jaKeys = labelKeys(ja as Tree);
    expect(jaKeys).toEqual(labelKeys(en as Tree));
    for (const required of [
      'settings.tabs.labels',
      'settings.labels.confirmDelete',
      'settings.labels.confirmDeleteInUse',
      'ticket.labels.edit',
      'toolbar.labelAll',
      'toolbar.labelSelected',
      'toolbar.labelGroupLabel',
      // The label panel's clear button now uses toolbar.filterClear, shared
      // with the other three filters (DFLT-00086) -- it is no longer a
      // label-specific key, so filterTranslations.test.ts covers it instead.
      'errors.LABEL_NOT_FOUND',
      'errors.LABEL_NAME_TAKEN',
      'errors.INVALID_LABEL_NAME',
      'errors.INVALID_LABEL_COLOR'
    ]) {
      expect(jaKeys).toContain(required);
    }
    expect(jaKeys.filter(k => k.startsWith('labels.colors.'))).toHaveLength(10);
  });
});

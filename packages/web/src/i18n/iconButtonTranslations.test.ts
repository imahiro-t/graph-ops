// DFLT-00171: the node types editor's add row names its confirm / cancel
// buttons after what they do, in both languages.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

describe('icon button translations', () => {
  it.each(['confirmAddType', 'cancelAddType'] as const)('define settings.nodeTypes.%s in ja and en', key => {
    for (const tree of [ja, en]) {
      const value = tree.settings.nodeTypes[key];
      expect(typeof value).toBe('string');
      expect(value.trim()).not.toBe('');
    }
    expect(ja.settings.nodeTypes[key]).not.toBe(en.settings.nodeTypes[key]);
  });

  it('say what the buttons confirm and cancel', () => {
    expect(ja.settings.nodeTypes.confirmAddType).toBe('ノード種別を追加');
    expect(ja.settings.nodeTypes.cancelAddType).toBe('追加を取り消す');
    expect(en.settings.nodeTypes.confirmAddType).toBe('Add node type');
    expect(en.settings.nodeTypes.cancelAddType).toBe('Cancel adding');
  });
});

// Guards the prompts the Web UI's launch buttons send to Claude Code
// (DFLT-00076). The plugin's skills are only reachable as namespaced slash
// commands (`/graph-ops:<skill>`), and Claude Code only expands a slash
// command at the very start of the input -- so each skill-invoking prompt
// must begin with the namespaced command, in every UI language.
import { afterAll, describe, expect, it } from 'vitest';
import i18n from './index';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

const languages = ['ja', 'en'] as const;
const originalLanguage = i18n.language;

afterAll(async () => {
  await i18n.changeLanguage(originalLanguage);
});

describe.each(languages)('claudePrompts (%s)', (lng) => {
  const t = i18n.getFixedT(lng);

  it('「リファイン」ボタンのプロンプトは /graph-ops:refine-ticket <ticketId> で始まる', () => {
    expect(t('claudePrompts.refineTicket', { ticketId: 'DFLT-00001' })).toMatch(
      /^\/graph-ops:refine-ticket DFLT-00001(\s|$)/
    );
  });

  it('「実行する」ボタンのプロンプトは /graph-ops:process-ticket <ticketId> になる', () => {
    expect(t('claudePrompts.processTicket', { ticketId: 'DFLT-00001' })).toBe(
      '/graph-ops:process-ticket DFLT-00001'
    );
  });

  it('チケット作成のプロンプトは /graph-ops:create-ticket で始まり、タイトルと説明を含む', () => {
    const prompt = t('claudePrompts.createTicket', { title: 'タイトルX', description: '説明Y' });
    expect(prompt).toMatch(/^\/graph-ops:create-ticket\s/);
    expect(prompt).toContain('タイトルX');
    expect(prompt).toContain('説明Y');
  });

  it('名前空間なしのスキルコマンドを含まない', () => {
    const prompts = (lng === 'ja' ? ja : en).claudePrompts as Record<string, string>;
    for (const value of Object.values(prompts)) {
      expect(value).not.toMatch(/(^|[^:\w])\/(create|refine|process)-ticket\b/);
    }
  });
});

it('ja と en で claudePrompts のキーがそろっている', () => {
  expect(Object.keys(ja.claudePrompts).sort()).toEqual(Object.keys(en.claudePrompts).sort());
});

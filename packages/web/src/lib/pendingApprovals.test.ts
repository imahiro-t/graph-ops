import { describe, expect, it } from 'vitest';
import { parsePendingApprovalCounts } from './pendingApprovals';
import en from '../i18n/locales/en/translation.json';
import ja from '../i18n/locales/ja/translation.json';

describe('parsePendingApprovalCounts', () => {
  it('keeps positive integer counts', () => {
    expect(parsePendingApprovalCounts({ counts: { a: 2, b: 1 } })).toEqual({ a: 2, b: 1 });
  });

  it('drops zero, negative, fractional and non-number counts', () => {
    expect(parsePendingApprovalCounts({ counts: { a: 0, b: -1, c: 1.5, d: '3', e: null, f: true, g: 4 } })).toEqual({ g: 4 });
  });

  it.each([null, undefined, 'x', 3, [], {}, { counts: null }, { counts: [1, 2] }, { counts: 'x' }])(
    'returns no counts for %j',
    body => {
      expect(parsePendingApprovalCounts(body)).toEqual({});
    }
  );
});

describe('pending approval translations', () => {
  it('has the badge text in ja and both plural forms in en', () => {
    expect(ja.projectSwitcher.pendingApprovals).toContain('{{count}}');
    expect(en.projectSwitcher.pendingApprovals_one).toContain('{{count}}');
    expect(en.projectSwitcher.pendingApprovals_other).toContain('{{count}}');
  });
});

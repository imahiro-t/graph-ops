import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWbr, splitWbr, WBR_MARK } from './wbr';

describe('wbr marks in translations (DFLT-00292)', () => {
  it('splits a text on its marks, and keeps a text without marks whole', () => {
    expect(splitWbr(`クローズ${WBR_MARK}理由`)).toEqual(['クローズ', '理由']);
    expect(splitWbr('Closed reason')).toEqual(['Closed reason']);
    expect(splitWbr('')).toEqual(['']);
  });

  it('joins words with <wbr> elements and never parses them as markup', () => {
    const { container } = render(<span>{renderWbr(['ラベルを', '<b>編集</b>'])}</span>);
    const span = container.firstElementChild as HTMLElement;
    expect(span.querySelectorAll('wbr')).toHaveLength(1);
    expect(span.querySelector('b')).toBeNull();
    expect(span.textContent).toBe('ラベルを<b>編集</b>');
    expect(span.querySelector('wbr')?.previousSibling?.textContent).toBe('ラベルを');
  });

  it('draws no <wbr> for a single word', () => {
    const { container } = render(<span>{renderWbr(splitWbr('Edit labels'))}</span>);
    expect(container.querySelectorAll('wbr')).toHaveLength(0);
    expect(container.textContent).toBe('Edit labels');
  });
});

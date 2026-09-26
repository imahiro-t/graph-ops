import { describe, expect, it } from 'vitest';
import { focusIfLost, focusKeySelector, neighborAfterRemoval } from './focusAfterRemoval';

describe('neighborAfterRemoval', () => {
  const ids = ['a', 'b', 'c'];

  it('picks the next item when the first one is removed', () => {
    expect(neighborAfterRemoval(ids, 'a', ['b', 'c'])).toBe('b');
  });

  it('picks the next item when a middle one is removed', () => {
    expect(neighborAfterRemoval(ids, 'b', ['a', 'c'])).toBe('c');
  });

  it('picks the previous item when the last one is removed', () => {
    expect(neighborAfterRemoval(ids, 'c', ['a', 'b'])).toBe('b');
  });

  it('returns null when the list is now empty', () => {
    expect(neighborAfterRemoval(['a'], 'a', [])).toBeNull();
  });

  it('picks the first remaining item when the removed one was not in the list', () => {
    expect(neighborAfterRemoval(['b', 'c'], 'a', ['b', 'c'])).toBe('b');
  });

  it('falls back to the last remaining item when the list shrank past the position', () => {
    // Another item disappeared at the same time, so position 2 no longer exists.
    expect(neighborAfterRemoval(ids, 'c', ['a'])).toBe('a');
  });
});

describe('focusKeySelector', () => {
  it('matches an element whose key contains selector metacharacters', () => {
    const el = document.createElement('button');
    const key = 'delete-we"ird]\\name';
    el.setAttribute('data-focus-key', key);
    document.body.appendChild(el);
    try {
      expect(document.querySelector(focusKeySelector(key))).toBe(el);
    } finally {
      el.remove();
    }
  });
});

describe('focusIfLost', () => {
  it('focuses the element when focus is on <body>', () => {
    const el = document.createElement('button');
    document.body.appendChild(el);
    try {
      (document.activeElement as HTMLElement | null)?.blur();
      expect(focusIfLost(el)).toBe(true);
      expect(el).toHaveFocus();
    } finally {
      el.remove();
    }
  });

  it('leaves focus alone when the user has moved it to another element', () => {
    const el = document.createElement('button');
    const other = document.createElement('input');
    document.body.append(el, other);
    try {
      other.focus();
      expect(focusIfLost(el)).toBe(false);
      expect(other).toHaveFocus();
    } finally {
      el.remove();
      other.remove();
    }
  });

  it('does nothing without an element', () => {
    expect(focusIfLost(null)).toBe(false);
  });
});

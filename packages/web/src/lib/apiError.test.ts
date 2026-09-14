import { describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { errorMessage, translateErrorCode } from './apiError';

describe('errorMessage', () => {
  it('reads a thrown Error object message', () => {
    expect(errorMessage(new Error('boom'), 'fallback')).toBe('boom');
  });

  it('falls back when the thrown value has no usable message', () => {
    expect(errorMessage(undefined, 'fallback')).toBe('fallback');
    expect(errorMessage(null, 'fallback')).toBe('fallback');
    expect(errorMessage({}, 'fallback')).toBe('fallback');
    // A message that resolves to '' is not "usable" either.
    expect(errorMessage({ message: '' }, 'fallback')).toBe('fallback');
  });
});

describe('translateErrorCode', () => {
  it('resolves a code that has a dedicated translation', () => {
    // TICKET_NOT_FOUND is one of the backend's documented error codes; any
    // code with a real errors.<CODE> entry proves the non-UNKNOWN path.
    const translated = translateErrorCode(i18n.t.bind(i18n), 'TICKET_NOT_FOUND');
    expect(translated).not.toBe(i18n.t('errors.UNKNOWN'));
  });

  it('falls back to errors.UNKNOWN for a code with no dedicated translation', () => {
    const translated = translateErrorCode(i18n.t.bind(i18n), 'SOME_CODE_THAT_DOES_NOT_EXIST');
    expect(translated).toBe(i18n.t('errors.UNKNOWN'));
  });
});

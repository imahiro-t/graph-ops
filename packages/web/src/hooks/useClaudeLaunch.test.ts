// M-2 regression: useClaudeLaunch's failure message must never interpolate
// "undefined" -- see the hook's `errorMessage(err, t('claudeLaunch.unknownReason'))`
// fallback (this ticket's plan section 4-2, item M-2).
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { useClaudeLaunch } from './useClaudeLaunch';

describe('useClaudeLaunch', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('(a) falls back to unknownReason when the rejection carries no message, never "undefined"', async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce({});

    const { result } = renderHook(() => useClaudeLaunch());

    await act(async () => {
      await result.current.launch('do something');
    });

    await waitFor(() => {
      expect(result.current.lastMessage).toContain(i18n.t('claudeLaunch.unknownReason'));
    });
    expect(result.current.lastMessage).not.toContain('undefined');
  });

  it('(b) uses the localized error code when the response is not OK', async () => {
    (fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: false,
      status: 404,
      json: async () => ({ error: { code: 'TICKET_NOT_FOUND', message: 'ticket not found' } })
    });

    const { result } = renderHook(() => useClaudeLaunch());

    await act(async () => {
      await result.current.launch('do something', 'DFLT-00001');
    });

    await waitFor(() => {
      expect(result.current.lastMessage).toContain(i18n.t('errors.TICKET_NOT_FOUND'));
    });
    expect(result.current.lastMessage).not.toContain('undefined');
  });
});

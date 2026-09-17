// DFLT-00084: the wire shape of the label endpoints.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';

vi.mock('./apiFetch', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './apiFetch';
import { createLabel, deleteLabel, fetchLabels, setTicketLabels, updateLabel } from './labelsApi';

const mockedApiFetch = apiFetch as unknown as ReturnType<typeof vi.fn>;

function lastCall(): { path: string; init: RequestInit | undefined } {
  const [path, init] = mockedApiFetch.mock.calls[mockedApiFetch.mock.calls.length - 1];
  return { path: String(path), init };
}

describe('labelsApi', () => {
  beforeEach(() => {
    mockedApiFetch.mockReset();
    mockedApiFetch.mockImplementation(async () => new Response('{}', { status: 200 }));
  });

  it('lists a project labels with GET', async () => {
    mockedApiFetch.mockImplementationOnce(async () => new Response('[]', { status: 200 }));
    await expect(fetchLabels(i18n.t, 'proj-A')).resolves.toEqual([]);
    expect(lastCall().path).toBe('/api/projects/proj-A/labels');
    expect(lastCall().init?.method).toBeUndefined();
  });

  it('creates with POST {name, color}', async () => {
    await createLabel(i18n.t, 'proj-A', 'バグ', 'red');
    expect(lastCall().path).toBe('/api/projects/proj-A/labels');
    expect(lastCall().init?.method).toBe('POST');
    expect(JSON.parse(String(lastCall().init?.body))).toEqual({ name: 'バグ', color: 'red' });
  });

  it('updates with PATCH carrying only the given fields', async () => {
    await updateLabel(i18n.t, 'label-1', { color: 'orange' });
    expect(lastCall().path).toBe('/api/labels/label-1');
    expect(lastCall().init?.method).toBe('PATCH');
    expect(JSON.parse(String(lastCall().init?.body))).toEqual({ color: 'orange' });
  });

  it('deletes with DELETE', async () => {
    mockedApiFetch.mockImplementationOnce(
      async () => new Response(JSON.stringify({ success: true, removed_ticket_count: 3 }), { status: 200 })
    );
    await expect(deleteLabel(i18n.t, 'label-1')).resolves.toEqual({ success: true, removed_ticket_count: 3 });
    expect(lastCall().path).toBe('/api/labels/label-1');
    expect(lastCall().init?.method).toBe('DELETE');
  });

  it('sets ticket labels with PATCH {label_ids}', async () => {
    await setTicketLabels(i18n.t, 'TEST-00001', ['label-1', 'label-2']);
    expect(lastCall().path).toBe('/api/tickets/TEST-00001');
    expect(lastCall().init?.method).toBe('PATCH');
    expect(JSON.parse(String(lastCall().init?.body))).toEqual({ label_ids: ['label-1', 'label-2'] });
  });

  it('throws the localized message for an error code', async () => {
    mockedApiFetch.mockImplementationOnce(
      async () =>
        new Response(JSON.stringify({ error: { code: 'LABEL_NAME_TAKEN', message: 'taken' } }), { status: 400 })
    );
    await expect(createLabel(i18n.t, 'proj-A', 'bug', 'blue')).rejects.toThrow(i18n.t('errors.LABEL_NAME_TAKEN'));
  });
});

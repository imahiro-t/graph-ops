// Client for the label endpoints (DFLT-00084) -- see
// packages/core-go/internal/httpserver/labels.go. Like settingsApi.ts, every
// function throws an Error carrying the *localized* message for the
// backend's error code (e.g. LABEL_NAME_TAKEN), so components can render
// e.message directly.
import { TFunction } from 'i18next';
import { Label, LabelColor, LabelUsage, Ticket } from '../types';
import { apiFetch } from './apiFetch';
import { ApiCodeError, parseApiError, translateErrorCode } from './apiError';

// Throws an ApiCodeError: its message is the localized one, and its code
// lets a caller tell TICKET_CHANGED apart (DFLT-00330).
async function requestJSON<T>(t: TFunction, path: string, init?: RequestInit): Promise<T> {
  const res = await apiFetch(path, init);
  if (!res.ok) {
    const payload = await parseApiError(res);
    throw new ApiCodeError(payload ? translateErrorCode(t, payload.code) : t('errors.UNKNOWN'), payload?.code ?? '');
  }
  return res.json() as Promise<T>;
}

function jsonBody(method: 'POST' | 'PATCH', payload: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  };
}

export function fetchLabels(t: TFunction, projectId: string): Promise<LabelUsage[]> {
  return requestJSON<LabelUsage[]>(t, `/api/projects/${encodeURIComponent(projectId)}/labels`);
}

export function createLabel(t: TFunction, projectId: string, name: string, color: LabelColor): Promise<Label> {
  return requestJSON<Label>(t, `/api/projects/${encodeURIComponent(projectId)}/labels`, jsonBody('POST', { name, color }));
}

export function updateLabel(
  t: TFunction,
  labelId: string,
  patch: { name?: string; color?: LabelColor }
): Promise<Label> {
  return requestJSON<Label>(t, `/api/labels/${encodeURIComponent(labelId)}`, jsonBody('PATCH', patch));
}

export function deleteLabel(
  t: TFunction,
  labelId: string
): Promise<{ success: boolean; removed_ticket_count: number }> {
  return requestJSON(t, `/api/labels/${encodeURIComponent(labelId)}`, { method: 'DELETE' });
}

// Replaces the ticket's labels with exactly labelIds ([] removes them all).
// ifUpdatedAt (DFLT-00330) is the updated_at of the ticket the new set was
// built from: the server writes nothing and answers 409 TICKET_CHANGED
// (an ApiCodeError with that code) when the ticket has been written since.
// Left out, the labels are replaced unconditionally.
export function setTicketLabels(t: TFunction, ticketId: string, labelIds: string[], ifUpdatedAt?: string): Promise<Ticket> {
  const payload: { label_ids: string[]; if_updated_at?: string } = { label_ids: labelIds };
  if (ifUpdatedAt !== undefined) payload.if_updated_at = ifUpdatedAt;
  return requestJSON<Ticket>(t, `/api/tickets/${encodeURIComponent(ticketId)}`, jsonBody('PATCH', payload));
}

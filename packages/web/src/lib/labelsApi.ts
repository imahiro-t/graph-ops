// Client for the label endpoints (DFLT-00084) -- see
// packages/core-go/internal/httpserver/labels.go. Like settingsApi.ts, every
// function throws an Error carrying the *localized* message for the
// backend's error code (e.g. LABEL_NAME_TAKEN), so components can render
// e.message directly.
import { TFunction } from 'i18next';
import { Label, LabelColor, LabelUsage, Ticket } from '../types';
import { apiFetch } from './apiFetch';
import { localizedApiErrorMessage } from './apiError';

async function requestJSON<T>(t: TFunction, path: string, init?: RequestInit): Promise<T> {
  const res = await apiFetch(path, init);
  if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
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
export function setTicketLabels(t: TFunction, ticketId: string, labelIds: string[]): Promise<Ticket> {
  return requestJSON<Ticket>(t, `/api/tickets/${encodeURIComponent(ticketId)}`, jsonBody('PATCH', { label_ids: labelIds }));
}

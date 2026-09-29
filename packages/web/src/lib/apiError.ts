import { TFunction } from 'i18next';

// Shape of an error response from the Go backend (see
// packages/core-go/internal/httpserver/server.go's writeError / APIError):
// {"error": {"code": "TICKET_NOT_FOUND", "message": "<English, dev-facing>"}}.
// `code` is what we localize; `message` is never shown to the end user.
// `details` is optional structured context some errors carry, e.g.
// {"keys": ["maxTickets"]} for AUTOPILOT_SETTING_LOCKED (DFLT-00142).
export interface ApiErrorPayload {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}

// Reads {"error": {...}} out of a fetch Response body (or `{}` if the body
// isn't in that shape, e.g. a network-level failure with no JSON at all).
export async function parseApiError(response: Response): Promise<ApiErrorPayload | null> {
  try {
    const data = await response.json();
    if (data && typeof data === 'object' && data.error && typeof data.error === 'object') {
      const { code, message, details } = data.error as Partial<ApiErrorPayload>;
      if (typeof code === 'string') {
        const payload: ApiErrorPayload = { code, message: typeof message === 'string' ? message : '' };
        if (details && typeof details === 'object' && !Array.isArray(details)) {
          payload.details = details;
        }
        return payload;
      }
    }
  } catch {
    // Body wasn't JSON (or was empty) -- fall through to null.
  }
  return null;
}

// Resolves a backend error code to a localized, user-facing message via the
// `errors.*` translation namespace, falling back to `errors.UNKNOWN` for any
// code that has no dedicated translation registered (see completion
// condition 3 / the "unrecognized error code" Gherkin scenario).
export function translateErrorCode(t: TFunction, code: string): string {
  const key = `errors.${code}`;
  if (t(key, { defaultValue: '' })) {
    return t(key);
  }
  return t('errors.UNKNOWN');
}

// Reads a thrown value's `message` for display, falling back to `fallback`
// when there is nothing usable to show. Call sites catch `unknown` (what a
// `throw` can actually produce) rather than typing the binding `any` and
// reaching into it directly: `e.message` on a thrown null/undefined throws a
// second TypeError inside the catch block, losing the original failure
// entirely.
export function errorMessage(e: unknown, fallback: string): string {
  const message = (e as { message?: unknown } | null | undefined)?.message;
  return typeof message === 'string' && message !== '' ? message : fallback;
}

// An Error carrying the localized message for display plus the backend's
// error code, for a caller that has to react to one code in particular
// (e.g. TICKET_CHANGED, DFLT-00330). `code` is '' when the response carried
// none.
export class ApiCodeError extends Error {
  readonly code: string;
  constructor(message: string, code: string) {
    super(message);
    this.name = 'ApiCodeError';
    this.code = code;
  }
}

// Whether e is an ApiCodeError with the given code.
export function hasApiErrorCode(e: unknown, code: string): boolean {
  return e instanceof ApiCodeError && e.code === code;
}

// Convenience wrapper: given a failed fetch Response, resolves the
// localized message the UI should display.
export async function localizedApiErrorMessage(t: TFunction, response: Response): Promise<string> {
  const payload = await parseApiError(response);
  if (!payload) {
    return t('errors.UNKNOWN');
  }
  return translateErrorCode(t, payload.code);
}

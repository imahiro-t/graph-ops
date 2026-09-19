// Client-side mirrors of the HTTP custom data source rules the server
// enforces (DFLT-00088): packages/core-go/internal/store's
// ValidateHTTPDataSourceSettings and NormalizeHTTPDataSourceURL, and
// runtimeconfig.IsLoopbackHost. They only exist so the settings form can say
// what is wrong at the field; the server's own check stays authoritative.

// Mirrors runtimeconfig.IsLoopbackHost: "localhost" or any 127.0.0.0/8 or
// ::1 address. `hostname` is URL.hostname (an IPv6 literal keeps its
// brackets there).
export function isLoopbackHost(hostname: string): boolean {
  let h = hostname.trim().toLowerCase();
  if (h.startsWith('[') && h.endsWith(']')) h = h.slice(1, -1);
  if (h.endsWith('.')) h = h.slice(0, -1);
  if (h === 'localhost') return true;
  if (/^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(h)) return true;
  return h === '::1';
}

export type HTTPDataSourceProblem =
  | 'urlRequired'
  | 'urlInvalid'
  | 'plaintextRemote'
  | 'tokenRequired';

// Returns the first problem with a URL/token pair, or null. `tokenPresent`
// is whether the token field holds anything at all -- a saved (redacted)
// token or a "${ENV_VAR}" reference both count, like on the server.
export function httpDataSourceProblem(rawUrl: string, tokenPresent: boolean): HTTPDataSourceProblem | null {
  const trimmed = rawUrl.trim();
  if (!trimmed) return 'urlRequired';
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return 'urlInvalid';
  }
  const scheme = url.protocol.toLowerCase();
  if (scheme !== 'http:' && scheme !== 'https:') return 'urlInvalid';
  if (!url.hostname || url.username || url.password || url.search || trimmed.includes('#') || trimmed.endsWith('?')) {
    return 'urlInvalid';
  }
  const loopback = isLoopbackHost(url.hostname);
  if (scheme === 'http:' && !loopback) return 'plaintextRemote';
  if (!loopback && !tokenPresent) return 'tokenRequired';
  return null;
}

// Mirrors store.NormalizeHTTPDataSourceURL: scheme/host case, an explicit
// default port and trailing slashes do not make two URLs different. An
// unparsable URL is returned trimmed, as is.
export function normalizeHTTPDataSourceURL(rawUrl: string): string {
  const trimmed = rawUrl.trim();
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return trimmed;
  }
  const scheme = url.protocol.toLowerCase().replace(/:$/, '');
  const port = url.port || (scheme === 'https' ? '443' : scheme === 'http' ? '80' : '');
  const host = url.hostname.toLowerCase();
  const path = url.pathname.replace(/\/+$/, '');
  return `${scheme}://${host}${port ? `:${port}` : ''}${path}`;
}

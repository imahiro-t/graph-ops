// The backend requires this header on every state-changing request
// (anything but GET/HEAD/OPTIONS) and deliberately never lists it in
// Access-Control-Allow-Headers (see internal/httpserver/server.go's
// csrfHeaderName) -- so a cross-origin page's fetch/XHR attempting to set it
// fails the browser's CORS preflight, and a plain HTML <form> POST can't set
// custom headers at all. This is the CSRF mitigation for a server that has no
// authentication of any kind (Security review node-5f79e568).
//
// The rest of that original sentence -- "answers with a wildcard CORS origin,
// and listens on all interfaces" -- described the server as it was then and
// is no longer true: Access-Control-Allow-Origin is now echoed back only for
// loopback origins, `serve` binds 127.0.0.1 unless widened deliberately, and
// every request's Host header is checked (403 HOST_NOT_ALLOWED) so a rebound
// domain cannot pose as this origin. This header is one layer of four, not
// the last line of defense it used to be (ticket DFLT-00023).
//
// Same-origin requests -- which is all this app ever makes, including
// through the Vite dev server's /api proxy -- are never subject to CORS
// preflight, so attaching this header here is transparent for legitimate use.
const API_CLIENT_HEADER = 'X-Graph-Engine-Client';

// Thin wrapper around fetch() for this app's own /api/* backend: always
// attaches API_CLIENT_HEADER so every call site gets the CSRF mitigation
// above for free, instead of relying on each one to remember it. Safe to use
// for GET requests too (the backend simply ignores the header there).
export function apiFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set(API_CLIENT_HEADER, '1');
  return fetch(input, { ...init, headers });
}

// DFLT-00330: comparing the backend's RFC 3339 timestamps (updated_at) by
// time, to the nanosecond. They are RFC3339Nano strings, which drop trailing
// zeros ("…:01Z" is earlier than "…:01.5Z" although it sorts after it as
// text), and a Date keeps only milliseconds, so neither a string comparison
// nor Date.parse alone can order two writes made within the same
// millisecond.

const RFC3339 = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/i;

// [whole seconds since the epoch, nanoseconds] of an RFC 3339 timestamp, or
// null when it is not one.
function parse(value: string): [number, number] | null {
  const m = RFC3339.exec(value);
  if (!m) return null;
  const ms = Date.parse(`${m[1]}${m[3].toUpperCase()}`);
  if (Number.isNaN(ms)) return null;
  return [ms / 1000, Number((m[2] ?? '').padEnd(9, '0'))];
}

// Whether timestamp a is strictly later than timestamp b. A value that does
// not parse is never later than anything, and nothing is later than it
// except a value that does parse.
export function isLaterTimestamp(a: string, b: string): boolean {
  const pa = parse(a);
  const pb = parse(b);
  if (!pa) return false;
  if (!pb) return true;
  return pa[0] !== pb[0] ? pa[0] > pb[0] : pa[1] > pb[1];
}

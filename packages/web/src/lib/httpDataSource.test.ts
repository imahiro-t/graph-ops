import { describe, expect, it } from 'vitest';
import { httpDataSourceProblem, isLoopbackHost, normalizeHTTPDataSourceURL } from './httpDataSource';

describe('httpDataSourceProblem (mirrors store.ValidateHTTPDataSourceSettings)', () => {
  it.each([
    ['http://localhost:8787', false, null],
    ['http://127.0.0.1:8787', false, null],
    ['http://127.0.0.2:8787', false, null],
    ['http://[::1]:8787', false, null],
    ['http://example.com', true, 'plaintextRemote'],
    ['http://192.168.1.10:8787', true, 'plaintextRemote'],
    ['https://example.com', false, 'tokenRequired'],
    ['https://example.com', true, null],
    ['https://localhost:8443', false, null],
    ['', true, 'urlRequired'],
    ['ftp://example.com', true, 'urlInvalid'],
    ['https://', true, 'urlInvalid'],
    ['https://user:pass@example.com', true, 'urlInvalid'],
    ['https://example.com/#frag', true, 'urlInvalid'],
    ['example.com', true, 'urlInvalid'],
    ['https://example.com/?q=1', true, 'urlInvalid']
  ])('%s (token present: %s) -> %s', (url, tokenPresent, want) => {
    expect(httpDataSourceProblem(url, tokenPresent)).toBe(want);
  });
});

describe('isLoopbackHost', () => {
  it('accepts localhost, 127/8 and ::1 only', () => {
    expect(isLoopbackHost('localhost')).toBe(true);
    expect(isLoopbackHost('LOCALHOST.')).toBe(true);
    expect(isLoopbackHost('127.10.0.1')).toBe(true);
    expect(isLoopbackHost('[::1]')).toBe(true);
    expect(isLoopbackHost('example.com')).toBe(false);
    expect(isLoopbackHost('128.0.0.1')).toBe(false);
  });
});

describe('normalizeHTTPDataSourceURL (mirrors store.NormalizeHTTPDataSourceURL)', () => {
  it('treats case, the default port and a trailing slash as the same URL', () => {
    const base = normalizeHTTPDataSourceURL('https://a.example.com');
    expect(normalizeHTTPDataSourceURL('https://A.EXAMPLE.com')).toBe(base);
    expect(normalizeHTTPDataSourceURL('https://a.example.com/')).toBe(base);
    expect(normalizeHTTPDataSourceURL('https://a.example.com:443')).toBe(base);
  });

  it('treats a different host, scheme or port as a different URL', () => {
    const base = normalizeHTTPDataSourceURL('https://a.example.com');
    expect(normalizeHTTPDataSourceURL('https://b.example.com')).not.toBe(base);
    expect(normalizeHTTPDataSourceURL('http://a.example.com')).not.toBe(base);
    expect(normalizeHTTPDataSourceURL('https://a.example.com:8443')).not.toBe(base);
  });
});

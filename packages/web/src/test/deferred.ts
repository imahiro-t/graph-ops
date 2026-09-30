// A promise the test resolves or rejects by hand, to look at the screen
// while a request is still in flight and to control the order in which
// several requests answer (DFLT-00356; previously copied into each settings
// editor's test).

export interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: unknown) => void;
}

export function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

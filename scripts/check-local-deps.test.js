'use strict';
// Unit tests for scripts/check-local-deps.js (DFLT-00267). Each test builds a
// throwaway tree:
//
//   <base>/node_modules/...          "the parent repository's" packages
//   <base>/repo/                     the checkout (worktree) root
//   <base>/repo/node_modules/...     hoisted packages
//   <base>/repo/packages/web/node_modules/...  non-hoisted packages

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const { checkLocalDeps, REQUIRED_PACKAGES } = require('./check-local-deps.js');

function makeBase() {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'check-local-deps-test-'));
  const root = path.join(base, 'repo');
  fs.mkdirSync(path.join(root, 'packages', 'web'), { recursive: true });
  return { base, root };
}

function installPackage(dir, pkg) {
  const pkgDir = path.join(dir, 'node_modules', pkg);
  fs.mkdirSync(pkgDir, { recursive: true });
  fs.writeFileSync(path.join(pkgDir, 'package.json'), JSON.stringify({ name: pkg, version: '1.0.0' }));
}

function cleanup(base) {
  fs.rmSync(base, { recursive: true, force: true });
}

test('passes when every package is installed at the checkout root', () => {
  const { base, root } = makeBase();
  try {
    for (const pkg of REQUIRED_PACKAGES) installPackage(root, pkg);
    assert.deepStrictEqual(checkLocalDeps(root), { ok: true });
  } finally {
    cleanup(base);
  }
});

test('passes when packages sit in packages/web/node_modules (not hoisted)', () => {
  const { base, root } = makeBase();
  try {
    const [first, ...rest] = REQUIRED_PACKAGES;
    installPackage(path.join(root, 'packages', 'web'), first);
    for (const pkg of rest) installPackage(root, pkg);
    assert.deepStrictEqual(checkLocalDeps(root), { ok: true });
  } finally {
    cleanup(base);
  }
});

test('fails with "npm ci" and the root path when packages are only found outside the checkout', () => {
  const { base, root } = makeBase();
  try {
    // The parent repository has eslint hoisted, the worktree has nothing.
    for (const pkg of REQUIRED_PACKAGES) installPackage(base, pkg);
    const result = checkLocalDeps(root);
    assert.strictEqual(result.ok, false);
    assert.match(result.message, /npm ci/);
    assert.ok(result.message.includes(root), result.message);
    assert.match(result.message, /eslint: only found outside this checkout/);
    assert.ok(result.message.includes(path.join(base, 'node_modules', 'eslint')), result.message);
  } finally {
    cleanup(base);
  }
});

test('fails when a package is found nowhere', () => {
  const { base, root } = makeBase();
  try {
    for (const pkg of REQUIRED_PACKAGES.filter(p => p !== '@eslint/js')) installPackage(root, pkg);
    const result = checkLocalDeps(root);
    assert.strictEqual(result.ok, false);
    assert.match(result.message, /@eslint\/js: not found/);
    assert.match(result.message, /npm ci/);
  } finally {
    cleanup(base);
  }
});

test('treats packages inside the checkout as inside when the root is given through a symlink', () => {
  const { base, root } = makeBase();
  try {
    for (const pkg of REQUIRED_PACKAGES) installPackage(root, pkg);
    const link = path.join(base, 'linked-repo');
    fs.symlinkSync(root, link, 'dir');
    assert.deepStrictEqual(checkLocalDeps(link), { ok: true });
    // `root` itself is under the un-realpath'd os.tmpdir() (macOS: /var ->
    // /private/var), so the tests above cover that case as well.
  } finally {
    cleanup(base);
  }
});

test('the real checkout passes once npm ci has been run in it', () => {
  // Runs under the root `npm test`, which needs installed dependencies anyway.
  const result = checkLocalDeps();
  assert.strictEqual(result.ok, true, result.message);
});

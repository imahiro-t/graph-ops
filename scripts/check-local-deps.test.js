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
const { spawnSync } = require('node:child_process');

const { checkLocalDeps, main, shellQuote, REQUIRED_PACKAGES } = require('./check-local-deps.js');

function makeBase(rootName = 'repo') {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'check-local-deps-test-'));
  const root = path.join(base, rootName);
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

test('quotes a root containing spaces in the printed cd command', () => {
  const { base, root } = makeBase('My Projects');
  try {
    const result = checkLocalDeps(root);
    assert.strictEqual(result.ok, false);
    assert.ok(result.message.includes(`  cd '${root}' && npm ci`), result.message);
  } finally {
    cleanup(base);
  }
});

test('shellQuote wraps in single quotes and escapes embedded single quotes', () => {
  assert.strictEqual(shellQuote('/a b/c'), "'/a b/c'");
  assert.strictEqual(shellQuote("/it's/$HOME"), "'/it'\\''s/$HOME'");
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

// main() (DFLT-00273): the Node.js version is checked before the dependencies.
function runMain({ node, deps }) {
  const calls = [];
  const written = [];
  const exits = [];
  main({
    checkNode: () => { calls.push('node'); return node; },
    checkDeps: () => { calls.push('deps'); return deps; },
    stderr: { write: (text) => written.push(text) },
    exit: (code) => exits.push(code)
  });
  return { calls, stderr: written.join(''), exits };
}

test('main stops on an unsupported Node.js without running the dependency check', () => {
  const run = runMain({
    node: { ok: false, message: 'Node.js >=24 is required ..., but this is Node.js v20.10.0:' },
    deps: { ok: false, message: 'Dependencies are not installed' }
  });
  assert.deepStrictEqual(run.calls, ['node']);
  assert.deepStrictEqual(run.exits, [1]);
  assert.strictEqual(run.stderr, 'Node.js >=24 is required ..., but this is Node.js v20.10.0:\n');
});

test('main runs the dependency check once the Node.js version is fine, and fails on it', () => {
  const run = runMain({ node: { ok: true }, deps: { ok: false, message: 'Dependencies are not installed' } });
  assert.deepStrictEqual(run.calls, ['node', 'deps']);
  assert.deepStrictEqual(run.exits, [1]);
  assert.strictEqual(run.stderr, 'Dependencies are not installed\n');
});

test('main writes nothing and does not exit when both checks pass', () => {
  const run = runMain({ node: { ok: true }, deps: { ok: true } });
  assert.deepStrictEqual(run.calls, ['node', 'deps']);
  assert.deepStrictEqual(run.exits, []);
  assert.strictEqual(run.stderr, '');
});

test('the CLI exits 0 in this checkout on the running Node.js', () => {
  const result = spawnSync(process.execPath, [path.join(__dirname, 'check-local-deps.js')], { encoding: 'utf8' });
  assert.strictEqual(result.status, 0, result.stderr);
  assert.strictEqual(result.stderr, '');
});

// packages/web's pre scripts (DFLT-00289): each script in CHECKED_WEB_SCRIPTS
// has a pre<name> running this check, so none of them starts on an
// unsupported Node.js or without this checkout's own dependencies. This test
// only looks at the scripts listed here: when you add a script to
// packages/web that starts eslint, vitest, vite or tsc, add it to the list
// too (and give it the same pre<name>). `preview` (vite preview) is left out
// on purpose: it only serves the dist that `build` produced, and prebuild
// already checked that.
// npm runs pre<name> for any script name, including one with a colon, so
// `test:watch` is covered by `pretest:watch` (not by `pretest`).
const CHECKED_WEB_SCRIPTS = ['lint', 'test', 'build', 'dev', 'test:watch'];
const CHECK_COMMAND = 'node ../../scripts/check-local-deps.js';

test("packages/web's lint / test / build / dev / test:watch each run this check first", () => {
  const pkg = JSON.parse(
    fs.readFileSync(path.join(__dirname, '..', 'packages', 'web', 'package.json'), 'utf8')
  );
  const scripts = pkg.scripts || {};
  for (const name of CHECKED_WEB_SCRIPTS) {
    assert.ok(
      typeof scripts[name] === 'string',
      `packages/web/package.json has no "${name}" script; update CHECKED_WEB_SCRIPTS if it was renamed`
    );
    assert.strictEqual(
      scripts[`pre${name}`],
      CHECK_COMMAND,
      `packages/web/package.json's "pre${name}" should be "${CHECK_COMMAND}"`
    );
  }
});

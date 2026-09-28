'use strict';
// Tests for scripts/check-node-version.js (DFLT-00273): the parsing and the
// comparison, and that the repository's declarations -- packages/web and
// root package.json engines.node, .nvmrc, CI's node-version -- agree.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const {
  checkNodeVersion,
  parseMinimum,
  parseVersion,
  readRequiredRange
} = require('./check-node-version.js');

const repoRoot = path.resolve(__dirname, '..');

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

test('parseMinimum understands ">=MAJOR[.MINOR[.PATCH]]"', () => {
  assert.deepStrictEqual(parseMinimum('>=24'), { major: 24, minor: 0, patch: 0 });
  assert.deepStrictEqual(parseMinimum(' >= 24.1 '), { major: 24, minor: 1, patch: 0 });
  assert.deepStrictEqual(parseMinimum('>=24.1.2'), { major: 24, minor: 1, patch: 2 });
});

test('parseMinimum returns null for any other range', () => {
  for (const range of ['^24', '24', '>=24 <26', '>24', '>=24.x', '^20.19.0 || >=22.12.0', '']) {
    assert.strictEqual(parseMinimum(range), null, range);
  }
});

test('parseVersion reads process.version-style strings', () => {
  assert.deepStrictEqual(parseVersion('v20.10.0'), { major: 20, minor: 10, patch: 0 });
  assert.deepStrictEqual(parseVersion('24.21.0'), { major: 24, minor: 21, patch: 0 });
  assert.deepStrictEqual(parseVersion('v24.0.0-pre'), { major: 24, minor: 0, patch: 0 });
  assert.strictEqual(parseVersion('abc'), null);
  assert.strictEqual(parseVersion('v24'), null);
});

test('an older Node.js fails with the required and the current version', () => {
  const result = checkNodeVersion({ current: 'v20.10.0', range: '>=24', execPath: '/opt/node/20.10.0/bin/node' });
  assert.strictEqual(result.ok, false);
  const [first] = result.message.split('\n');
  assert.strictEqual(first, 'Node.js >=24 is required to develop GraphOps, but this is Node.js v20.10.0:');
  assert.ok(result.message.includes('/opt/node/20.10.0/bin/node'), result.message);
  assert.ok(result.message.includes('ASDF_NODEJS_VERSION='), result.message);
  assert.ok(result.message.includes('nvm use'), result.message);
  assert.ok(result.message.includes('fake timers'), result.message);
});

test('asdf (ASDF_NODEJS_VERSION) is suggested before .nvmrc-based switching', () => {
  const { message } = checkNodeVersion({ current: 'v20.10.0', range: '>=24' });
  assert.ok(message.indexOf('ASDF_NODEJS_VERSION') < message.indexOf('.nvmrc'), message);
});

test('the comparison follows major, then minor, then patch', () => {
  const cases = [
    ['v22.12.0', '>=24', false],
    ['v23.99.99', '>=24', false],
    ['v24.0.0', '>=24', true],
    ['v24.21.0', '>=24', true],
    ['v26.0.0', '>=24', true],
    ['v24.1.9', '>=24.2', false],
    ['v24.2.0', '>=24.2', true],
    ['v24.2.2', '>=24.2.3', false],
    ['v24.2.3', '>=24.2.3', true],
    ['v24.0.0-pre', '>=24', true]
  ];
  for (const [current, range, ok] of cases) {
    assert.strictEqual(checkNodeVersion({ current, range }).ok, ok, `${current} vs ${range}`);
  }
});

test('an engines.node the check cannot interpret fails as a configuration error', () => {
  const result = checkNodeVersion({ current: 'v24.21.0', range: '^24' });
  assert.strictEqual(result.ok, false);
  assert.ok(result.message.includes('"^24"'), result.message);
  assert.ok(result.message.includes('>=MAJOR[.MINOR[.PATCH]]'), result.message);
});

test('a current version that cannot be parsed fails and shows it', () => {
  const result = checkNodeVersion({ current: 'weird', range: '>=24' });
  assert.strictEqual(result.ok, false);
  assert.ok(result.message.includes('but this is Node.js weird'), result.message);
  assert.ok(result.message.includes('could not be parsed'), result.message);
});

test('readRequiredRange reads engines.node of packages/web, and throws naming the file without it', () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'check-node-version-test-'));
  try {
    const webDir = path.join(base, 'packages', 'web');
    fs.mkdirSync(webDir, { recursive: true });
    fs.writeFileSync(path.join(webDir, 'package.json'), JSON.stringify({ engines: { node: '>=30.1' } }));
    assert.strictEqual(readRequiredRange(base), '>=30.1');
    assert.strictEqual(checkNodeVersion({ root: base, current: 'v30.0.0' }).ok, false);

    fs.writeFileSync(path.join(webDir, 'package.json'), JSON.stringify({ name: 'web' }));
    assert.throws(() => readRequiredRange(base), (error) => error.message.includes(path.join(webDir, 'package.json')));
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});

// --- The repository's own declarations -----------------------------------

test('packages/web declares engines.node in the supported form, at least Node.js 24', () => {
  const minimum = parseMinimum(readRequiredRange());
  assert.notStrictEqual(minimum, null, readRequiredRange());
  assert.ok(minimum.major >= 24, readRequiredRange());
});

test('the root package.json declares the same engines.node as packages/web', () => {
  const root = readJson(path.join(repoRoot, 'package.json'));
  assert.strictEqual(root.engines && root.engines.node, readRequiredRange());
});

test('.nvmrc names a Node.js major that satisfies engines.node', () => {
  const nvmrc = fs.readFileSync(path.join(repoRoot, '.nvmrc'), 'utf8').trim();
  assert.match(nvmrc, /^\d+$/);
  const result = checkNodeVersion({ current: `v${nvmrc}.0.0` });
  assert.strictEqual(result.ok, true, result.message);
});

test("every node-version in .github/workflows satisfies engines.node", () => {
  const dir = path.join(repoRoot, '.github', 'workflows');
  const found = [];
  for (const name of fs.readdirSync(dir).filter((file) => /\.ya?ml$/.test(file))) {
    const text = fs.readFileSync(path.join(dir, name), 'utf8');
    for (const match of text.matchAll(/^\s*node-version:\s*['"]?([^'"\s#]+)['"]?/gm)) {
      found.push({ name, value: match[1] });
    }
  }
  assert.ok(found.length > 0, 'no node-version found in .github/workflows');
  for (const { name, value } of found) {
    // A major only ("24") means its lowest release, the strictest reading.
    const current = /^\d+$/.test(value) ? `v${value}.0.0` : `v${value}`;
    const result = checkNodeVersion({ current });
    assert.strictEqual(result.ok, true, `${name}: node-version ${value}\n${result.message}`);
  }
});

test('the Node.js running these tests satisfies engines.node', () => {
  // On an older Node.js the root `npm test` reaches this test before
  // packages/web's pretest, so the failure shows the same guidance.
  const result = checkNodeVersion();
  assert.strictEqual(result.ok, true, result.message);
});

test('the CLI exits 0 on the running Node.js', () => {
  const result = spawnSync(process.execPath, [path.join(__dirname, 'check-node-version.js')], { encoding: 'utf8' });
  assert.strictEqual(result.status, 0, result.stderr);
  assert.strictEqual(result.stderr, '');
});

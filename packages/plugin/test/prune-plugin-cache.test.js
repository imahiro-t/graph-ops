'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const { pruneStalePluginCaches, pruneStaleEngineVersions } = require('../scripts/prune-plugin-cache');

const MINUTE = 60 * 1000;
const MAX_AGE_MS = 60 * MINUTE;
// Whole seconds, so mtimes set through utimesSync land exactly on the boundary.
const NOW = Math.floor(Date.now() / 1000) * 1000;

function makeTmp(t) {
  const tmp = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'graph-ops-prune-')));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  return tmp;
}

function setAge(p, ageMs) {
  const seconds = (NOW - ageMs) / 1000;
  fs.utimesSync(p, seconds, seconds);
}

function place(dir, name, ageMs) {
  const p = path.join(dir, name);
  fs.mkdirSync(p, { recursive: true });
  setAge(p, ageMs);
  return p;
}

// Builds <tmp>/.cache/graph-ops/v0.1.0/packages/plugin and returns its paths.
function cacheLayout(tmp) {
  const base = path.join(tmp, '.cache', 'graph-ops');
  const pluginRoot = path.join(base, 'v0.1.0', 'packages', 'plugin');
  fs.mkdirSync(pluginRoot, { recursive: true });
  return { base, pluginRoot };
}

const noWarn = () => {};

test('removes old versions and interrupted clones, keeps recent, unrelated and own entries', (t) => {
  const tmp = makeTmp(t);
  const { base, pluginRoot } = cacheLayout(tmp);
  place(base, 'v0.0.1', 120 * MINUTE);
  place(base, 'v0.1.0~123', 120 * MINUTE);
  place(base, 'v0.0.9~999', 0);
  place(base, 'foo', 120 * MINUTE);
  setAge(path.join(base, 'v0.1.0'), 120 * MINUTE);

  pruneStalePluginCaches({ pluginRoot, now: NOW, maxAgeMs: MAX_AGE_MS, warn: noWarn });

  assert.deepStrictEqual(fs.readdirSync(base).sort(), ['foo', 'v0.0.9~999', 'v0.1.0']);
});

for (const [ageMinutes, removed] of [
  [59, false],
  [60, true],
  [61, true],
]) {
  test(`an entry ${ageMinutes} minutes old is ${removed ? 'removed' : 'kept'}`, (t) => {
    const tmp = makeTmp(t);
    const { base, pluginRoot } = cacheLayout(tmp);
    const entry = place(base, 'v0.0.1', ageMinutes * MINUTE);

    pruneStalePluginCaches({ pluginRoot, now: NOW, maxAgeMs: MAX_AGE_MS, warn: noWarn });

    assert.strictEqual(fs.existsSync(entry), !removed);
  });
}

for (const [label, relRoot, removed] of [
  ['the expected cache layout (control)', '.cache/graph-ops/v0.1.0/packages/plugin', true],
  ['a monorepo checkout', 'graph-ops/packages/plugin', false],
  ['a base not named graph-ops', '.cache/other-name/v0.1.0/packages/plugin', false],
  ['a base whose parent is not .cache', 'not-cache/graph-ops/v0.1.0/packages/plugin', false],
  ['a plugin root not under packages/', '.cache/graph-ops/v0.1.0/other/plugin', false],
]) {
  test(`layout check: ${label} ${removed ? 'prunes' : 'prunes nothing'}`, (t) => {
    const tmp = makeTmp(t);
    const pluginRoot = path.join(tmp, ...relRoot.split('/'));
    fs.mkdirSync(pluginRoot, { recursive: true });
    const threeUp = path.resolve(pluginRoot, '..', '..', '..');
    const entry = place(threeUp, 'v0.0.1', 120 * MINUTE);

    assert.doesNotThrow(() =>
      pruneStalePluginCaches({ pluginRoot, now: NOW, maxAgeMs: MAX_AGE_MS, warn: noWarn })
    );

    assert.strictEqual(fs.existsSync(entry), !removed);
  });
}

test('does not throw when the cache base cannot be read', () => {
  const missing = path.join(os.tmpdir(), 'graph-ops-prune-missing', '.cache', 'graph-ops', 'v0.1.0', 'packages', 'plugin');
  assert.doesNotThrow(() => pruneStalePluginCaches({ pluginRoot: missing, warn: noWarn }));
  assert.doesNotThrow(() => pruneStalePluginCaches({ pluginRoot: missing, warn: () => { throw new Error('boom'); } }));
});

test(
  'an entry that cannot be removed does not stop the others from being pruned',
  {
    skip:
      process.platform === 'win32'
        ? 'skipped on Windows: chmod does not make a directory undeletable there'
        : process.getuid && process.getuid() === 0
          ? 'skipped as root: permission checks do not apply, so the removal cannot be made to fail'
          : false,
  },
  (t) => {
    const tmp = makeTmp(t);
    const { base, pluginRoot } = cacheLayout(tmp);
    place(base, 'v0.0.0', 120 * MINUTE);
    place(base, 'v0.0.2', 120 * MINUTE);
    const locked = path.join(base, 'v0.0.1', 'locked');
    fs.mkdirSync(locked, { recursive: true });
    fs.writeFileSync(path.join(locked, 'file'), 'x');
    fs.chmodSync(locked, 0o555);
    // Set last: creating children above bumped v0.0.1's mtime.
    setAge(path.join(base, 'v0.0.1'), 120 * MINUTE);

    const warnings = [];
    assert.doesNotThrow(() =>
      pruneStalePluginCaches({ pluginRoot, now: NOW, maxAgeMs: MAX_AGE_MS, warn: (m) => warnings.push(m) })
    );

    try {
      assert.ok(!fs.existsSync(path.join(base, 'v0.0.0')), 'v0.0.0 should have been removed');
      assert.ok(!fs.existsSync(path.join(base, 'v0.0.2')), 'v0.0.2 should have been removed');
      assert.ok(fs.existsSync(path.join(locked, 'file')), 'v0.0.1/locked/file should remain');
      assert.ok(fs.existsSync(path.join(base, 'v0.1.0')), 'own tag v0.1.0 should remain');
      assert.strictEqual(warnings.length, 1, `expected one warning, got ${JSON.stringify(warnings)}`);
    } finally {
      fs.chmodSync(locked, 0o755);
    }
  }
);

// --- pruneStaleEngineVersions (per-user graph-engine cache) ---

test('pruneStaleEngineVersions removes only old v<x.y.z> directories other than the kept version', (t) => {
  const engineRoot = path.join(makeTmp(t), 'engine');
  place(engineRoot, 'v0.1.0', 120 * MINUTE); // old -> removed
  place(engineRoot, 'v0.2.0', 120 * MINUTE); // kept version, even though old
  place(engineRoot, 'v0.1.5', 10 * MINUTE); // recent -> kept
  place(engineRoot, 'foo', 120 * MINUTE); // not a version -> kept
  place(engineRoot, 'v0.0.9~123', 120 * MINUTE); // not exactly v<x.y.z> -> kept
  place(engineRoot, '0.0.8', 120 * MINUTE); // no leading v -> kept
  const file = path.join(engineRoot, 'v0.0.7');
  fs.writeFileSync(file, 'not a directory');
  setAge(file, 120 * MINUTE);

  pruneStaleEngineVersions({ engineRoot, keepVersion: '0.2.0', now: NOW, maxAgeMs: MAX_AGE_MS, warn: noWarn });

  assert.deepStrictEqual(fs.readdirSync(engineRoot).sort(), ['0.0.8', 'foo', 'v0.0.7', 'v0.0.9~123', 'v0.1.5', 'v0.2.0']);
});

test('pruneStaleEngineVersions never throws when the engine root is missing', () => {
  const missing = path.join(os.tmpdir(), 'graph-ops-prune-missing-engine', 'engine');
  assert.doesNotThrow(() => pruneStaleEngineVersions({ engineRoot: missing, keepVersion: '0.1.0', warn: noWarn }));
  assert.doesNotThrow(() =>
    pruneStaleEngineVersions({ engineRoot: missing, keepVersion: '0.1.0', warn: () => { throw new Error('boom'); } })
  );
});

test('pruneStalePluginCaches never touches the per-user engine cache next to the tag clones', (t) => {
  const tmp = makeTmp(t);
  const { base, pluginRoot } = cacheLayout(tmp);
  const engine = place(base, 'engine', 120 * MINUTE);
  place(engine, 'v0.0.1', 120 * MINUTE);
  setAge(engine, 120 * MINUTE);

  pruneStalePluginCaches({ pluginRoot, now: NOW, maxAgeMs: MAX_AGE_MS, warn: noWarn });

  assert.ok(fs.existsSync(path.join(engine, 'v0.0.1')));
});

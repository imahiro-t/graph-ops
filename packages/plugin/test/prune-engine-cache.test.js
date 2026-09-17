'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const { pruneStaleEngineVersions } = require('../scripts/prune-engine-cache');

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

const noWarn = () => {};

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

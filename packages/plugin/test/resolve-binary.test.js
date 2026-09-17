'use strict';

const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');

const { resolveBinary } = require('../scripts/resolve-binary');
const { makeTmp, makePluginRoot, writeFile } = require('./helpers');

// <tmp>/plugin is the plugin root, so <tmp>/core-go is its monorepo sibling.
function setup(t, { libexec, libexecVersion, monorepo, cache }) {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp, { version: '1.2.3' });
  const engineDir = path.join(tmp, 'engine');
  const paths = {
    root,
    engineDir,
    libexec: path.join(root, 'libexec', 'graph-engine'),
    monorepo: path.join(tmp, 'core-go', 'graph-engine'),
    cache: path.join(engineDir, 'v1.2.3', 'graph-engine'),
  };
  if (libexec) {
    writeFile(paths.libexec, 'x', 0o755);
    if (libexecVersion) writeFile(path.join(root, 'libexec', '.version'), `${libexecVersion}\n`);
  }
  if (monorepo) writeFile(paths.monorepo, 'x', 0o755);
  if (cache) writeFile(paths.cache, 'x', 0o755);
  return paths;
}

function resolve(p, platform = 'linux') {
  return resolveBinary({ pluginRoot: p.root, env: { GRAPH_OPS_ENGINE_DIR: p.engineDir }, platform });
}

const cases = [
  ['libexec with matching version beats everything', { libexec: true, libexecVersion: '1.2.3', monorepo: true, cache: true }, 'libexec'],
  ['libexec with mismatching version is skipped for the monorepo build', { libexec: true, libexecVersion: '1.0.0', monorepo: true, cache: true }, 'monorepo'],
  ['libexec without .version is skipped', { libexec: true, monorepo: true }, 'monorepo'],
  ['monorepo build beats the cache', { monorepo: true, cache: true }, 'monorepo'],
  ['stale libexec + cache resolves to the cache', { libexec: true, libexecVersion: '1.0.0', cache: true }, 'cache'],
  ['cache alone', { cache: true }, 'cache'],
];

for (const [label, layout, expected] of cases) {
  test(`resolveBinary: ${label}`, (t) => {
    const p = setup(t, layout);
    assert.deepStrictEqual(resolve(p), { command: p[expected], args: [] });
  });
}

test('resolveBinary: nothing present -> the sh shim on POSIX', (t) => {
  const p = setup(t, { libexec: true, libexecVersion: '1.0.0' });
  assert.deepStrictEqual(resolve(p), { command: path.join(p.root, 'bin', 'graph-engine'), args: [] });
});

test('resolveBinary: nothing present -> node engine-shim.js on win32', (t) => {
  const p = setup(t, {});
  assert.deepStrictEqual(resolve(p, 'win32'), {
    command: process.execPath,
    args: [path.join(p.root, 'scripts', 'engine-shim.js')],
  });
});

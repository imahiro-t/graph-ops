'use strict';
// bin/graph-engine.cmd relays its arguments to scripts/engine-shim.js through
// the environment instead of cmd.exe's all-arguments token (finding CHK-05).
// These are the golden tests for the Node half of that relay: fixed
// environments in, fixed argv out. Nothing here needs cmd.exe, so they run on
// macOS and Linux like every other test in this package.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const { argvFromEnv, stripArgEnv, resolveArgv } = require('../scripts/cmd-args');
const { makeTmp, copyPlugin, writeFile, fakeEngineScript } = require('./helpers');

test('argvFromEnv rebuilds exactly what cmd.exe put in the variables', () => {
  const cases = [
    // The main path: bin/ is on PATH, so the command is a bare short name.
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'list' }, ['list']],
    [{ GRAPH_OPS_ARGC: '0' }, []],
    [
      { GRAPH_OPS_ARGC: '3', GRAPH_OPS_ARG_1: 'get-ticket', GRAPH_OPS_ARG_2: 'DFLT-1', GRAPH_OPS_ARG_3: '--json' },
      ['get-ticket', 'DFLT-1', '--json'],
    ],
    [{ GRAPH_OPS_ARGC: '2', GRAPH_OPS_ARG_1: 'add-artifact', GRAPH_OPS_ARG_2: 'a b c' }, ['add-artifact', 'a b c']],
    // The characters %* would have mangled: a variable reference stays text,
    // an ampersand does not start a second command, a quote does not re-split.
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: '%PATH%' }, ['%PATH%']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'a&b' }, ['a&b']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'x && echo done' }, ['x && echo done']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'say "hi"' }, ['say "hi"']],
    // And the characters `setlocal EnableDelayedExpansion` would have eaten
    // on the way in -- which is why the .cmd shim does not enable it.
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'a!b!c' }, ['a!b!c']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'やった!' }, ['やった!']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: '!FOO!' }, ['!FOO!']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'a^b' }, ['a^b']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: '50%^ done!' }, ['50%^ done!']],
    [{ GRAPH_OPS_ARGC: '2', GRAPH_OPS_ARG_1: 'echo', GRAPH_OPS_ARG_2: '' }, ['echo', '']],
    [{ GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'C:\\path\\' }, ['C:\\path\\']],
    [{ GRAPH_OPS_ARGC: '2', GRAPH_OPS_ARG_1: 'add-artifact', GRAPH_OPS_ARG_2: '計画作成' }, ['add-artifact', '計画作成']],
  ];
  for (const [env, expected] of cases) {
    assert.deepStrictEqual(argvFromEnv(env), expected, JSON.stringify(env));
  }
});

test('argvFromEnv returns null rather than a guess when the relay is incomplete', () => {
  const cases = [
    {},
    { GRAPH_OPS_ARGC: 'abc' },
    { GRAPH_OPS_ARGC: '-1' },
    { GRAPH_OPS_ARGC: '1.5' },
    { GRAPH_OPS_ARGC: '' },
    { GRAPH_OPS_ARGC: '2', GRAPH_OPS_ARG_1: 'list' },
  ];
  for (const env of cases) {
    assert.strictEqual(argvFromEnv(env), null, JSON.stringify(env));
  }
  // null is "no answer", never "no arguments" -- those must stay distinct.
  assert.deepStrictEqual(argvFromEnv({ GRAPH_OPS_ARGC: '0' }), []);
});

test('stripArgEnv removes only the relay variables', () => {
  const env = {
    GRAPH_OPS_ARGC: '1',
    GRAPH_OPS_ARG_1: 'list',
    GRAPH_OPS_ARG_7: 'leftover',
    GRAPH_OPS_ENGINE_DIR: '/keep/me',
    PATH: '/usr/bin',
  };
  stripArgEnv(env);
  assert.deepStrictEqual(env, { GRAPH_OPS_ENGINE_DIR: '/keep/me', PATH: '/usr/bin' });
});

test('resolveArgv prefers the relay, falls back to process.argv, and always clears the relay', () => {
  const relayed = { GRAPH_OPS_ARGC: '1', GRAPH_OPS_ARG_1: 'list', KEEP: '1' };
  assert.deepStrictEqual(resolveArgv(relayed, ['ignored']), ['list']);
  assert.deepStrictEqual(relayed, { KEEP: '1' });

  // No relay at all: the sh shim and resolve-binary.js path.
  const plain = { KEEP: '1' };
  assert.deepStrictEqual(resolveArgv(plain, ['list', '--json']), ['list', '--json']);

  // A half-set relay must not swallow real arguments, and must not survive
  // into whatever runs next.
  const partial = { GRAPH_OPS_ARGC: '2', GRAPH_OPS_ARG_1: 'list' };
  assert.deepStrictEqual(resolveArgv(partial, ['real', 'args']), ['real', 'args']);
  assert.deepStrictEqual(partial, {});
});

// The shim end to end, against a fake graph-engine that echoes each argument
// as "[arg]" on stdout -- the same fixture engine-shim.test.js uses.
function setup(t) {
  const tmp = makeTmp(t, 'graph-ops-cmdargs-');
  const root = copyPlugin(tmp, { version: '1.2.3' });
  const engineDir = path.join(tmp, 'engine');
  const home = path.join(tmp, 'home');
  fs.mkdirSync(home);
  writeFile(path.join(engineDir, 'v1.2.3', 'graph-engine'), fakeEngineScript('cache'), 0o755);
  return { root, env: { PATH: process.env.PATH, HOME: home, GRAPH_OPS_ENGINE_DIR: engineDir } };
}

function runShim(root, env) {
  return spawnSync(process.execPath, [path.join(root, 'scripts', 'engine-shim.js')], { env, encoding: 'utf8' });
}

test('engine-shim.js: runs the relayed arguments when the .cmd shim supplied them', { skip: process.platform === 'win32' && 'fake sh engine' }, (t) => {
  const { root, env } = setup(t);
  const res = runShim(root, {
    ...env,
    GRAPH_OPS_ARGC: '3',
    GRAPH_OPS_ARG_1: 'add-artifact',
    GRAPH_OPS_ARG_2: 'a & b "c" %PATH%',
    GRAPH_OPS_ARG_3: 'やった!',
  });
  assert.strictEqual(res.status, 0, res.stderr);
  assert.strictEqual(res.stdout, '[add-artifact]\n[a & b "c" %PATH%]\n[やった!]\n');
});

test('engine-shim.js: does not pass the relay variables on to graph-engine', { skip: process.platform === 'win32' && 'fake sh engine' }, (t) => {
  const { root, env } = setup(t);
  // A fake engine that reports its own environment, so what the child
  // actually inherits is directly observable -- including a leftover
  // GRAPH_OPS_ARG_7 that nothing downstream should ever see.
  writeFile(
    path.join(env.GRAPH_OPS_ENGINE_DIR, 'v1.2.3', 'graph-engine'),
    "#!/bin/sh\nenv | grep '^GRAPH_OPS_' | sort\n",
    0o755
  );
  const res = runShim(root, {
    ...env,
    GRAPH_OPS_ARGC: '1',
    GRAPH_OPS_ARG_1: 'list',
    GRAPH_OPS_ARG_7: 'leftover',
  });
  assert.strictEqual(res.status, 0, res.stderr);
  assert.doesNotMatch(res.stdout, /GRAPH_OPS_ARGC/);
  assert.doesNotMatch(res.stdout, /GRAPH_OPS_ARG_/);
  // Everything else is still inherited.
  assert.match(res.stdout, /GRAPH_OPS_ENGINE_DIR/);
});

test('engine-shim.js: an incomplete relay falls back to process.argv instead of dropping arguments', { skip: process.platform === 'win32' && 'fake sh engine' }, (t) => {
  const { root, env } = setup(t);
  const res = spawnSync(
    process.execPath,
    [path.join(root, 'scripts', 'engine-shim.js'), 'list', '--json'],
    { env: { ...env, GRAPH_OPS_ARGC: '2', GRAPH_OPS_ARG_1: 'list' }, encoding: 'utf8' }
  );
  assert.strictEqual(res.status, 0, res.stderr);
  assert.strictEqual(res.stdout, '[list]\n[--json]\n');
});

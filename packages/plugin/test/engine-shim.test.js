'use strict';
// End-to-end checks of the committed shims (bin/graph-engine and
// scripts/engine-shim.js) against a copy of packages/plugin with fake
// graph-engine binaries. None of these reach the network: every case either
// has a binary in place or has no `node` to download with.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync, execFileSync } = require('node:child_process');

const { makeTmp, copyPlugin, writeFile, fakeEngineScript } = require('./helpers');

const skip = process.platform === 'win32' ? 'POSIX sh shim tests do not run on Windows' : false;

function setup(t) {
  const tmp = makeTmp(t, 'graph-ops-shim-');
  const root = copyPlugin(tmp, { version: '1.2.3' });
  const engineDir = path.join(tmp, 'engine');
  const home = path.join(tmp, 'home');
  fs.mkdirSync(home);
  const env = { PATH: process.env.PATH, HOME: home, GRAPH_OPS_ENGINE_DIR: engineDir };
  return { tmp, root, engineDir, env };
}

function placeFake(file, label) {
  return writeFile(file, fakeEngineScript(label), 0o755);
}

// A PATH directory holding only the tools the sh shim needs -- and no node.
function pathWithoutNode(tmp) {
  const dir = path.join(tmp, 'tools');
  fs.mkdirSync(dir);
  for (const tool of ['dirname', 'sed', 'head', 'tr', 'uname', 'readlink', 'cat']) {
    const found = execFileSync('/bin/sh', ['-c', `command -v ${tool}`], { encoding: 'utf8' }).trim();
    fs.symlinkSync(found, path.join(dir, tool));
  }
  return dir;
}

const TRICKY_ARGS = ['get-ticket', 'with space', "it's", 'say "hi"', '', '$HOME', '*', '--flag=a b', 'line1\nline2'];
const EXPECTED_STDOUT = TRICKY_ARGS.map((a) => `[${a}]\n`).join('');

const launchers = {
  'bin/graph-engine': (root, args, opts) => spawnSync(path.join(root, 'bin', 'graph-engine'), args, opts),
  'scripts/engine-shim.js': (root, args, opts) =>
    spawnSync(process.execPath, [path.join(root, 'scripts', 'engine-shim.js'), ...args], opts),
};

for (const [name, launch] of Object.entries(launchers)) {
  test(`${name}: passes arguments verbatim, keeps stdout clean and returns the exit code`, { skip }, (t) => {
    const { root, engineDir, env } = setup(t);
    placeFake(path.join(engineDir, 'v1.2.3', 'graph-engine'), 'cache');

    const ok = launch(root, TRICKY_ARGS, { env, encoding: 'utf8' });
    assert.strictEqual(ok.status, 0, ok.stderr);
    assert.strictEqual(ok.stdout, EXPECTED_STDOUT);
    assert.match(ok.stderr, /fake-engine:cache/);

    const failing = launch(root, ['x'], { env: { ...env, FAKE_EXIT: '7' }, encoding: 'utf8' });
    assert.strictEqual(failing.status, 7);
    assert.strictEqual(failing.stdout, '[x]\n');
  });

  test(`${name}: skips a libexec binary whose .version does not match`, { skip }, (t) => {
    const { root, engineDir, env } = setup(t);
    placeFake(path.join(root, 'libexec', 'graph-engine'), 'libexec');
    placeFake(path.join(engineDir, 'v1.2.3', 'graph-engine'), 'cache');

    writeFile(path.join(root, 'libexec', '.version'), '1.0.0\n');
    let res = launch(root, [], { env, encoding: 'utf8' });
    assert.match(res.stderr, /fake-engine:cache/);

    writeFile(path.join(root, 'libexec', '.version'), '1.2.3\n');
    res = launch(root, [], { env, encoding: 'utf8' });
    assert.match(res.stderr, /fake-engine:libexec/);
  });
}

test('scripts/engine-shim.js: reports 128 + signal number when the engine is killed by a signal', { skip }, (t) => {
  const { root, engineDir, env } = setup(t);
  writeFile(path.join(engineDir, 'v1.2.3', 'graph-engine'), '#!/bin/sh\nkill -TERM $$\n', 0o755);
  const res = launchers['scripts/engine-shim.js'](root, [], { env, encoding: 'utf8' });
  assert.strictEqual(res.status, 128 + 15);
});

test('bin/graph-engine: works when invoked through a symlink', { skip }, (t) => {
  const { tmp, root, engineDir, env } = setup(t);
  placeFake(path.join(engineDir, 'v1.2.3', 'graph-engine'), 'cache');
  const linkDir = path.join(tmp, 'links');
  fs.mkdirSync(linkDir);
  fs.symlinkSync(path.join(root, 'bin', 'graph-engine'), path.join(linkDir, 'graph-engine'));

  const res = spawnSync('graph-engine', ['a'], { env: { ...env, PATH: `${linkDir}:${env.PATH}` }, encoding: 'utf8' });
  assert.strictEqual(res.status, 0, res.stderr);
  assert.strictEqual(res.stdout, '[a]\n');
});

test('bin/graph-engine: no binary and no node on PATH -> exit 127 with guidance on stderr only', { skip }, (t) => {
  const { tmp, root, env } = setup(t);
  const res = spawnSync(path.join(root, 'bin', 'graph-engine'), ['x'], {
    env: { ...env, PATH: pathWithoutNode(tmp) },
    encoding: 'utf8',
  });
  assert.strictEqual(res.status, 127, res.stderr);
  assert.strictEqual(res.stdout, '');
  assert.match(res.stderr, /requires Node\.js/);
  assert.ok(res.stderr.includes(path.join(env.GRAPH_OPS_ENGINE_DIR, 'v1.2.3', 'graph-engine')), res.stderr);
});

test('bin/graph-engine: no node but a stale libexec binary -> used with a warning', { skip }, (t) => {
  const { tmp, root, env } = setup(t);
  placeFake(path.join(root, 'libexec', 'graph-engine'), 'libexec');
  writeFile(path.join(root, 'libexec', '.version'), '1.0.0\n');
  const res = spawnSync(path.join(root, 'bin', 'graph-engine'), ['x'], {
    env: { ...env, PATH: pathWithoutNode(tmp) },
    encoding: 'utf8',
  });
  assert.strictEqual(res.status, 0, res.stderr);
  assert.strictEqual(res.stdout, '[x]\n');
  assert.match(res.stderr, /warning: Node\.js not found/);
  assert.match(res.stderr, /fake-engine:libexec/);
});

'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const {
  ensureBinary,
  ensureEngine,
  engineCacheRoot,
  engineCacheDir,
  releaseRepo,
  pluginVersion,
  assetNameForCurrentTarget,
  localExeName,
  MAX_ATTEMPTS,
} = require('../scripts/install-binary');
const { makeTmp, copyPlugin, makePluginRoot, writeFile, sha256, pluginDir } = require('./helpers');

let supportedPlatform = true;
try {
  assetNameForCurrentTarget();
} catch {
  supportedPlatform = false;
}
const downloadSkip = supportedPlatform ? false : 'no release asset is published for this platform';

const EXE = localExeName();
const BINARY = 'fake graph-engine binary\n';

// Network-free stand-ins for the HTTPS fetchers. Records every URL requested.
function fakeRelease({ binary = BINARY, digest = sha256(binary), failFile = false } = {}) {
  const urls = [];
  return {
    urls,
    fetchFile: async (url, dest) => {
      urls.push(url);
      if (failFile) throw new Error('getaddrinfo ENOTFOUND github.com');
      fs.writeFileSync(dest, binary);
    },
    fetchText: async (url) => {
      urls.push(url);
      return `${digest}  ${assetNameForCurrentTarget()}\n0000000000000000000000000000000000000000000000000000000000000000  other-asset\n`;
    },
  };
}

function quiet() {
  const lines = [];
  return { lines, log: (m) => lines.push(m) };
}

test('releaseRepo ignores GRAPH_OPS_RELEASE_REPO and only reads plugin.json', (t) => {
  const root = makePluginRoot(makeTmp(t));
  const saved = process.env.GRAPH_OPS_RELEASE_REPO;
  process.env.GRAPH_OPS_RELEASE_REPO = 'attacker/evil';
  t.after(() => {
    if (saved === undefined) delete process.env.GRAPH_OPS_RELEASE_REPO;
    else process.env.GRAPH_OPS_RELEASE_REPO = saved;
  });
  assert.strictEqual(releaseRepo(root), 'example-owner/example-repo');
});

test('no source file of the plugin mentions GRAPH_OPS_RELEASE_REPO anymore', () => {
  for (const dir of ['scripts', 'bin', 'commands']) {
    for (const name of fs.readdirSync(path.join(pluginDir, dir))) {
      const text = fs.readFileSync(path.join(pluginDir, dir, name), 'utf8');
      assert.ok(!text.includes('GRAPH_OPS_RELEASE_REPO'), `${dir}/${name} still mentions GRAPH_OPS_RELEASE_REPO`);
    }
  }
});

test('a copy of packages/plugin alone still resolves repository and version', (t) => {
  const root = copyPlugin(makeTmp(t));
  const real = JSON.parse(fs.readFileSync(path.join(pluginDir, '.claude-plugin', 'plugin.json'), 'utf8'));
  assert.strictEqual(releaseRepo(root), 'imahiro-t/graph-ops');
  assert.strictEqual(pluginVersion(root), real.version);
});

test('engineCacheRoot resolution order', () => {
  const posix = (env) => engineCacheRoot({ env, platform: 'linux' });
  assert.strictEqual(
    posix({ GRAPH_OPS_ENGINE_DIR: '/x/engine', XDG_CACHE_HOME: '/xdg', HOME: '/home/u' }),
    path.resolve('/x/engine')
  );
  assert.strictEqual(posix({ XDG_CACHE_HOME: '/xdg', HOME: '/home/u' }), path.join('/xdg', 'graph-ops', 'engine'));
  assert.strictEqual(posix({ XDG_CACHE_HOME: 'relative', HOME: '/home/u' }), path.join('/home/u', '.cache', 'graph-ops', 'engine'));
  assert.strictEqual(posix({ XDG_CACHE_HOME: '', HOME: '/home/u' }), path.join('/home/u', '.cache', 'graph-ops', 'engine'));
  assert.strictEqual(
    posix({ HOME: '/home/u', CLAUDE_PLUGIN_DATA: '/data/plugin' }),
    path.join('/home/u', '.cache', 'graph-ops', 'engine'),
    'CLAUDE_PLUGIN_DATA must not affect the cache location'
  );
  assert.strictEqual(
    engineCacheRoot({ env: { LOCALAPPDATA: '/lad', XDG_CACHE_HOME: '/xdg' }, platform: 'win32' }),
    path.join('/lad', 'graph-ops', 'engine')
  );
  assert.strictEqual(
    engineCacheRoot({ env: { GRAPH_OPS_ENGINE_DIR: '/x/engine', LOCALAPPDATA: '/lad' }, platform: 'win32' }),
    path.resolve('/x/engine')
  );
});

test('engineCacheRoot ignores a relative GRAPH_OPS_ENGINE_DIR (like a relative XDG_CACHE_HOME)', () => {
  const posix = (env) => engineCacheRoot({ env, platform: 'linux' });
  assert.strictEqual(posix({ GRAPH_OPS_ENGINE_DIR: 'engine', HOME: '/home/u' }), path.join('/home/u', '.cache', 'graph-ops', 'engine'));
  assert.strictEqual(posix({ GRAPH_OPS_ENGINE_DIR: './x', XDG_CACHE_HOME: '/xdg', HOME: '/home/u' }), path.join('/xdg', 'graph-ops', 'engine'));
  assert.strictEqual(
    engineCacheRoot({ env: { GRAPH_OPS_ENGINE_DIR: 'engine', LOCALAPPDATA: '/lad' }, platform: 'win32' }),
    path.join('/lad', 'graph-ops', 'engine')
  );
});

test('engineCacheDir appends v<version>', (t) => {
  const root = makePluginRoot(makeTmp(t), { version: '4.5.6' });
  assert.strictEqual(
    engineCacheDir(root, { env: { GRAPH_OPS_ENGINE_DIR: '/e' }, platform: 'linux' }),
    path.join(path.resolve('/e'), 'v4.5.6')
  );
});

test('ensureBinary installs into libexec/ by default, never bin/, and downloads from plugin.json\'s release', { skip: downloadSkip }, async (t) => {
  const root = makePluginRoot(makeTmp(t));
  const release = fakeRelease();
  const binPath = await ensureBinary({ pluginRoot: root, ...release, retryBackoffMs: 0 });

  assert.strictEqual(binPath, path.join(root, 'libexec', EXE));
  assert.strictEqual(fs.readFileSync(binPath, 'utf8'), BINARY);
  assert.strictEqual(fs.readFileSync(path.join(root, 'libexec', '.version'), 'utf8'), '1.2.3\n');
  assert.ok(!fs.existsSync(path.join(root, 'bin')), 'bin/ must not be written to');
  if (process.platform !== 'win32') {
    assert.ok(fs.statSync(binPath).mode & 0o100, 'binary must be executable');
  }
  const base = 'https://github.com/example-owner/example-repo/releases/download/v1.2.3';
  assert.deepStrictEqual(release.urls, [`${base}/${assetNameForCurrentTarget()}`, `${base}/checksums.txt`]);
});

test('ensureBinary honours an explicit binDir and skips the marker when asked', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  const binDir = path.join(tmp, 'cache', 'v1.2.3');
  const binPath = await ensureBinary({ pluginRoot: root, binDir, versionMarker: false, ...fakeRelease(), retryBackoffMs: 0 });

  assert.strictEqual(binPath, path.join(binDir, EXE));
  assert.ok(!fs.existsSync(path.join(binDir, '.version')));
  assert.ok(!fs.existsSync(path.join(root, 'libexec')));
  assert.deepStrictEqual(fs.readdirSync(binDir), [EXE], 'no temporary download file may be left behind');
});

test('a checksum mismatch never places the binary', { skip: downloadSkip }, async (t) => {
  const root = makePluginRoot(makeTmp(t));
  const release = fakeRelease({ digest: sha256('something else') });
  await assert.rejects(
    ensureBinary({ pluginRoot: root, ...release, retryBackoffMs: 0 }),
    /checksum mismatch/
  );
  assert.deepStrictEqual(fs.readdirSync(path.join(root, 'libexec')), []);
  assert.strictEqual(release.urls.length, MAX_ATTEMPTS * 2);
});

test('ensureEngine: libexec with a matching .version wins and nothing is fetched', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  writeFile(path.join(root, 'libexec', EXE), 'x', 0o755);
  writeFile(path.join(root, 'libexec', '.version'), '1.2.3\n');
  const release = fakeRelease();
  const { log } = quiet();

  const bin = await ensureEngine({ pluginRoot: root, env: { GRAPH_OPS_ENGINE_DIR: path.join(tmp, 'engine') }, log, ...release });
  assert.strictEqual(bin, path.join(root, 'libexec', EXE));
  assert.deepStrictEqual(release.urls, []);
});

test('ensureEngine: a libexec .version mismatch is skipped in favour of the cache, then a download', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  const engineDir = path.join(tmp, 'engine');
  writeFile(path.join(root, 'libexec', EXE), 'old', 0o755);
  writeFile(path.join(root, 'libexec', '.version'), '1.0.0\n');
  const { log, lines } = quiet();

  // Cached binary present -> used, no download.
  const cached = writeFile(path.join(engineDir, 'v1.2.3', EXE), 'cached', 0o755);
  let release = fakeRelease();
  assert.strictEqual(await ensureEngine({ pluginRoot: root, env: { GRAPH_OPS_ENGINE_DIR: engineDir }, log, ...release }), cached);
  assert.deepStrictEqual(release.urls, []);

  // Cache empty -> downloaded into the cache (not libexec), announced on log.
  fs.rmSync(path.join(engineDir, 'v1.2.3'), { recursive: true });
  release = fakeRelease();
  const bin = await ensureEngine({ pluginRoot: root, env: { GRAPH_OPS_ENGINE_DIR: engineDir }, log, ...release, retryBackoffMs: 0 });
  assert.strictEqual(bin, path.join(engineDir, 'v1.2.3', EXE));
  assert.strictEqual(fs.readFileSync(bin, 'utf8'), BINARY);
  assert.strictEqual(fs.readFileSync(path.join(root, 'libexec', EXE), 'utf8'), 'old', 'libexec must be left alone');
  assert.ok(lines.some((l) => l.includes('downloading graph-engine v1.2.3')), lines.join(''));
});

test('ensureEngine: a failed download falls back to a stale libexec binary with a warning', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  writeFile(path.join(root, 'libexec', EXE), 'old', 0o755);
  writeFile(path.join(root, 'libexec', '.version'), '1.0.0\n');
  const { log, lines } = quiet();

  const bin = await ensureEngine({
    pluginRoot: root,
    env: { GRAPH_OPS_ENGINE_DIR: path.join(tmp, 'engine') },
    log,
    ...fakeRelease({ failFile: true }),
    retryBackoffMs: 0,
  });
  assert.strictEqual(bin, path.join(root, 'libexec', EXE));
  assert.ok(lines.some((l) => /warning: .*ENOTFOUND.*falling back/.test(l)), lines.join(''));
  assert.ok(!fs.existsSync(path.join(tmp, 'engine', 'v1.2.3', EXE)));
});

test('ensureEngine: a failed download with nothing to fall back to throws with manual-install guidance', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  const engineDir = path.join(tmp, 'engine');
  const { log } = quiet();

  await assert.rejects(
    ensureEngine({ pluginRoot: root, env: { GRAPH_OPS_ENGINE_DIR: engineDir }, log, ...fakeRelease({ failFile: true }), retryBackoffMs: 0 }),
    (err) => {
      assert.match(err.message, /could not obtain graph-engine v1\.2\.3/);
      assert.match(err.message, /ENOTFOUND/);
      assert.ok(err.message.includes('https://github.com/example-owner/example-repo/releases/download/v1.2.3'), err.message);
      assert.ok(err.message.includes(path.join(engineDir, 'v1.2.3', EXE)), err.message);
      return true;
    }
  );
});

test('ensureEngine: a successful download prunes other old versions from the cache', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  const engineDir = path.join(tmp, 'engine');
  const NOW = Math.floor(Date.now() / 1000) * 1000;
  const old = path.join(engineDir, 'v1.0.0');
  const recent = path.join(engineDir, 'v1.1.0');
  for (const [dir, ageMs] of [[old, 2 * 60 * 60 * 1000], [recent, 0]]) {
    writeFile(path.join(dir, EXE), 'x');
    const s = (NOW - ageMs) / 1000;
    fs.utimesSync(dir, s, s);
  }
  const { log } = quiet();

  await ensureEngine({ pluginRoot: root, env: { GRAPH_OPS_ENGINE_DIR: engineDir }, log, now: NOW, ...fakeRelease(), retryBackoffMs: 0 });
  assert.deepStrictEqual(fs.readdirSync(engineDir).sort(), ['v1.1.0', 'v1.2.3']);
});

// Captures everything written to process.stderr while fn runs.
async function captureStderr(fn) {
  const chunks = [];
  const original = process.stderr.write;
  process.stderr.write = (chunk, ...rest) => {
    chunks.push(String(chunk));
    const cb = rest.find((r) => typeof r === 'function');
    if (cb) cb();
    return true;
  };
  try {
    return { result: await fn(), stderr: chunks.join('') };
  } finally {
    process.stderr.write = original;
  }
}

// A fetchFile hook that simulates another process finishing the same install
// while this one's attempt fails (e.g. rename EPERM/EBUSY on Windows, or a
// network error after the other process already won the race).
function racingRelease({ binDir, versionFile }) {
  const urls = [];
  return {
    urls,
    fetchFile: async (url) => {
      urls.push(url);
      writeFile(path.join(binDir, EXE), 'placed by another process', 0o755);
      if (versionFile !== undefined) writeFile(path.join(binDir, '.version'), versionFile);
      throw new Error('simulated failure after another process installed the binary');
    },
    fetchText: async (url) => {
      urls.push(url);
      throw new Error('fetchText must not be reached');
    },
  };
}

test('ensureBinary (versionMarker: false) returns a binary another process placed mid-attempt without retrying or warning', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  const binDir = path.join(tmp, 'cache', 'v1.2.3');
  const release = racingRelease({ binDir });

  const { result, stderr } = await captureStderr(() =>
    ensureBinary({ pluginRoot: root, binDir, versionMarker: false, ...release, retryBackoffMs: 0 })
  );
  assert.strictEqual(result, path.join(binDir, EXE));
  assert.strictEqual(release.urls.length, 1, 'no second attempt may be made');
  assert.strictEqual(stderr, '', 'no warning may be printed');
  assert.deepStrictEqual(fs.readdirSync(binDir), [EXE], 'no temporary download file may be left behind');
});

test('ensureBinary (versionMarker: true) returns a binary + matching .version another process placed mid-attempt without retrying or warning', { skip: downloadSkip }, async (t) => {
  const root = makePluginRoot(makeTmp(t));
  const binDir = path.join(root, 'libexec');
  const release = racingRelease({ binDir, versionFile: '1.2.3\n' });

  const { result, stderr } = await captureStderr(() => ensureBinary({ pluginRoot: root, ...release, retryBackoffMs: 0 }));
  assert.strictEqual(result, path.join(binDir, EXE));
  assert.strictEqual(release.urls.length, 1, 'no second attempt may be made');
  assert.strictEqual(stderr, '', 'no warning may be printed');
});

test('ensureBinary (versionMarker: true) does not treat a stale-.version binary as up to date while retrying', { skip: downloadSkip }, async (t) => {
  const root = makePluginRoot(makeTmp(t));
  const binDir = path.join(root, 'libexec');
  const release = racingRelease({ binDir, versionFile: '1.0.0\n' });

  const { result, stderr } = await captureStderr(() => ensureBinary({ pluginRoot: root, ...release, retryBackoffMs: 0 }));
  assert.strictEqual(result, path.join(binDir, EXE));
  assert.strictEqual(release.urls.length, MAX_ATTEMPTS, 'every attempt must still be made');
  assert.match(stderr, /warning: failed to fetch graph-engine v1\.2\.3 .*falling back to the previously cached binary/);
});

test('ensureBinary removes a leftover temporary file from an earlier run before downloading', { skip: downloadSkip }, async (t) => {
  const tmp = makeTmp(t);
  const root = makePluginRoot(tmp);
  const binDir = path.join(tmp, 'cache', 'v1.2.3');
  writeFile(path.join(binDir, `${EXE}.download-${process.pid}`), 'leftover');

  const binPath = await ensureBinary({ pluginRoot: root, binDir, versionMarker: false, ...fakeRelease(), retryBackoffMs: 0 });
  assert.strictEqual(fs.readFileSync(binPath, 'utf8'), BINARY);
  assert.deepStrictEqual(fs.readdirSync(binDir), [EXE]);
});

// `bin/graph-engine` execs "$(node scripts/install-binary.js --print-path)",
// so stdout must be exactly one line: the absolute path, and nothing else.
for (const where of ['per-user cache', 'libexec']) {
  test(`install-binary.js --print-path prints exactly the ${where} binary path and exits 0 (no network)`, (t) => {
    const tmp = makeTmp(t);
    const root = copyPlugin(tmp, { version: '1.2.3' });
    const engineDir = path.join(tmp, 'engine');
    const home = path.join(tmp, 'home');
    fs.mkdirSync(home);
    const expected =
      where === 'libexec'
        ? writeFile(path.join(root, 'libexec', EXE), 'fake', 0o755)
        : writeFile(path.join(engineDir, 'v1.2.3', EXE), 'fake', 0o755);
    if (where === 'libexec') writeFile(path.join(root, 'libexec', '.version'), '1.2.3\n');

    const res = spawnSync(process.execPath, [path.join(root, 'scripts', 'install-binary.js'), '--print-path'], {
      env: { PATH: process.env.PATH, HOME: home, GRAPH_OPS_ENGINE_DIR: engineDir, LOCALAPPDATA: path.join(tmp, 'lad') },
      encoding: 'utf8',
    });
    assert.strictEqual(res.status, 0, res.stderr);
    assert.ok(path.isAbsolute(expected));
    assert.strictEqual(res.stdout, `${expected}\n`);
    assert.strictEqual(res.stdout.split('\n').length, 2, 'stdout must be a single line');
  });
}

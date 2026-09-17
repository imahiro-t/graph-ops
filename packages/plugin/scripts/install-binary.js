#!/usr/bin/env node
// Fetches the pre-built graph-engine Go binary for the current OS/arch from
// GitHub Releases, verifies it against the release's checksums.txt, and
// resolves which graph-engine binary the plugin should run.
//
// Where binaries live (see findLocalEngine / ensureEngine for the order):
//   - <pluginRoot>/libexec/graph-engine[.exe] (+ libexec/.version): filled by
//     `npm run build:go` in the monorepo, and by claude-plugin-path.js for the
//     marketplace's `command` source (a tag-pinned clone under
//     `~/.cache/graph-ops/<tag>`). Never tracked by git.
//   - <engineCacheRoot>/v<version>/graph-engine[.exe]: the per-user cache the
//     committed bin/graph-engine shim downloads into on first run when the
//     plugin was installed without a binary (e.g. through a `git-subdir`
//     source such as the community catalog, where gitignored files never
//     arrive and no install-time script runs).
//
// bin/ itself only holds the committed shims (bin/graph-engine for sh,
// bin/graph-engine.cmd for cmd/PowerShell). Claude Code adds bin/ to the
// Bash tool's PATH while the plugin is enabled, so skills keep calling a bare
// `graph-engine` and the shim picks the actual binary.
//
// Where a download comes from is fixed by the installed commit's
// .claude-plugin/plugin.json alone ("repository" + "version"): nothing in the
// environment can redirect it. GRAPH_OPS_ENGINE_DIR only relocates the
// per-user cache directory.
//
// Deliberately zero npm dependencies: this runs straight out of a fresh git
// clone / plugin cache where `npm install` has never been run, so there is no
// `node_modules` to load anything from.
'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const https = require('https');
const crypto = require('crypto');
const { pruneStaleEngineVersions } = require('./prune-plugin-cache');

// Idle-socket timeout applied to every GET (including each redirect hop):
// without this, a hung connection (dead proxy, captive portal, stalled TLS
// handshake, ...) leaves the request's Promise pending forever, which in
// turn hangs claude-plugin-path.js or the bin/graph-engine shim.
const REQUEST_TIMEOUT_MS = 10000;

// A fresh install (no cached binary yet) has no fallback to fall back to, so
// a single transient blip (DNS hiccup, TLS reset, ...) would otherwise be a
// hard failure. Retry the whole fetch-and-verify attempt a couple of times
// with a short backoff before giving up.
const MAX_ATTEMPTS = 3;
const RETRY_BACKOFF_MS = 500;

// Keep this in sync with .github/workflows/release.yml, which builds and
// uploads assets under exactly these names.
const ASSET_NAME_BY_TARGET = {
  'darwin-x64': 'graph-engine-darwin-amd64',
  'darwin-arm64': 'graph-engine-darwin-arm64',
  'linux-x64': 'graph-engine-linux-amd64',
  'win32-x64': 'graph-engine-windows-amd64.exe',
};

function currentTargetKey() {
  return `${process.platform}-${process.arch}`;
}

function assetNameForCurrentTarget() {
  const key = currentTargetKey();
  const asset = ASSET_NAME_BY_TARGET[key];
  if (!asset) {
    const supported = Object.keys(ASSET_NAME_BY_TARGET).join(', ');
    throw new Error(
      `no prebuilt graph-engine binary is published for this platform (${key}). ` +
        `Supported platforms: ${supported}. You can build packages/core-go from ` +
        `source (\`go build ./cmd/graph-engine\`) and place the result on PATH as ` +
        `"graph-engine" instead.`
    );
  }
  return asset;
}

function localExeName(platform = process.platform) {
  return platform === 'win32' ? 'graph-engine.exe' : 'graph-engine';
}

// Reads .claude-plugin/plugin.json, returning both the parsed manifest and
// the path it came from so callers can name that path in their errors.
function readPluginJson(pluginRoot) {
  const pluginJsonPath = path.join(pluginRoot, '.claude-plugin', 'plugin.json');
  return { pluginJsonPath, pluginJson: JSON.parse(fs.readFileSync(pluginJsonPath, 'utf8')) };
}

// Reads the plugin's own version from .claude-plugin/plugin.json. Releases
// are tagged `v<version>` (see README's versioning note) -- the plugin only
// ever asks GitHub Releases for the tag matching its own version, so a given
// plugin version always resolves to one fixed, reproducible binary.
function pluginVersion(pluginRoot) {
  const { pluginJsonPath, pluginJson } = readPluginJson(pluginRoot);
  if (!pluginJson.version) {
    throw new Error(`${pluginJsonPath} has no "version" field`);
  }
  return pluginJson.version;
}

// The GitHub repo to fetch releases from, as "owner/repo", taken only from
// .claude-plugin/plugin.json's "repository" field. There is intentionally no
// environment-variable override: the download source must be determined by
// the installed plugin content itself, not by the environment graph-engine
// happens to be launched from.
function releaseRepo(pluginRoot) {
  const { pluginJsonPath, pluginJson } = readPluginJson(pluginRoot);
  const repoUrl = typeof pluginJson.repository === 'string' ? pluginJson.repository : pluginJson.repository && pluginJson.repository.url;
  if (!repoUrl) {
    throw new Error(
      `${pluginJsonPath} has no "repository" field -- ` +
        `don't know which GitHub repo to download release binaries from.`
    );
  }
  const match = repoUrl.match(/github\.com[/:]([^/]+)\/([^/.]+?)(?:\.git)?\/?$/);
  if (!match) {
    throw new Error(`could not parse a GitHub "owner/repo" out of repository URL: ${repoUrl}`);
  }
  return `${match[1]}/${match[2]}`;
}

// The GitHub Releases directory every asset of this plugin version lives
// under. The binary and the checksums.txt that verifies it must always come
// from the same release, so both URLs are built from this one place.
function releaseBaseUrl(pluginRoot) {
  const repo = releaseRepo(pluginRoot);
  const version = pluginVersion(pluginRoot);
  return `https://github.com/${repo}/releases/download/v${version}`;
}

// Issues a GET, follows redirects (up to redirectsLeft hops), and resolves
// with the final 200 response so the caller can consume its body however it
// needs (piped to a file for the binary, buffered to a string for the
// checksums manifest). A REQUEST_TIMEOUT_MS idle-socket timeout is applied
// to *every* hop -- set via the `timeout` option (so it also covers the
// initial connect/TLS handshake) and backed by an explicit `timeout`
// listener that destroys the socket and rejects, since Node's `timeout`
// event does not do that on its own.
function requestFollowingRedirects(url, redirectsLeft = 5) {
  return new Promise((resolve, reject) => {
    const req = https.get(url, { timeout: REQUEST_TIMEOUT_MS }, (res) => {
      const { statusCode, headers } = res;
      if (statusCode >= 300 && statusCode < 400 && headers.location) {
        res.resume();
        if (redirectsLeft <= 0) {
          reject(new Error(`too many redirects fetching ${url}`));
          return;
        }
        requestFollowingRedirects(headers.location, redirectsLeft - 1).then(resolve, reject);
        return;
      }
      if (statusCode !== 200) {
        res.resume();
        reject(new Error(`GET ${url} -> HTTP ${statusCode}`));
        return;
      }
      resolve(res);
    });
    req.on('error', reject);
    req.on('timeout', () => {
      req.destroy(new Error(`timed out after ${REQUEST_TIMEOUT_MS}ms fetching ${url}`));
    });
  });
}

async function httpGetFollowingRedirects(url, destPath) {
  const res = await requestFollowingRedirects(url);
  await new Promise((resolve, reject) => {
    const file = fs.createWriteStream(destPath);
    res.pipe(file);
    file.on('finish', () => file.close(() => resolve()));
    file.on('error', reject);
    res.on('error', reject);
  });
}

async function httpGetTextFollowingRedirects(url) {
  const res = await requestFollowingRedirects(url);
  return new Promise((resolve, reject) => {
    const chunks = [];
    res.on('data', (chunk) => chunks.push(chunk));
    res.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    res.on('error', reject);
  });
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function sha256File(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash('sha256');
    const stream = fs.createReadStream(filePath);
    stream.on('data', (chunk) => hash.update(chunk));
    stream.on('error', reject);
    stream.on('end', () => resolve(hash.digest('hex')));
  });
}

// Parses the `checksums.txt` asset produced by .github/workflows/release.yml
// (plain `sha256sum *` output: "<64-hex-digest>  <filename>" per line, GNU
// coreutils' two-space "text mode" separator, optionally a `*` in front of
// the filename for "binary mode") into a Map from asset filename to the
// lowercase hex digest expected for it.
function parseChecksums(text) {
  const map = new Map();
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim();
    if (!line) continue;
    const match = line.match(/^([0-9a-fA-F]{64})\s+\*?(.+)$/);
    if (!match) continue;
    map.set(match[2].trim(), match[1].toLowerCase());
  }
  return map;
}

// Fetches checksums.txt from the same GitHub Release as the binary and
// returns the expected SHA256 digest for `assetName`, throwing if the
// manifest can't be fetched or doesn't cover that asset. There is no
// "proceed unverified" fallback here: a release with no integrity manifest,
// or one missing this platform's entry, is treated the same as a corrupt
// download (see ensureBinary's existing offline/cached-binary fallback,
// which still applies on top of this).
async function fetchExpectedChecksum(pluginRoot, assetName, fetchText = httpGetTextFollowingRedirects) {
  const url = `${releaseBaseUrl(pluginRoot)}/checksums.txt`;
  const text = await fetchText(url);
  const digest = parseChecksums(text).get(assetName);
  if (!digest) {
    throw new Error(`checksums.txt fetched from ${url} has no entry for ${assetName}`);
  }
  return digest;
}

function readCachedVersion(binDir) {
  try {
    return fs.readFileSync(path.join(binDir, '.version'), 'utf8').trim();
  } catch {
    return null; // no cached binary yet, or an unreadable marker file
  }
}

function writeCachedVersion(binDir, version) {
  fs.writeFileSync(path.join(binDir, '.version'), `${version}\n`);
}

// Downloads the binary asset to tmpPath and verifies its SHA256 against the
// digest published in the release's checksums.txt (see .github/workflows/
// release.yml), throwing if the download is empty, unfetchable, or doesn't
// match. Integrity verification is not optional/best-effort: a binary that
// fails this check is treated exactly like a failed download by the caller
// (see ensureBinary's cached-binary fallback / hard failure), never used.
//
// fetchFile/fetchText exist only so tests can exercise this without a
// network. The URLs are always built from plugin.json; the hooks are not
// reachable from the environment or the command line.
async function downloadAndVerify(
  pluginRoot,
  tmpPath,
  { fetchFile = httpGetFollowingRedirects, fetchText = httpGetTextFollowingRedirects } = {}
) {
  const baseUrl = releaseBaseUrl(pluginRoot);
  const asset = assetNameForCurrentTarget();
  const url = `${baseUrl}/${asset}`;
  await fetchFile(url, tmpPath);
  const stat = fs.statSync(tmpPath);
  if (stat.size === 0) {
    throw new Error(`downloaded file from ${url} is empty`);
  }
  const expectedDigest = await fetchExpectedChecksum(pluginRoot, asset, fetchText);
  const actualDigest = await sha256File(tmpPath);
  if (actualDigest !== expectedDigest) {
    throw new Error(
      `checksum mismatch for ${url}: expected sha256 ${expectedDigest}, got ${actualDigest} -- ` +
        `refusing to install a binary that doesn't match its published checksum`
    );
  }
}

/**
 * Ensures <binDir>/graph-engine[.exe] exists and matches the plugin's own
 * version, downloading it from GitHub Releases if needed. binDir defaults to
 * <pluginRoot>/libexec -- never bin/, which only holds the committed shims.
 *
 * With versionMarker (the default) the version is tracked by a
 * <binDir>/.version file, for a directory that holds "whatever version was
 * last installed" (libexec/). With versionMarker: false the directory itself
 * is version-specific (the per-user cache's v<version>/), so the binary
 * existing is enough and no marker is written.
 *
 * Every download is verified against the SHA256 checksum published
 * alongside it in the release's checksums.txt before it is ever chmod'd or
 * treated as trusted; a mismatch, an unparseable/missing manifest entry, or
 * an empty download are all treated the same as a network failure. A
 * transient failure (network blip, momentary checksum-fetch hiccup) is
 * retried a couple of times with a short backoff before giving up, since a
 * fresh install has no cached binary to fall back to.
 *
 * The download goes to a per-process temporary file inside binDir and is
 * renamed into place only after verification, so concurrent first runs never
 * expose a partial or unverified binary.
 *
 * Returns the absolute path to the binary. Never re-downloads when the
 * cached binary already matches the plugin's version (so this is cheap and
 * network-free on every run after the first). If every attempt fails (e.g.
 * offline, or the release's checksums.txt doesn't match) and a
 * previously-downloaded binary of *any* version is still present in binDir,
 * that binary is kept and used, with a warning on stderr -- only throws when
 * there is no usable binary at all.
 *
 * fetchFile/fetchText/retryBackoffMs are test-only hooks (see
 * downloadAndVerify).
 */
async function ensureBinary({
  pluginRoot,
  binDir = path.join(pluginRoot, 'libexec'),
  versionMarker = true,
  fetchFile,
  fetchText,
  retryBackoffMs = RETRY_BACKOFF_MS,
}) {
  const exe = localExeName();
  const binPath = path.join(binDir, exe);
  const version = pluginVersion(pluginRoot);

  if (fs.existsSync(binPath) && (!versionMarker || readCachedVersion(binDir) === version)) {
    return binPath; // already up to date, no network needed
  }

  fs.mkdirSync(binDir, { recursive: true });
  const tmpPath = `${binPath}.download-${process.pid}`;
  let lastErr;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    try {
      await downloadAndVerify(pluginRoot, tmpPath, { fetchFile, fetchText });
      if (process.platform !== 'win32') {
        fs.chmodSync(tmpPath, 0o755);
      }
      fs.renameSync(tmpPath, binPath);
      if (versionMarker) {
        writeCachedVersion(binDir, version);
      }
      return binPath;
    } catch (err) {
      lastErr = err;
      try {
        fs.unlinkSync(tmpPath);
      } catch {
        // the partial download may never have been created; nothing to clean up
      }
      if (attempt < MAX_ATTEMPTS) {
        await sleep(retryBackoffMs * attempt);
      }
    }
  }

  if (fs.existsSync(binPath)) {
    process.stderr.write(
      `graph-ops: warning: failed to fetch graph-engine v${version} after ${MAX_ATTEMPTS} attempts ` +
        `(${lastErr.message}); falling back to the previously cached binary at ${binPath}.\n`
    );
    return binPath;
  }
  throw new Error(`failed to install graph-engine binary after ${MAX_ATTEMPTS} attempts: ${lastErr.message}`);
}

/**
 * The per-user directory that holds one v<version>/ subdirectory per
 * downloaded graph-engine version. Resolution order:
 *   1. GRAPH_OPS_ENGINE_DIR (relocates the cache only; never the download
 *      source).
 *   2. win32: %LOCALAPPDATA%\graph-ops\engine
 *   3. otherwise: ${XDG_CACHE_HOME:-$HOME/.cache}/graph-ops/engine
 *      (a relative XDG_CACHE_HOME is ignored, as the XDG spec requires).
 *
 * CLAUDE_PLUGIN_DATA is deliberately not consulted: Claude Code does not pass
 * it to Bash tool commands, so the shim could never rely on it, and using it
 * only sometimes would split the cache in two.
 *
 * The bin/graph-engine sh shim mirrors this; keep them in sync.
 */
function engineCacheRoot({ env = process.env, platform = process.platform } = {}) {
  if (env.GRAPH_OPS_ENGINE_DIR) {
    return path.resolve(env.GRAPH_OPS_ENGINE_DIR);
  }
  if (platform === 'win32') {
    const localAppData = env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local');
    return path.join(localAppData, 'graph-ops', 'engine');
  }
  const xdg = env.XDG_CACHE_HOME;
  const cacheHome = xdg && path.isAbsolute(xdg) ? xdg : path.join(env.HOME || os.homedir(), '.cache');
  return path.join(cacheHome, 'graph-ops', 'engine');
}

function engineCacheDir(pluginRoot, options = {}) {
  return path.join(engineCacheRoot(options), `v${pluginVersion(pluginRoot)}`);
}

/**
 * Looks for an already-present binary without touching the network, in this
 * order (the first hit wins):
 *   1. <pluginRoot>/libexec/graph-engine[.exe], only when libexec/.version
 *      equals plugin.json's version.
 *   2. <pluginRoot>/../core-go/graph-engine[.exe] -- the monorepo's own
 *      `go build` output; only exists when running from a checkout. Its
 *      version is not checked (same as the previous resolve-binary.js).
 *   3. <engineCacheRoot>/v<version>/graph-engine[.exe] -- only ever holds a
 *      checksum-verified binary renamed into place, so presence is enough.
 * Returns the absolute path, or null when nothing usable is present.
 *
 * Used by ensureEngine and resolve-binary.js, and mirrored by the
 * bin/graph-engine sh shim; keep all of them in sync.
 */
function findLocalEngine({ pluginRoot, env = process.env, platform = process.platform } = {}) {
  const exe = localExeName(platform);
  let version = null;
  try {
    version = pluginVersion(pluginRoot);
  } catch {
    // without a readable version only the unversioned monorepo build applies
  }

  const libexecDir = path.join(pluginRoot, 'libexec');
  const libexecBin = path.join(libexecDir, exe);
  if (version && fs.existsSync(libexecBin) && readCachedVersion(libexecDir) === version) {
    return libexecBin;
  }

  const monorepoBuild = path.resolve(pluginRoot, '..', 'core-go', exe);
  if (fs.existsSync(monorepoBuild)) {
    return monorepoBuild;
  }

  if (version) {
    const cached = path.join(engineCacheRoot({ env, platform }), `v${version}`, exe);
    if (fs.existsSync(cached)) {
      return cached;
    }
  }
  return null;
}

function describeAssetName() {
  try {
    return assetNameForCurrentTarget();
  } catch {
    return `graph-engine build for ${currentTargetKey()}`;
  }
}

/**
 * Returns the path of the graph-engine binary to run, downloading it into
 * the per-user cache first when nothing usable is present:
 *   1-3. findLocalEngine (libexec with matching .version, monorepo build,
 *        per-user cache).
 *   4.   Download into <engineCacheRoot>/v<version>/ via ensureBinary
 *        (checksum-verified). After a successful download, other versions'
 *        cache directories untouched for an hour are pruned (best-effort).
 *   5.   If the download fails: a libexec binary of *any* version is used
 *        with a warning; otherwise this throws with the reason and where a
 *        binary can be placed by hand.
 *
 * Progress, warnings and errors only ever go to `log` (stderr by default):
 * stdout belongs to graph-engine itself, whose JSON output the skills parse.
 */
async function ensureEngine({ pluginRoot, env = process.env, now, log = (message) => process.stderr.write(message), ...installOptions }) {
  const local = findLocalEngine({ pluginRoot, env });
  if (local) {
    return local;
  }

  const version = pluginVersion(pluginRoot);
  const engineRoot = engineCacheRoot({ env });
  const cacheDir = path.join(engineRoot, `v${version}`);
  const exe = localExeName();
  const libexecBin = path.join(pluginRoot, 'libexec', exe);

  let binPath;
  try {
    log(`graph-ops: downloading graph-engine v${version} (${assetNameForCurrentTarget()}) into ${cacheDir} (first run only)...\n`);
    binPath = await ensureBinary({ pluginRoot, binDir: cacheDir, versionMarker: false, ...installOptions });
  } catch (err) {
    if (fs.existsSync(libexecBin)) {
      log(
        `graph-ops: warning: ${err.message}; falling back to ${libexecBin}, ` +
          `which was not built for plugin version ${version}.\n`
      );
      return libexecBin;
    }
    let source = 'the GitHub release';
    try {
      source = releaseBaseUrl(pluginRoot);
    } catch {
      // plugin.json is broken; the original error already says so
    }
    throw new Error(
      `graph-ops: could not obtain graph-engine v${version}: ${err.message}\n` +
        `graph-ops: to install it by hand, download ${describeAssetName()} from ${source}, ` +
        `verify it against checksums.txt in the same release, and save it as ${path.join(cacheDir, exe)} (executable).`
    );
  }

  log(`graph-ops: graph-engine v${version} ready at ${binPath}\n`);
  pruneStaleEngineVersions({
    engineRoot,
    keepVersion: version,
    now,
    warn: (message) => log(`graph-ops: warning: ${message}\n`),
  });
  return binPath;
}

module.exports = {
  ensureBinary,
  ensureEngine,
  findLocalEngine,
  engineCacheRoot,
  engineCacheDir,
  assetNameForCurrentTarget,
  releaseRepo,
  releaseBaseUrl,
  pluginVersion,
  parseChecksums,
  localExeName,
  MAX_ATTEMPTS,
  REQUEST_TIMEOUT_MS,
  RETRY_BACKOFF_MS,
};

// Command line:
//   node install-binary.js               install into <pluginRoot>/libexec/
//   node install-binary.js --print-path  resolve the binary to run (downloading
//                                        into the per-user cache if needed)
//                                        and print only its path on stdout;
//                                        used by the bin/graph-engine shim
if (require.main === module) {
  const pluginRoot = path.resolve(__dirname, '..');
  const printPath = process.argv.slice(2).includes('--print-path');
  const run = printPath ? ensureEngine({ pluginRoot }) : ensureBinary({ pluginRoot });
  run
    .then((binPath) => {
      if (printPath) {
        process.stdout.write(`${binPath}\n`);
      } else {
        process.stderr.write(`graph-engine binary ready at ${binPath}\n`);
      }
    })
    .catch((err) => {
      process.stderr.write(`${err.message}\n`);
      process.exitCode = 1;
    });
}

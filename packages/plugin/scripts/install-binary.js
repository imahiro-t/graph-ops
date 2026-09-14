#!/usr/bin/env node
// Fetches the pre-built graph-engine Go binary for the current OS/arch from
// GitHub Releases and places it under <pluginRoot>/bin/, so that plugin.json
// (whose plugin root gets added to PATH by Claude Code while the plugin is
// enabled) exposes a bare `graph-engine` command with no other setup.
//
// This is the production counterpart to the monorepo's local `go build`:
// nothing here depends on packages/core-go existing on disk, so it works
// when `packages/plugin` runs on its own -- in production, from inside the
// tag-pinned clone that the marketplace's `command` source keeps under
// `~/.cache/graph-ops/<tag>` (see scripts/claude-plugin-path.js).
//
// Deliberately zero npm dependencies: this runs straight out of that fresh
// git clone, where `npm install` has never been run, so there is no
// `node_modules` to load anything from.
'use strict';

const fs = require('fs');
const path = require('path');
const https = require('https');
const crypto = require('crypto');

// Idle-socket timeout applied to every GET (including each redirect hop):
// without this, a hung connection (dead proxy, captive portal, stalled TLS
// handshake, ...) leaves the request's Promise pending forever, which in
// turn hangs claude-plugin-path.js -- and that script is invoked in the
// background on every session start while the plugin is enabled, so a hang
// here is a silent, hard-to-diagnose availability failure for the user.
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

function localExeName() {
  return process.platform === 'win32' ? 'graph-engine.exe' : 'graph-engine';
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

// The GitHub repo to fetch releases from, as "owner/repo". Resolution order:
//   1. GRAPH_OPS_RELEASE_REPO env var (escape hatch / testing).
//   2. .claude-plugin/plugin.json's "repository" field.
function releaseRepo(pluginRoot) {
  if (process.env.GRAPH_OPS_RELEASE_REPO) {
    return process.env.GRAPH_OPS_RELEASE_REPO;
  }
  const { pluginJsonPath, pluginJson } = readPluginJson(pluginRoot);
  const repoUrl = typeof pluginJson.repository === 'string' ? pluginJson.repository : pluginJson.repository && pluginJson.repository.url;
  if (!repoUrl) {
    throw new Error(
      `${pluginJsonPath} has no "repository" field, and GRAPH_OPS_RELEASE_REPO is not set -- ` +
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
async function fetchExpectedChecksum(pluginRoot, assetName) {
  const url = `${releaseBaseUrl(pluginRoot)}/checksums.txt`;
  const text = await httpGetTextFollowingRedirects(url);
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
async function downloadAndVerify(pluginRoot, tmpPath) {
  const baseUrl = releaseBaseUrl(pluginRoot);
  const asset = assetNameForCurrentTarget();
  const url = `${baseUrl}/${asset}`;
  await httpGetFollowingRedirects(url, tmpPath);
  const stat = fs.statSync(tmpPath);
  if (stat.size === 0) {
    throw new Error(`downloaded file from ${url} is empty`);
  }
  const expectedDigest = await fetchExpectedChecksum(pluginRoot, asset);
  const actualDigest = await sha256File(tmpPath);
  if (actualDigest !== expectedDigest) {
    throw new Error(
      `checksum mismatch for ${url}: expected sha256 ${expectedDigest}, got ${actualDigest} -- ` +
        `refusing to install a binary that doesn't match its published checksum`
    );
  }
}

/**
 * Ensures <pluginRoot>/bin/graph-engine[.exe] exists and matches the
 * plugin's own version, downloading it from GitHub Releases if needed.
 *
 * Every download is verified against the SHA256 checksum published
 * alongside it in the release's checksums.txt before it is ever chmod'd or
 * treated as trusted; a mismatch, an unparseable/missing manifest entry, or
 * an empty download are all treated the same as a network failure. A
 * transient failure (network blip, momentary checksum-fetch hiccup) is
 * retried a couple of times with a short backoff before giving up, since a
 * fresh install has no cached binary to fall back to.
 *
 * Returns the absolute path to the binary. Never re-downloads when the
 * cached binary already matches the plugin's version (so this is cheap and
 * network-free on every run after the first). If every attempt fails (e.g.
 * offline, or the release's checksums.txt doesn't match) and a
 * previously-downloaded binary of *any* version is still present, that
 * binary is kept and used, with a warning on stderr -- only throws when
 * there is no usable binary at all.
 */
async function ensureBinary({ pluginRoot }) {
  const binDir = path.join(pluginRoot, 'bin');
  const exe = localExeName();
  const binPath = path.join(binDir, exe);
  const version = pluginVersion(pluginRoot);

  if (fs.existsSync(binPath) && readCachedVersion(binDir) === version) {
    return binPath; // already up to date, no network needed
  }

  fs.mkdirSync(binDir, { recursive: true });
  const tmpPath = `${binPath}.download-${process.pid}`;
  let lastErr;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    try {
      await downloadAndVerify(pluginRoot, tmpPath);
      fs.renameSync(tmpPath, binPath);
      if (process.platform !== 'win32') {
        fs.chmodSync(binPath, 0o755);
      }
      writeCachedVersion(binDir, version);
      return binPath;
    } catch (err) {
      lastErr = err;
      try {
        fs.unlinkSync(tmpPath);
      } catch {
        // the partial download may never have been created; nothing to clean up
      }
      if (attempt < MAX_ATTEMPTS) {
        await sleep(RETRY_BACKOFF_MS * attempt);
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

module.exports = { ensureBinary, assetNameForCurrentTarget, releaseRepo, pluginVersion, parseChecksums };

if (require.main === module) {
  const pluginRoot = path.resolve(__dirname, '..');
  ensureBinary({ pluginRoot })
    .then((binPath) => {
      process.stderr.write(`graph-engine binary ready at ${binPath}\n`);
    })
    .catch((err) => {
      process.stderr.write(`${err.message}\n`);
      process.exitCode = 1;
    });
}

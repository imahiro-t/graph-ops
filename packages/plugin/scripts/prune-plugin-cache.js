// Removes stale plugin caches left behind by the marketplace's `command`
// source (see /.claude-plugin/marketplace.json and
// scripts/claude-plugin-path.js).
//
// That command keeps one shallow, tag-pinned git clone per release under
// `~/.cache/graph-ops/<tag>`, cloning into a per-process `<tag>~<pid>`
// directory first and renaming it into place. Nothing in the one-line command
// itself ever deletes an old version, and an interrupted clone (killed by
// Claude Code's timeout, a crash, ...) leaves its `<tag>~<pid>` directory
// behind, so this runs from claude-plugin-path.js on every invocation to clean
// both up.
//
// Rules:
//   - Only acts when pluginRoot has exactly the shape
//     `<base>/<tag>/packages/plugin` with `<base>` named `graph-ops` and its
//     parent named `.cache`. Anything else (a monorepo checkout, a local dev
//     marketplace, ...) is left completely alone.
//   - Candidates are the entries directly under `<base>` whose name starts
//     with `v<major>.<minor>.<patch>` -- other versions, and `~<pid>`
//     temporary clone directories -- except this plugin's own `<tag>`.
//   - A candidate is removed only once its mtime is at least maxAgeMs
//     (default 60 minutes) old, so a clone another process is still writing
//     is never pulled out from under it.
//   - Never throws: a failure to remove one entry is reported on stderr and
//     the remaining entries are still processed.
//
// Deliberately zero npm dependencies, like install-binary.js: this runs
// straight out of a fresh git clone with no `node_modules`.
'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_MAX_AGE_MS = 60 * 60 * 1000;
const TAG_PATTERN = /^v\d+\.\d+\.\d+/;

function defaultWarn(message) {
  process.stderr.write(`graph-ops: warning: ${message}\n`);
}

// Returns { base, ownTag } when pluginRoot is `<...>/.cache/graph-ops/<tag>/packages/plugin`,
// or null for any other layout.
function cacheLayout(pluginRoot) {
  const root = path.resolve(pluginRoot);
  const packagesDir = path.dirname(root);
  const tagDir = path.dirname(packagesDir);
  const base = path.dirname(tagDir);
  if (path.basename(root) !== 'plugin' || path.basename(packagesDir) !== 'packages') {
    return null;
  }
  const ownTag = path.basename(tagDir);
  if (!TAG_PATTERN.test(ownTag) || ownTag.includes('~')) {
    return null;
  }
  if (path.basename(base) !== 'graph-ops' || path.basename(path.dirname(base)) !== '.cache') {
    return null;
  }
  return { base, ownTag };
}

/**
 * Deletes stale version caches / interrupted clone directories next to the
 * running plugin's own cache. See the file header for the exact rules.
 *
 * @param {object} options
 * @param {string} options.pluginRoot  the running plugin's root (…/packages/plugin)
 * @param {number} [options.now]       current time in ms (injectable for tests)
 * @param {number} [options.maxAgeMs]  minimum age before an entry is removed
 * @param {(message: string) => void} [options.warn] receives per-entry failures
 */
function pruneStalePluginCaches({ pluginRoot, now = Date.now(), maxAgeMs = DEFAULT_MAX_AGE_MS, warn = defaultWarn } = {}) {
  try {
    const layout = cacheLayout(pluginRoot);
    if (!layout) {
      return;
    }
    const { base, ownTag } = layout;
    for (const name of fs.readdirSync(base)) {
      if (name === ownTag || !TAG_PATTERN.test(name)) {
        continue;
      }
      const entryPath = path.join(base, name);
      try {
        const { mtimeMs } = fs.lstatSync(entryPath);
        if (now - mtimeMs >= maxAgeMs) {
          fs.rmSync(entryPath, { recursive: true, force: true });
        }
      } catch (err) {
        warn(`failed to remove stale plugin cache ${entryPath}: ${err.message}`);
      }
    }
  } catch (err) {
    try {
      warn(`failed to prune stale plugin caches: ${err.message}`);
    } catch {
      // a broken warn callback must not turn cleanup into a failure either
    }
  }
}

const ENGINE_VERSION_DIR_PATTERN = /^v\d+\.\d+\.\d+$/;

/**
 * Deletes other versions' directories from the per-user graph-engine cache
 * (`<engineRoot>/v<version>/`, see engineCacheRoot in install-binary.js).
 * Called only right after a new version was successfully downloaded there.
 *
 * Rules:
 *   - Candidates are the entries directly under engineRoot named exactly
 *     `v<major>.<minor>.<patch>`, except `v<keepVersion>`. Anything else
 *     (unexpected files, pre-release-looking names, ...) is left alone.
 *   - A candidate is removed only once its mtime is at least maxAgeMs
 *     (default 60 minutes) old, so a download another process is still
 *     writing into it is never pulled out from under it.
 *   - Never throws: a failure to remove one entry (e.g. a binary that is
 *     still running on Windows) is reported through warn and the remaining
 *     entries are still processed. A plugin install still on an old version
 *     simply downloads that version again on its next run.
 *
 * Kept separate from pruneStalePluginCaches, which only ever looks at the
 * `command` source's tag clones and never at the `engine/` directory.
 *
 * @param {object} options
 * @param {string} options.engineRoot   the per-user engine cache root
 * @param {string} options.keepVersion  the plugin version to keep (without "v")
 * @param {number} [options.now]        current time in ms (injectable for tests)
 * @param {number} [options.maxAgeMs]   minimum age before an entry is removed
 * @param {(message: string) => void} [options.warn] receives per-entry failures
 */
function pruneStaleEngineVersions({ engineRoot, keepVersion, now = Date.now(), maxAgeMs = DEFAULT_MAX_AGE_MS, warn = defaultWarn } = {}) {
  try {
    const keep = `v${keepVersion}`;
    for (const name of fs.readdirSync(engineRoot)) {
      if (name === keep || !ENGINE_VERSION_DIR_PATTERN.test(name)) {
        continue;
      }
      const entryPath = path.join(engineRoot, name);
      try {
        const stat = fs.lstatSync(entryPath);
        if (stat.isDirectory() && now - stat.mtimeMs >= maxAgeMs) {
          fs.rmSync(entryPath, { recursive: true, force: true });
        }
      } catch (err) {
        warn(`failed to remove stale graph-engine cache ${entryPath}: ${err.message}`);
      }
    }
  } catch (err) {
    try {
      warn(`failed to prune stale graph-engine caches: ${err.message}`);
    } catch {
      // a broken warn callback must not turn cleanup into a failure either
    }
  }
}

module.exports = { pruneStalePluginCaches, pruneStaleEngineVersions, DEFAULT_MAX_AGE_MS };

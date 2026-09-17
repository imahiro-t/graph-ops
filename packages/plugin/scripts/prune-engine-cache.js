// Removes other versions' graph-engine binaries from the per-user engine
// cache that the bin/graph-engine shim downloads into (see engineCacheRoot in
// install-binary.js). See pruneStaleEngineVersions for the exact rules.
//
// Deliberately zero npm dependencies, like install-binary.js: this runs
// straight out of the plugin cache with no `node_modules`.
'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_MAX_AGE_MS = 60 * 60 * 1000;

function defaultWarn(message) {
  process.stderr.write(`graph-ops: warning: ${message}\n`);
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

module.exports = { pruneStaleEngineVersions, DEFAULT_MAX_AGE_MS };

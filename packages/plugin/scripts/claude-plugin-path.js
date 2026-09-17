#!/usr/bin/env node
// Entry point for the marketplace's `command`-source plugin entry (see
// /.claude-plugin/marketplace.json at the repo root). Per Claude Code's
// contract for a `command` source, this must:
//   - print exactly one line on stdout: the absolute path of a directory
//     that already contains the complete, ready-to-use plugin content
//     (.claude-plugin/, skills/, agents/, commands/, defaults/, ...);
//   - exit 0 on success;
//   - on failure, print nothing on stdout, write a reason to stderr, and
//     exit non-zero.
//
// How this gets here: the marketplace's `command` is a single `node -e` line
// (no npm involved) that makes a shallow `git clone` of this repository,
// pinned to the release tag `v<version>`, into `~/.cache/graph-ops/<tag>`
// (reusing it as-is when it already exists), and then does
// `require('<that clone>/packages/plugin')`. That resolves through
// packages/plugin/package.json's "main" to this file, and main() below runs
// as soon as the file is required -- there is deliberately no
// `require.main === module` check.
//
// __dirname is therefore inside the cached clone's packages/plugin, so the
// plugin content itself is already in place -- the only thing this needs to
// prepare before printing the path is the platform-specific graph-engine
// binary under libexec/ (bin/ only holds the committed bin/graph-engine shim,
// which runs libexec/graph-engine when libexec/.version matches the plugin
// version, so a `command`-source install never downloads on first run). It
// also prunes stale caches of other versions (see
// scripts/prune-plugin-cache.js); that is best-effort and never affects the
// printed path or the exit code.
'use strict';

const path = require('path');
const { ensureBinary } = require('./install-binary');
const { pruneStalePluginCaches } = require('./prune-plugin-cache');

async function main() {
  const pluginRoot = path.resolve(__dirname, '..');
  await ensureBinary({ pluginRoot });
  try {
    pruneStalePluginCaches({ pluginRoot });
  } catch (err) {
    process.stderr.write(`graph-ops: warning: failed to prune stale plugin caches: ${err.message}\n`);
  }
  process.stdout.write(`${pluginRoot}\n`);
}

main().catch((err) => {
  process.stderr.write(`${err.message}\n`);
  process.exit(1);
});

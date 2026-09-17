#!/usr/bin/env node
// Locates how the plugin's commands/*.js scripts (Node-side wrappers) should
// spawn graph-engine. Returns `{ command, args }`: spawn `command` with
// `[...args, <subcommand>, ...]`.
//
// Resolution order (shared with scripts/install-binary.js's findLocalEngine
// and mirrored by the bin/graph-engine sh shim):
//
//   1. <pluginRoot>/libexec/graph-engine[.exe] when libexec/.version matches
//      plugin.json's version -- filled by `npm run build:go`.
//   2. <pluginRoot>/../core-go/graph-engine[.exe] -- monorepo `go build`
//      output, for a checkout.
//   3. <engineCacheRoot>/v<version>/graph-engine[.exe] -- the per-user cache
//      the shim downloads into (see engineCacheRoot in install-binary.js).
//   4. Nothing present yet: go through the shim, which downloads the binary
//      (checksum-verified) and then runs it.
//        - POSIX: <pluginRoot>/bin/graph-engine (an executable sh script).
//        - win32: `node scripts/engine-shim.js`, because spawnSync without a
//          shell cannot run bin/graph-engine.cmd.
//
// This module is only consulted by commands/*.js. SKILL/agent instructions
// call the bare `graph-engine` command, which resolves to the committed
// bin/graph-engine shim: a plugin's own `bin/` directory is added to the Bash
// tool's PATH while the plugin is enabled.
'use strict';

const path = require('path');
const { findLocalEngine } = require('./install-binary');

function resolveBinary({ pluginRoot = path.resolve(__dirname, '..'), env = process.env, platform = process.platform } = {}) {
  const found = findLocalEngine({ pluginRoot, env, platform });
  if (found) {
    return { command: found, args: [] };
  }
  if (platform === 'win32') {
    return { command: process.execPath, args: [path.join(pluginRoot, 'scripts', 'engine-shim.js')] };
  }
  return { command: path.join(pluginRoot, 'bin', 'graph-engine'), args: [] };
}

module.exports = { resolveBinary };

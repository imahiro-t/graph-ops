#!/usr/bin/env node
// Locates the graph-engine Go binary that the plugin's commands/*.js scripts
// (Node-side wrappers) should spawn. Resolution order:
//
//   1. packages/plugin/bin/graph-engine[.exe] -- where a real install places
//      the binary: install-binary.js writes it here when the plugin is
//      fetched via the marketplace's `command` source (see
//      scripts/claude-plugin-path.js), and `npm run build:go` copies the
//      locally-built binary here too so the dev flow uses the same path.
//   2. ../../core-go/graph-engine[.exe] -- monorepo dev fallback, kept for
//      anyone who has only run `go build` directly under packages/core-go
//      without going through `npm run build:go`.
//   3. "graph-engine" on PATH -- last resort.
//
// This module is only consulted by commands/*.js (Node-invoked CLI wrappers).
// SKILL/agent instructions call the bare `graph-engine` command directly and
// rely on (1): a plugin's own `bin/` directory is added to PATH automatically
// while the plugin is enabled.
const path = require('path');
const fs = require('fs');

function resolveBinary() {
  const exe = process.platform === 'win32' ? 'graph-engine.exe' : 'graph-engine';

  const pluginLocalBin = path.resolve(__dirname, '../bin', exe);
  if (fs.existsSync(pluginLocalBin)) {
    return pluginLocalBin;
  }

  const monorepoBuild = path.resolve(__dirname, '../../core-go', exe);
  if (fs.existsSync(monorepoBuild)) {
    return monorepoBuild;
  }

  return 'graph-engine';
}

module.exports = { resolveBinary };

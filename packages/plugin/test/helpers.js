'use strict';
// Shared fixtures for the plugin's binary-resolution tests.

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');

const pluginDir = path.resolve(__dirname, '..');

function makeTmp(t, prefix = 'graph-ops-test-') {
  const tmp = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), prefix)));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  return tmp;
}

// Copies packages/plugin alone (no sibling core-go, no local binaries) to
// <dir>/plugin, the way a git-subdir install sees it, and returns that root.
function copyPlugin(dir, { version } = {}) {
  const root = path.join(dir, 'plugin');
  fs.cpSync(pluginDir, root, {
    recursive: true,
    filter: (src) => {
      const rel = path.relative(pluginDir, src);
      const top = rel.split(path.sep)[0];
      return top !== 'libexec' && top !== 'node_modules' && top !== 'test';
    },
  });
  if (version) {
    const pluginJsonPath = path.join(root, '.claude-plugin', 'plugin.json');
    const pluginJson = JSON.parse(fs.readFileSync(pluginJsonPath, 'utf8'));
    pluginJson.version = version;
    fs.writeFileSync(pluginJsonPath, `${JSON.stringify(pluginJson, null, 2)}\n`);
  }
  return root;
}

// A minimal plugin root with only .claude-plugin/plugin.json.
function makePluginRoot(dir, manifest = {}) {
  const root = path.join(dir, 'plugin');
  fs.mkdirSync(path.join(root, '.claude-plugin'), { recursive: true });
  fs.writeFileSync(
    path.join(root, '.claude-plugin', 'plugin.json'),
    JSON.stringify({ name: 'graph-ops', version: '1.2.3', repository: 'https://github.com/example-owner/example-repo', ...manifest })
  );
  return root;
}

function writeFile(p, content, mode) {
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
  if (mode !== undefined) fs.chmodSync(p, mode);
  return p;
}

function sha256(content) {
  return crypto.createHash('sha256').update(content).digest('hex');
}

// A fake graph-engine: prints each argument as "[arg]" on stdout, a marker on
// stderr, and exits with $FAKE_EXIT (default 0).
function fakeEngineScript(label) {
  return `#!/bin/sh\necho "fake-engine:${label}" >&2\nfor a in "$@"; do printf '[%s]\\n' "$a"; done\nexit "\${FAKE_EXIT:-0}"\n`;
}

module.exports = { pluginDir, makeTmp, copyPlugin, makePluginRoot, writeFile, sha256, fakeEngineScript };

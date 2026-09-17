'use strict';
// Guards the production marketplace's plugin source
// (/.claude-plugin/marketplace.json at the repo root).
//
// The graph-ops entry is a `git-subdir` source: Claude Code sparse-clones
// packages/plugin from this repository at `ref` and copies it into its plugin
// cache. The ref has to be the release tag of the plugin's own version, and
// the URL has to be the plugin's own repository: install-binary.js downloads
// the graph-engine binary from `<repository>/releases/download/v<version>`,
// so any drift would install skills that don't match the binary they fetch.
// None of that is visible until someone actually installs, so it is checked
// here instead (run by `npm test`, and so by CI).

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const pluginDir = path.resolve(__dirname, '..');
const repoRoot = path.resolve(pluginDir, '..', '..');
const marketplacePath = path.join(repoRoot, '.claude-plugin', 'marketplace.json');
const pluginJsonPath = path.join(pluginDir, '.claude-plugin', 'plugin.json');

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function graphOpsSource() {
  const marketplace = readJson(marketplacePath);
  const entry = (marketplace.plugins || []).find((p) => p.name === 'graph-ops');
  assert.ok(entry, `${marketplacePath} has no plugin entry named "graph-ops"`);
  return entry.source;
}

test('the graph-ops entry is a git-subdir source with no command-source fields', () => {
  const source = graphOpsSource();
  assert.strictEqual(source.source, 'git-subdir', 'marketplace.json source.source must be "git-subdir"');
  for (const field of ['command', 'mode', 'timeout', 'sha']) {
    assert.ok(!(field in source), `marketplace.json source must not set "${field}"`);
  }
});

test('the source URL is the plugin repository over HTTPS', () => {
  const { repository } = readJson(pluginJsonPath);
  assert.strictEqual(graphOpsSource().url, `${repository}.git`);
  assert.match(graphOpsSource().url, /^https:\/\/github\.com\//);
});

test('the source path points at this plugin directory', () => {
  const source = graphOpsSource();
  assert.strictEqual(source.path, 'packages/plugin');
  assert.strictEqual(path.resolve(repoRoot, source.path), pluginDir);
  assert.ok(fs.existsSync(path.join(repoRoot, source.path, '.claude-plugin', 'plugin.json')));
});

test('the source ref is the release tag of plugin.json version', () => {
  const { version } = readJson(pluginJsonPath);
  assert.strictEqual(graphOpsSource().ref, `v${version}`);
});

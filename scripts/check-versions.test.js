'use strict';
// Unit tests for scripts/check-versions.js. Runs against a throwaway fixture
// tree (not the real repo) so both the matching and mismatching cases can be
// exercised without touching actual project files.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const { checkVersions } = require('./check-versions.js');

function marketplaceSource(ref) {
  return { source: 'git-subdir', url: 'https://github.com/example/graph-ops.git', path: 'packages/plugin', ref };
}

// Builds a fixture tree with the 5 real locations, using `versions` (an
// array of 5 strings, in the same order as check-versions.js's JSON
// locations followed by the marketplace entry) as each location's value.
function makeFixture(versions) {
  const [rootVersion, webVersion, pluginPkgVersion, pluginJsonVersion, marketplaceVersion] = versions;
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'check-versions-test-'));

  fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify({ name: 'graph-ops', version: rootVersion }));

  fs.mkdirSync(path.join(root, 'packages', 'web'), { recursive: true });
  fs.writeFileSync(
    path.join(root, 'packages', 'web', 'package.json'),
    JSON.stringify({ name: '@graph-ops/web', version: webVersion })
  );

  fs.mkdirSync(path.join(root, 'packages', 'plugin', '.claude-plugin'), { recursive: true });
  fs.writeFileSync(
    path.join(root, 'packages', 'plugin', 'package.json'),
    JSON.stringify({ name: '@graph-ops/plugin', version: pluginPkgVersion })
  );
  fs.writeFileSync(
    path.join(root, 'packages', 'plugin', '.claude-plugin', 'plugin.json'),
    JSON.stringify({ name: 'graph-ops', version: pluginJsonVersion })
  );

  fs.mkdirSync(path.join(root, '.claude-plugin'), { recursive: true });
  fs.writeFileSync(
    path.join(root, '.claude-plugin', 'marketplace.json'),
    JSON.stringify({
      plugins: [
        {
          name: 'graph-ops',
          source: marketplaceSource(`v${marketplaceVersion}`),
        },
      ],
    })
  );

  return root;
}

test('checkVersions passes when all 5 locations agree', (t) => {
  const root = makeFixture(['1.2.3', '1.2.3', '1.2.3', '1.2.3', '1.2.3']);
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  const locations = checkVersions(root);
  assert.strictEqual(locations.length, 5);
  assert.ok(locations.every((l) => l.version === '1.2.3'));
});

test('checkVersions throws, naming every location, when one location disagrees', (t) => {
  const root = makeFixture(['1.2.3', '1.2.3', '1.2.3', '1.9.9', '1.2.3']);
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  assert.throws(() => checkVersions(root), (err) => {
    assert.match(err.message, /Version mismatch across 5 locations/);
    assert.match(err.message, /plugin\.json: 1\.9\.9/);
    assert.match(err.message, /package\.json: 1\.2\.3/);
    return true;
  });
});

test('checkVersions throws when the marketplace.json source ref has no extractable version', (t) => {
  const root = makeFixture(['1.2.3', '1.2.3', '1.2.3', '1.2.3', '1.2.3']);
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  // Overwrite the marketplace ref with something that is not a v<version> tag.
  fs.writeFileSync(
    path.join(root, '.claude-plugin', 'marketplace.json'),
    JSON.stringify({
      plugins: [
        {
          name: 'graph-ops',
          source: marketplaceSource('main'),
        },
      ],
    })
  );

  // A failed extraction must be a hard failure, never a silent "it matches".
  assert.throws(() => checkVersions(root), /Could not extract a version/);
});

test('checkVersions throws when a JSON location is missing its version field', (t) => {
  const root = makeFixture(['1.2.3', '1.2.3', '1.2.3', '1.2.3', '1.2.3']);
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  fs.writeFileSync(path.join(root, 'packages', 'web', 'package.json'), JSON.stringify({ name: '@graph-ops/web' }));

  assert.throws(() => checkVersions(root), /packages\/web\/package\.json has no non-empty string "version" field/);
});

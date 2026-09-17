'use strict';
// Verifies that the project's version is consistent across every place it is
// hardcoded in duplicate:
//   - package.json
//   - packages/web/package.json
//   - packages/plugin/package.json
//   - packages/plugin/.claude-plugin/plugin.json
//   - .claude-plugin/marketplace.json (the graph-ops entry's `git-subdir`
//     source `ref`, the release tag `vX.Y.Z` -- see
//     packages/plugin/test/marketplace-source.test.js)
//
// Run directly (`npm run check:versions`) or required as a module by tests.
// Exits non-zero -- with a message naming every location and its value -- on
// any mismatch. A failure to extract the marketplace.json version is treated
// as a hard failure too, never as an implicit "they match".

const fs = require('node:fs');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..');

// Locations whose version lives at the JSON top-level `version` field.
const JSON_LOCATIONS = [
  'package.json',
  'packages/web/package.json',
  'packages/plugin/package.json',
  'packages/plugin/.claude-plugin/plugin.json',
];

// Location whose version is the release tag in the marketplace source ref.
const MARKETPLACE_LOCATION = '.claude-plugin/marketplace.json';

function readJsonVersion(root, relPath) {
  const abs = path.join(root, relPath);
  let parsed;
  try {
    parsed = JSON.parse(fs.readFileSync(abs, 'utf8'));
  } catch (err) {
    throw new Error(`Failed to read/parse ${relPath}: ${err.message}`);
  }
  if (typeof parsed.version !== 'string' || parsed.version === '') {
    throw new Error(`${relPath} has no non-empty string "version" field`);
  }
  return parsed.version;
}

function readMarketplaceVersion(root, relPath = MARKETPLACE_LOCATION) {
  const abs = path.join(root, relPath);
  let marketplace;
  try {
    marketplace = JSON.parse(fs.readFileSync(abs, 'utf8'));
  } catch (err) {
    throw new Error(`Failed to read/parse ${relPath}: ${err.message}`);
  }
  const entry = (marketplace.plugins || []).find((p) => p && p.name === 'graph-ops');
  if (!entry || typeof entry.source?.ref !== 'string') {
    throw new Error(`${relPath} has no "graph-ops" plugin entry with a string source.ref`);
  }
  const match = entry.source.ref.match(/^v(.+)$/);
  if (!match) {
    // Extraction failure must be a hard failure, not a silent pass: treating
    // an unexpected ref shape as "versions agree" would be a false negative
    // that lets a real mismatch through undetected.
    throw new Error(
      `Could not extract a version from ${relPath}'s source.ref ` +
        '(expected a v<version> release tag); treating this as a failure ' +
        'rather than skipping the check'
    );
  }
  return match[1];
}

// Reads every tracked location's version and throws (with a message naming
// every location and value) if they don't all agree. Returns the list of
// { location, version } pairs on success.
function checkVersions(root = repoRoot) {
  const locations = [
    ...JSON_LOCATIONS.map((relPath) => ({ location: relPath, version: readJsonVersion(root, relPath) })),
    { location: MARKETPLACE_LOCATION, version: readMarketplaceVersion(root) },
  ];

  const versions = new Set(locations.map((l) => l.version));
  if (versions.size > 1) {
    const detail = locations.map((l) => `  - ${l.location}: ${l.version}`).join('\n');
    throw new Error(`Version mismatch across ${locations.length} locations:\n${detail}`);
  }

  return locations;
}

function main() {
  try {
    const locations = checkVersions();
    console.log(`OK: all ${locations.length} locations agree on version ${locations[0].version}.`);
    for (const { location, version } of locations) {
      console.log(`  - ${location}: ${version}`);
    }
  } catch (err) {
    console.error(`Version consistency check failed: ${err.message}`);
    process.exitCode = 1;
  }
}

if (require.main === module) {
  main();
}

module.exports = {
  checkVersions,
  readJsonVersion,
  readMarketplaceVersion,
  JSON_LOCATIONS,
  MARKETPLACE_LOCATION,
  repoRoot,
};

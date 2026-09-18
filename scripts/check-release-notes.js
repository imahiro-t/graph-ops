'use strict';
// Verifies that the release notes for the current version exist:
// docs/release-notes/v<version>.md, where <version> is package.json's
// "version" (kept in sync with the other 4 locations by check-versions.js).
//
// The release workflow publishes that file as the GitHub Release body, so a
// version bump without its notes would fail the release. Running this check
// in CI catches the omission before a tag is ever cut.
//
// Run directly (`npm run check:release-notes`) or required as a module by
// tests. Exits non-zero, naming the expected path, when the file is missing
// or contains only whitespace.

const fs = require('node:fs');
const path = require('node:path');

const { readJsonVersion, repoRoot } = require('./check-versions.js');

const RELEASE_NOTES_DIR = 'docs/release-notes';

function releaseNotesPath(version) {
  return `${RELEASE_NOTES_DIR}/v${version}.md`;
}

// Returns { version, location } on success; throws otherwise.
function checkReleaseNotes(root = repoRoot) {
  const version = readJsonVersion(root, 'package.json');
  const location = releaseNotesPath(version);
  let text;
  try {
    text = fs.readFileSync(path.join(root, location), 'utf8');
  } catch (err) {
    throw new Error(`${location} is missing (${err.code || err.message}); write the release notes for v${version} there`);
  }
  if (text.trim() === '') {
    throw new Error(`${location} is empty; write the release notes for v${version} there`);
  }
  return { version, location };
}

function main() {
  try {
    const { version, location } = checkReleaseNotes();
    console.log(`OK: release notes for v${version} found at ${location}.`);
  } catch (err) {
    console.error(`Release notes check failed: ${err.message}`);
    process.exitCode = 1;
  }
}

if (require.main === module) {
  main();
}

module.exports = { checkReleaseNotes, releaseNotesPath, RELEASE_NOTES_DIR };

'use strict';
// Unit tests for scripts/check-release-notes.js, run against throwaway
// fixture trees rather than the real repo.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const { checkReleaseNotes } = require('./check-release-notes.js');

function makeFixture(version, notes) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'check-release-notes-test-'));
  fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify({ name: 'graph-ops', version }));
  if (notes !== undefined) {
    fs.mkdirSync(path.join(root, 'docs', 'release-notes'), { recursive: true });
    fs.writeFileSync(path.join(root, 'docs', 'release-notes', `v${version}.md`), notes);
  }
  return root;
}

test('passes when the current version has non-empty release notes', () => {
  const root = makeFixture('1.2.3', '## Highlights\n\n- Something\n');
  assert.deepStrictEqual(checkReleaseNotes(root), {
    version: '1.2.3',
    location: 'docs/release-notes/v1.2.3.md',
  });
});

test('fails, naming the expected path, when the notes file is missing', () => {
  const root = makeFixture('1.2.3');
  assert.throws(() => checkReleaseNotes(root), /docs\/release-notes\/v1\.2\.3\.md is missing/);
});

test('fails when the notes file contains only whitespace', () => {
  const root = makeFixture('1.2.3', ' \n\t\n');
  assert.throws(() => checkReleaseNotes(root), /docs\/release-notes\/v1\.2\.3\.md is empty/);
});

test('notes for a different version do not count', () => {
  const root = makeFixture('1.2.3', '## Notes\n');
  fs.renameSync(
    path.join(root, 'docs', 'release-notes', 'v1.2.3.md'),
    path.join(root, 'docs', 'release-notes', 'v1.2.2.md')
  );
  assert.throws(() => checkReleaseNotes(root), /v1\.2\.3\.md is missing/);
});

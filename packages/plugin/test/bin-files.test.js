'use strict';
// bin/ is added to the Bash tool's PATH by Claude Code and is all a
// git-subdir install (e.g. the community catalog) gets, so git must track
// exactly the two shims there -- the sh one executable -- and never a binary.

const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const pluginDir = path.resolve(__dirname, '..');

test('git tracks only the graph-engine shims under packages/plugin/bin, with the sh shim executable', (t) => {
  const res = spawnSync('git', ['ls-files', '-s', '--', 'bin'], { cwd: pluginDir, encoding: 'utf8' });
  if (res.error || res.status !== 0) {
    t.skip('not running inside a git checkout');
    return;
  }
  const entries = res.stdout
    .trim()
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      const [meta, file] = line.split('\t');
      return { mode: meta.split(' ')[0], file };
    })
    .sort((a, b) => a.file.localeCompare(b.file));

  assert.deepStrictEqual(entries, [
    { mode: '100755', file: 'bin/graph-engine' },
    { mode: '100644', file: 'bin/graph-engine.cmd' },
  ]);
});

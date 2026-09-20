'use strict';
// bin/ is added to the Bash tool's PATH by Claude Code and is all a
// git-subdir install (e.g. the community catalog) gets, so git must track
// exactly the two shims there -- the sh one executable -- and never a binary.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
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

// bin/graph-engine.cmd's argument handling is the subject of finding CHK-05:
// cmd.exe re-parses whatever the all-arguments token expands to, which turns
// a %VAR%, an ampersand or a quote inside a graph-engine argument into an
// expansion, a second command or a re-split -- and graph-engine's arguments
// are arbitrary prose (add-artifact carries a whole document). The shim
// therefore copies each already-split argument into its own environment
// variable, which scripts/cmd-args.js reassembles. These assertions pin the
// shape that relies on, since there is no cmd.exe here to test the behaviour
// itself.
test('bin/graph-engine.cmd relays arguments through the environment instead of the all-arguments token', () => {
  const body = fs.readFileSync(path.join(pluginDir, 'bin', 'graph-engine.cmd'), 'utf8');

  assert.ok(!body.includes('%*'), 'the all-arguments token must not appear anywhere in the shim');
  assert.match(body, /GRAPH_OPS_ARGC/, 'the shim must record the argument count');
  assert.match(body, /GRAPH_OPS_ARG_/, 'the shim must record each argument in its own variable');

  // Delayed expansion must stay OFF. With it on, the result of the argument
  // expansion is scanned a second time, so an argument containing ! or ^
  // loses those characters -- the same silent corruption of user text that
  // dropping the all-arguments token is meant to remove, just on different
  // characters.
  assert.ok(
    !/setlocal\s+enabledelayedexpansion/i.test(body),
    'the shim must not enable delayed expansion (it would eat ! and ^ in arguments)'
  );
  // The pieces the copy loop needs: the quote-stripping form of the
  // argument, and shift, so there is no ten-argument limit.
  assert.match(body, /%~1/, 'arguments must be copied with the quote-stripping form');
  assert.match(body, /^\s*shift\s*\r?$/m, 'the shim must shift through every argument');
  // Non-regression: the exit code still comes back from graph-engine.
  assert.match(body, /exit \/b %ERRORLEVEL%/, "the shim must propagate graph-engine's exit code");
  // cmd.exe needs CRLF; a lone LF makes labels and goto unreliable.
  assert.ok(!/[^\r]\n/.test(body), 'the shim must use CRLF line endings');
});

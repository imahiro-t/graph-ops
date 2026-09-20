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
function readCmdShim() {
  return fs.readFileSync(path.join(pluginDir, 'bin', 'graph-engine.cmd'), 'utf8');
}

// Every line that actually runs `shift`, in order. Anchored at the start of a
// line so the word inside a `rem` comment is not one of them.
const SHIFT_LINE = /^[ \t]*shift\b.*$/gm;

test('bin/graph-engine.cmd relays arguments through the environment instead of the all-arguments token', () => {
  const body = readCmdShim();

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
  // Non-regression: the exit code still comes back from graph-engine.
  assert.match(body, /exit \/b %ERRORLEVEL%/, "the shim must propagate graph-engine's exit code");
  // cmd.exe needs CRLF; a lone LF makes labels and goto unreliable.
  assert.ok(!/[^\r]\n/.test(body), 'the shim must use CRLF line endings');
});

// The assertions above are presence checks, and presence is not what makes
// the shim safe -- the shape of each individual line is. Both defects the
// first attempt at CHK-05 shipped (an argument read without its quotes
// stripped, and the shim's own path read after `shift` had renumbered the
// parameters) satisfied every presence check while breaking the shim, so
// these pin the forms themselves. There is no cmd.exe on the machines that
// run this suite: this file is the whole of the verification.
test('bin/graph-engine.cmd never exposes an argument to a second round of cmd.exe parsing', () => {
  const body = readCmdShim();

  // Rule 1: no bare numbered parameter, anywhere. Reading one pastes the
  // caller's own quotes into the line, which closes the quoting early and
  // puts the argument's text -- an ampersand included -- back where cmd.exe
  // splits on it. `%~1` (quote-stripping) and `%~dp0` are the only forms
  // allowed, and neither is a percent sign followed by a digit.
  const bareParam = body.match(/%\d/g);
  assert.deepStrictEqual(
    bareParam,
    null,
    `a batch parameter must never be read bare (found ${JSON.stringify(bareParam)}); use the ~ form`
  );

  // Rule 1, continued: the end-of-arguments test in particular. Pinned to
  // the exact two lines, because this is where a bare parameter is easiest
  // to reintroduce and hardest to notice -- `if "%1"==""` reads plausibly
  // and is an injection.
  assert.ok(
    body.includes('set "GRAPH_OPS_ARG_PEEK=%~1"\r\nif not defined GRAPH_OPS_ARG_PEEK goto graph_ops_end_of_args\r\n'),
    'the end-of-arguments test must go through a quoted assignment plus `if not defined`'
  );
  // ...and the copy itself.
  assert.ok(
    body.includes('set "GRAPH_OPS_ARG_%GRAPH_OPS_ARGC%=%~1"\r\n'),
    'each argument must be copied by a fully quoted assignment of its ~ form'
  );

  // Rule 3: the shim's own path is resolved before anything shifts. `shift`
  // renumbers the batch parameters including parameter zero, so a path built
  // from it after the loop resolves against nothing and every invocation
  // that carries arguments fails to find engine-shim.js.
  const shimPathLine = 'set "GRAPH_OPS_SHIM=%~dp0..\\scripts\\engine-shim.js"\r\n';
  assert.ok(body.includes(shimPathLine), 'the shim must save its own directory in a variable');
  assert.match(body, /^[ \t]*node "%GRAPH_OPS_SHIM%"\r?$/m, 'the shim must launch node from that variable');
  const firstShift = body.search(SHIFT_LINE);
  assert.ok(firstShift !== -1, 'the shim must shift through every argument (no ten-argument limit)');
  assert.ok(
    body.indexOf(shimPathLine) < firstShift,
    'the shim must resolve its own directory before the first shift'
  );
  const dirTokens = [...body.matchAll(/%~dp0/g)];
  assert.ok(dirTokens.length > 0, 'the shim must resolve its own directory from parameter zero');
  for (const token of dirTokens) {
    assert.ok(token.index < firstShift, `%~dp0 at ${token.index} is read after shift has renumbered the parameters`);
  }
  // Belt and braces on the same failure: /1 shifts from parameter one, so
  // parameter zero survives the loop as well.
  for (const line of body.match(SHIFT_LINE) || []) {
    assert.strictEqual(line.trim(), 'shift /1', 'every shift must be `shift /1`, which leaves parameter zero alone');
  }

  // cmd.exe splits a line on these before `rem` gets to run, so one inside a
  // comment would execute. The single legitimate ampersand is the last
  // line's `endlocal & exit /b`.
  const lines = body.split('\r\n');
  for (const line of lines) {
    if (/^\s*endlocal\b/.test(line)) continue;
    assert.ok(!/[&|<>]/.test(line), `a command separator outside quotes would run as a command: ${line}`);
  }
});

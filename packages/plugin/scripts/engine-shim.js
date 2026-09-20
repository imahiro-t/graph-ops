#!/usr/bin/env node
// Node counterpart of the bin/graph-engine sh shim, used by
// bin/graph-engine.cmd (cmd/PowerShell on Windows) and by resolve-binary.js
// when no binary is present yet on win32.
//
// Resolves the graph-engine binary via ensureEngine (libexec -> monorepo
// build -> per-user cache -> checksum-verified download; see
// scripts/install-binary.js), then runs it with this script's arguments and
// the inherited stdio, exiting with its exit code (128 + signal number when
// it was killed by a signal). Everything this script itself prints goes to
// stderr; stdout is graph-engine's own.
//
// "This script's arguments" are process.argv's, except when the .cmd shim
// relayed them through the environment instead of through cmd.exe's
// all-arguments token -- see scripts/cmd-args.js for why it does that and
// what resolveArgv picks.
'use strict';

const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');
const { ensureEngine } = require('./install-binary');
const { resolveArgv } = require('./cmd-args');

async function main(argv) {
  const pluginRoot = path.resolve(__dirname, '..');
  let bin;
  try {
    bin = await ensureEngine({ pluginRoot });
  } catch (err) {
    process.stderr.write(`${err.message}\n`);
    return 1;
  }

  // env is passed explicitly rather than left to default to process.env, to
  // make it visible that what the child inherits is the environment
  // resolveArgv has already stripped the relay variables out of.
  const res = spawnSync(bin, argv, { stdio: 'inherit', env: process.env });
  if (res.error) {
    process.stderr.write(`graph-ops: failed to run ${bin}: ${res.error.message}\n`);
    return res.error.code === 'ENOENT' ? 127 : 126;
  }
  if (res.status !== null) {
    return res.status;
  }
  if (res.signal) {
    return 128 + (os.constants.signals[res.signal] || 0);
  }
  return 1;
}

main(resolveArgv()).then((code) => {
  process.exitCode = code;
});

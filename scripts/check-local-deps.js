'use strict';
// Fails fast when packages/web's tools are not installed inside this checkout
// (DFLT-00267).
//
// Run as the web package's prelint / pretest / prebuild. A fresh git worktree
// (.claude/worktrees/<id>) has no node_modules until `npm ci` is run in it,
// and npm puts every ancestor's node_modules/.bin on PATH -- so `npm run lint`
// there quietly started the PARENT repository's eslint, which then failed
// with ERR_MODULE_NOT_FOUND for @eslint/js (and vitest hung). This check
// looks for each tool the way Node would, from packages/web upwards, and
// stops with "run npm ci in <root>" when it is missing or only found outside
// the checkout.
//
// Before that it checks the Node.js version (check-node-version.js,
// DFLT-00273): on a Node older than the one declared for development the
// tools fail in confusing ways even when they are installed, so the Node
// message comes first and the dependency check is not run at all.
//
// Node built-ins only: it has to run precisely when nothing is installed.
// Run directly or required as a module by tests.

const fs = require('node:fs');
const path = require('node:path');
const { checkNodeVersion } = require('./check-node-version.js');

const repoRoot = path.resolve(__dirname, '..');

// devDependencies of packages/web that its lint / test / build start.
const REQUIRED_PACKAGES = ['eslint', '@eslint/js', 'vitest', 'vite', 'typescript'];

// Where Node's resolution from `fromDir` would find `pkg`: the first
// ancestor node_modules/<pkg> that has a package.json, or null. Checked by
// file existence because some packages' `exports` forbid
// require.resolve('<pkg>/package.json').
function findPackageDir(pkg, fromDir) {
  let dir = path.resolve(fromDir);
  for (;;) {
    const candidate = path.join(dir, 'node_modules', pkg);
    if (fs.existsSync(path.join(candidate, 'package.json'))) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) return null;
    dir = parent;
  }
}

// Both sides are realpath'd first so a symlinked path (macOS /tmp ->
// /private/tmp, a linked checkout) cannot make an inside directory look
// outside, or the reverse.
function isInside(root, target) {
  const rel = path.relative(fs.realpathSync(root), fs.realpathSync(target));
  return rel === '' || (!rel.startsWith('..') && !path.isAbsolute(rel));
}

// Quotes `value` for a POSIX shell so the command printed in the failure
// message can be copied and run as is, even when the path contains spaces or
// shell metacharacters. An embedded ' becomes '\''.
function shellQuote(value) {
  return `'${String(value).replace(/'/g, "'\\''")}'`;
}

// Returns { ok: true } or { ok: false, message }.
function checkLocalDeps(root = repoRoot, packages = REQUIRED_PACKAGES) {
  const webDir = path.join(root, 'packages', 'web');
  const missing = [];
  const outside = [];
  for (const pkg of packages) {
    const found = findPackageDir(pkg, webDir);
    if (found === null) missing.push(pkg);
    else if (!isInside(root, found)) outside.push({ pkg, found });
  }
  if (missing.length === 0 && outside.length === 0) return { ok: true };

  const lines = [
    'Dependencies are not installed in this checkout:',
    `  ${root}`,
    ''
  ];
  for (const pkg of missing) lines.push(`  - ${pkg}: not found`);
  for (const { pkg, found } of outside) lines.push(`  - ${pkg}: only found outside this checkout, at ${found}`);
  lines.push(
    '',
    'Run `npm ci` in the repository (worktree) root first:',
    `  cd ${shellQuote(root)} && npm ci`,
    '',
    'A new git worktree has no node_modules of its own; without this check the',
    "parent repository's tools would be picked up and fail in confusing ways."
  );
  return { ok: false, message: lines.join('\n') };
}

// The CLI: the Node.js version first, then the dependencies; the first
// failure is written to stderr and exits 1. The checks and process hooks are
// injectable so the tests can drive the order without another Node binary.
function main({
  checkNode = checkNodeVersion,
  checkDeps = checkLocalDeps,
  stderr = process.stderr,
  exit = process.exit
} = {}) {
  for (const check of [checkNode, checkDeps]) {
    const result = check();
    if (!result.ok) {
      stderr.write(`${result.message}\n`);
      exit(1);
      return;
    }
  }
}

if (require.main === module) main();

module.exports = { checkLocalDeps, findPackageDir, isInside, main, shellQuote, REQUIRED_PACKAGES };

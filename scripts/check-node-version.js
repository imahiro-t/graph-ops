'use strict';
// Fails fast when the running Node.js is older than the version declared for
// developing GraphOps (DFLT-00273).
//
// The requirement is declared once, as `engines.node` in
// packages/web/package.json (the root package.json and .nvmrc repeat it, and
// check-node-version.test.js keeps them and CI's node-version in line). On an
// older Node -- e.g. 20.10 -- vitest 4 and eslint 10 do not work properly:
// tests using fake timers time out one by one instead of reporting anything
// about the Node version. check-local-deps.js calls this check first, so
// packages/web's prelint / pretest / prebuild / predev / pretest:watch stop
// here with the required and the current version side by side.
//
// Only the development requirement lives here. What end users need to RUN the
// plugin (node on PATH, see README "Requirements") is unrelated and unchanged.
//
// Node built-ins only, and syntax old Node versions still parse: this has to
// run on exactly the Node versions it rejects. Run directly or required as a
// module by check-local-deps.js and the tests.

const fs = require('node:fs');
const path = require('node:path');

const repoRoot = path.resolve(__dirname, '..');

// The package.json whose engines.node is the source of truth.
function webPackageJsonPath(root) {
  return path.join(root, 'packages', 'web', 'package.json');
}

// Returns packages/web/package.json's engines.node, or throws naming the file.
function readRequiredRange(root = repoRoot) {
  const file = webPackageJsonPath(root);
  const pkg = JSON.parse(fs.readFileSync(file, 'utf8'));
  const range = pkg.engines && pkg.engines.node;
  if (typeof range !== 'string' || range.trim() === '') {
    throw new Error(`${file} has no engines.node to check the Node.js version against`);
  }
  return range;
}

function toVersion(match) {
  return {
    major: Number(match[1]),
    minor: match[2] === undefined ? 0 : Number(match[2]),
    patch: match[3] === undefined ? 0 : Number(match[3])
  };
}

// Only ">=MAJOR[.MINOR[.PATCH]]" is understood -- no general semver ranges,
// since this runs without any installed package. The declaration is pinned to
// this form by the tests; anything else returns null.
function parseMinimum(range) {
  const match = /^\s*>=\s*(\d+)(?:\.(\d+))?(?:\.(\d+))?\s*$/.exec(String(range));
  return match ? toVersion(match) : null;
}

// Parses a process.version-style string ("v20.10.0"; the leading "v" is
// optional, a prerelease/build suffix is ignored), or returns null.
function parseVersion(version) {
  const match = /^\s*v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?\s*$/.exec(String(version));
  return match ? toVersion(match) : null;
}

function compareVersions(a, b) {
  return a.major - b.major || a.minor - b.minor || a.patch - b.patch;
}

// How to get a Node.js whose major version is at least `major`. asdf comes first with
// ASDF_NODEJS_VERSION, because reading .nvmrc needs legacy_version_file in
// ~/.asdfrc, and how a bare "24" there resolves depends on asdf-nodejs'
// settings (it may pick a version that is not installed).
function switchHints(major) {
  return [
    'Switch to a newer Node.js for this repository, for example:',
    `  - asdf: prefix the command with ASDF_NODEJS_VERSION=<an installed ${major}.x>`,
    `          (e.g. ASDF_NODEJS_VERSION=${major}.x.y npm test; list them with \`asdf list nodejs\`)`,
    '  - nvm / fnm: `nvm use` / `fnm use` in the repository root (reads .nvmrc)'
  ];
}

// Returns { ok: true } or { ok: false, message }.
//
// A `current` that cannot be parsed fails too (showing it as is) rather than
// being waved through: this only runs under Node, whose process.version always
// parses, so an unparseable value means something is wrong with the check's
// input and silently passing would hide it.
function checkNodeVersion(options) {
  const opts = options || {};
  const current = opts.current === undefined ? process.version : opts.current;
  const range = opts.range === undefined ? readRequiredRange(opts.root) : opts.range;
  const execPath = opts.execPath === undefined ? process.execPath : opts.execPath;

  const minimum = parseMinimum(range);
  if (minimum === null) {
    return {
      ok: false,
      message: [
        `This check cannot interpret engines.node "${range}" in packages/web/package.json.`,
        'Write it as ">=MAJOR[.MINOR[.PATCH]]" (e.g. ">=24") so scripts/check-node-version.js can check it,',
        'and keep the root package.json, .nvmrc and CI\'s node-version in line with it.'
      ].join('\n')
    };
  }

  const version = parseVersion(current);
  if (version !== null && compareVersions(version, minimum) >= 0) return { ok: true };

  const lines = [
    `Node.js ${range} is required to develop GraphOps, but this is Node.js ${current}:`,
    `  ${execPath}`,
    ''
  ];
  if (version === null) {
    lines.push(`(The version "${current}" could not be parsed.)`, '');
  }
  lines.push(
    ...switchHints(minimum.major),
    '',
    'The requirement is declared as engines.node in package.json and in .nvmrc; CI uses the same',
    `Node.js ${minimum.major}. On an older Node.js, vitest 4 and eslint 10 fail in confusing ways (e.g. tests`,
    'using fake timers time out), so lint / test / build / dev / test:watch stop here instead.',
    'This is only the development requirement; running the GraphOps plugin itself is unaffected.'
  );
  return { ok: false, message: lines.join('\n') };
}

if (require.main === module) {
  const result = checkNodeVersion();
  if (!result.ok) {
    process.stderr.write(result.message + '\n');
    process.exit(1);
  }
}

module.exports = { checkNodeVersion, parseMinimum, parseVersion, readRequiredRange };

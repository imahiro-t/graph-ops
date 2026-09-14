'use strict';
// Guards the production marketplace's `command` source
// (/.claude-plugin/marketplace.json at the repo root).
//
// The command is a single `node -e "<JS>"` line that Claude Code hands to
// `sh` on macOS/Linux and to `cmd.exe` on Windows, so it has to satisfy
// Claude Code's own limits (<= 500 printable ASCII characters, no run of 4+
// spaces) AND avoid every character either shell would interpret inside the
// double quotes. Its tag and clone URL also have to stay in lockstep with
// the plugin's version and repository, or users would clone a tag whose
// Release doesn't exist. None of that is visible until someone actually
// installs, so it is checked here instead (run by `npm test`, and so by CI).

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { spawnSync } = require('node:child_process');

const pluginDir = path.resolve(__dirname, '..');
const repoRoot = path.resolve(pluginDir, '..', '..');
const marketplacePath = path.join(repoRoot, '.claude-plugin', 'marketplace.json');
const pluginJsonPath = path.join(pluginDir, '.claude-plugin', 'plugin.json');
const packageJsonPath = path.join(pluginDir, 'package.json');

const MAX_COMMAND_LENGTH = 500;
// `"` `$` `` ` `` `\` are special inside sh double quotes; `%` `^` `&` `|` `<` `>` are special to cmd.exe.
const FORBIDDEN_CHARS = ['"', '$', '`', '\\', '%', '^', '&', '|', '<', '>'];

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function graphOpsEntry() {
  const marketplace = readJson(marketplacePath);
  const entry = (marketplace.plugins || []).find((p) => p.name === 'graph-ops');
  assert.ok(entry, `${marketplacePath} has no plugin entry named "graph-ops"`);
  return entry;
}

function command() {
  const cmd = graphOpsEntry().source.command;
  assert.strictEqual(typeof cmd, 'string', 'marketplace.json source.command must be a string');
  return cmd;
}

function jsBody() {
  const match = command().match(/^node -e "([^"]*)"$/);
  assert.ok(match, 'marketplace.json command must have the form: node -e "<JS>" (with no other double quotes)');
  return match[1];
}

test('the graph-ops entry is a command source in copy mode with a timeout', () => {
  const { source } = graphOpsEntry();
  assert.strictEqual(source.source, 'command', 'marketplace.json source.source must be "command"');
  assert.strictEqual(source.mode, 'copy', 'marketplace.json source.mode must be "copy" (mode is not copy)');
  assert.ok(
    Number.isInteger(source.timeout) && source.timeout >= 60 && source.timeout <= 600,
    `marketplace.json source.timeout must be an integer between 60 and 600 seconds, got ${JSON.stringify(source.timeout)}`
  );
});

test('the command does not go through npm', () => {
  const cmd = command();
  for (const word of ['npx', 'npm', '@latest', 'graph-ops-claude-plugin-path']) {
    assert.ok(!cmd.includes(word), `marketplace.json command must not use npm, but contains ${JSON.stringify(word)}`);
  }
});

test('the command fits within Claude Code\'s 500-character limit', () => {
  const { length } = command();
  assert.ok(
    length <= MAX_COMMAND_LENGTH,
    `marketplace.json command is ${length} characters, over the ${MAX_COMMAND_LENGTH}-character limit`
  );
});

test('the command is printable ASCII only', () => {
  const cmd = command();
  const bad = [...cmd].filter((ch) => !/^[\x20-\x7e]$/.test(ch));
  assert.ok(
    /^[\x20-\x7e]+$/.test(cmd),
    `marketplace.json command must be printable ASCII only, found ${JSON.stringify(bad)}`
  );
});

test('the command has no run of 4 or more spaces', () => {
  assert.ok(!/ {4,}/.test(command()), 'marketplace.json command contains a run of 4 or more spaces');
});

test('the command is node -e "<JS>" and the JS uses no shell-special characters', () => {
  const body = jsBody();
  const found = FORBIDDEN_CHARS.filter((ch) => body.includes(ch));
  assert.deepStrictEqual(
    found,
    [],
    `marketplace.json command JS contains forbidden characters (special to sh or cmd.exe): ${JSON.stringify(found)}`
  );
});

test('the command JS is syntactically valid', () => {
  assert.doesNotThrow(() => new vm.Script(jsBody()), 'marketplace.json command JS has a syntax error');
});

test('the command clones the tag matching plugin.json / package.json versions', () => {
  const body = jsBody();
  const tag = body.match(/\bv='v([^']+)'/);
  assert.ok(tag, "marketplace.json command JS must set the tag as v='v<version>'");
  const commandVersion = tag[1];
  const pluginJson = readJson(pluginJsonPath);
  const packageJson = readJson(packageJsonPath);
  assert.strictEqual(
    commandVersion,
    pluginJson.version,
    `tag v${commandVersion} in marketplace.json command does not match plugin.json version ${pluginJson.version}`
  );
  assert.strictEqual(
    commandVersion,
    packageJson.version,
    `tag v${commandVersion} in marketplace.json command does not match packages/plugin/package.json version ${packageJson.version}`
  );
  assert.ok(
    body.includes("'clone','--depth=1','-b',v,"),
    "marketplace.json command must shallow-clone the tag: 'clone','--depth=1','-b',v"
  );
});

test('the command clones plugin.json\'s repository', () => {
  const urls = jsBody().match(/'https?:\/\/[^']*'/g) || [];
  const { repository } = readJson(pluginJsonPath);
  assert.deepStrictEqual(
    urls,
    [`'${repository}'`],
    `clone URL in marketplace.json command ${JSON.stringify(urls)} does not match plugin.json repository ${repository}`
  );
});

test('the command never prompts for credentials and aborts stalled transfers', () => {
  const body = jsBody();
  for (const snippet of [
    'e=process.env;',
    'e.GIT_TERMINAL_PROMPT=0;',
    "e.GIT_ASKPASS='';",
    'e.GIT_HTTP_LOW_SPEED_LIMIT=1;',
    'e.GIT_HTTP_LOW_SPEED_TIME=30;',
    "'-c','credential.helper=','clone'",
    'stdio:[0,2,2]',
  ]) {
    assert.ok(body.includes(snippet), `marketplace.json command JS is missing ${JSON.stringify(snippet)}`);
  }
});

test('requiring the plugin package resolves to scripts/claude-plugin-path.js', () => {
  assert.strictEqual(require.resolve(pluginDir), path.join(pluginDir, 'scripts', 'claude-plugin-path.js'));
});

test('with the cache already in place, the command loads it without running git', (t) => {
  const tmp = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'graph-ops-command-')));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));

  const { version } = readJson(pluginJsonPath);
  const stubRoot = path.join(tmp, '.cache', 'graph-ops', `v${version}`, 'packages', 'plugin');
  fs.mkdirSync(stubRoot, { recursive: true });
  fs.writeFileSync(path.join(stubRoot, 'package.json'), JSON.stringify({ main: 'stub.js' }));
  fs.writeFileSync(path.join(stubRoot, 'stub.js'), "process.stdout.write(__dirname + '\\n');\n");
  const cwd = path.join(tmp, '.claude');
  fs.mkdirSync(cwd);

  // PATH holds only node's own directory, so any attempt to run git fails.
  const result = spawnSync(process.execPath, ['-e', jsBody()], {
    cwd,
    env: { HOME: tmp, USERPROFILE: tmp, PATH: path.dirname(process.execPath) },
    encoding: 'utf8',
  });
  assert.strictEqual(result.status, 0, `command exited ${result.status}; stderr: ${result.stderr}`);
  assert.strictEqual(result.stdout, `${stubRoot}\n`);
});

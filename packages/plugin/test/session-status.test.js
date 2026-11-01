'use strict';
// DFLT-00383: the status band above Claude Code's prompt. The mod itself is
// tested with `claude plugin test packages/plugin` (hooks/register.test.tsx),
// which needs the claude binary; what is pinned here, with node alone, is
// that the plugin ships the mod in the shape Claude Code loads, and that the
// skills make the session-status calls the engine cannot make for them.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const pluginDir = path.resolve(__dirname, '..');
const read = (rel) => fs.readFileSync(path.join(pluginDir, rel), 'utf8');

test('plugin.json names the state contract and hooks.json names one module', () => {
  const manifest = JSON.parse(read('.claude-plugin/plugin.json'));
  assert.strictEqual(manifest.types, './types/index.d.ts');
  const hooks = JSON.parse(read('hooks/hooks.json'));
  assert.deepStrictEqual(Object.keys(hooks), ['modules']);
  assert.deepStrictEqual(hooks.modules, ['./register.tsx']);
  for (const rel of ['hooks/register.tsx', 'hooks/status-band.ts', 'types/index.d.ts', 'hooks/register.test.tsx']) {
    assert.ok(fs.existsSync(path.join(pluginDir, rel)), `${rel} is missing`);
  }
});

test('the mod files are committed, so a git-subdir install ships them', (t) => {
  const res = spawnSync('git', ['ls-files', '--', 'hooks', 'types', 'tsconfig.json'], { cwd: pluginDir, encoding: 'utf8' });
  if (res.status !== 0) {
    t.skip('not a git checkout');
    return;
  }
  const files = res.stdout.split('\n').filter(Boolean).sort();
  for (const rel of ['hooks/hooks.json', 'hooks/register.tsx', 'hooks/status-band.ts', 'types/index.d.ts']) {
    assert.ok(files.includes(rel), `${rel} is not committed`);
  }
});

test('the state contract declares the status under the plugin name', () => {
  const types = read('types/index.d.ts');
  assert.match(types, /declare module 'claude-code'/);
  assert.match(types, /'graph-ops': \{ status: GraphOpsStatus \| null \}/);
});

test('the mod reads only its own session\'s file under the graph-ops home', () => {
  const band = read('hooks/status-band.ts');
  assert.ok(band.includes('/.graph-ops/session-status/${sessionId}.json'));
  assert.ok(band.includes('/^[A-Za-z0-9_-]{1,128}$/'), 'a session ID is checked before it names a file');
  const mod = read('hooks/register.tsx');
  assert.ok(mod.includes('$.session.id()'), 'the session ID is asked for on every look');
  assert.ok(!/\$\.fs\.write|\$\.process|\$\.http/.test(mod), 'the mod only reads');
});

test('the skills make the session-status calls the engine cannot make', () => {
  const pt = read('skills/process-ticket/SKILL.md');
  for (const needle of [
    'graph-engine session-status set --ticket "<ticketId>" --phase awaiting_human --detail',
    'graph-engine session-status set --ticket "<ticketId>" --phase running',
    'run `graph-engine session-status clear` as its last command',
    'whatever the reason',
    'Ending a turn to wait for an approval (step 3) is not the end',
    'that skill\'s `autopilot report` clears the band',
    'ignore it and carry on',
  ]) {
    assert.ok(pt.includes(needle), `process-ticket must mention ${JSON.stringify(needle)}`);
  }
  const rt = read('skills/refine-ticket/SKILL.md');
  for (const needle of [
    'graph-engine session-status set --ticket "<ticketId>" --phase refining',
    'finish with `graph-engine session-status clear`',
    'when you stop early without refining',
  ]) {
    assert.ok(rt.includes(needle), `refine-ticket must mention ${JSON.stringify(needle)}`);
  }
  for (const name of ['autopilot-ticket', 'autopilot-tree']) {
    const body = read(`skills/${name}/SKILL.md`);
    assert.ok(body.includes('Then run `graph-engine session-status clear` as the last command'), `${name} must clear the band`);
    assert.ok(body.includes('keep the status band above this session\'s prompt up to date'), `${name} must say the engine keeps the band`);
  }
});

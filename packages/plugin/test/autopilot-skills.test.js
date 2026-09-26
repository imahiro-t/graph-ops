'use strict';
// DFLT-00142: the autopilot skills are instructions an LLM follows, so their
// behaviour is checked by hand (the @manual scenarios). What can be pinned
// automatically is that each rule the spec fixes is actually written down,
// that the files follow docs/authoring-format.md's skill format, and that the
// existing skills and node types point at the worker's overrides.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const pluginDir = path.resolve(__dirname, '..');
const read = (rel) => fs.readFileSync(path.join(pluginDir, rel), 'utf8');

const SKILLS = ['autopilot-ticket', 'autopilot-tree', 'autopilot-worker'];

function assertAll(body, file, needles) {
  for (const needle of needles) {
    assert.ok(body.includes(needle), `${file} must mention ${JSON.stringify(needle)}`);
  }
}

for (const name of SKILLS) {
  test(`${name}/SKILL.md follows the skill authoring format`, () => {
    const file = `skills/${name}/SKILL.md`;
    const body = read(file);
    const fm = body.match(/^---\nname: (.+)\ndescription: (.+)\n---\n/);
    assert.ok(fm, `${file} must start with name/description frontmatter`);
    assert.strictEqual(fm[1], name);
    assert.strictEqual((body.match(/^# /gm) || []).length, 1, 'exactly one H1');
    assert.match(body, new RegExp(`^# ${name} Skill$`, 'm'));
    assert.match(body, /^## 0\. Check for user\/team customization of this skill$/m);
    assert.ok(body.includes(`graph-engine get-skill-context "${name}"`));
    const lines = body.split('\n').length - (body.endsWith('\n') ? 1 : 0);
    assert.ok(lines <= 150, `${file} has ${lines} lines; the upper bound is 150`);
    assert.ok(!/[^\x00-\x7f]/.test(body), `${file} must be plain ASCII`);
    assert.ok(!/\bshould\b/.test(body), `${file} must not use "should"`);
  });
}

test('autopilot-worker/SKILL.md states the start-up and refine rules', () => {
  assertAll(read('skills/autopilot-worker/SKILL.md'), 'autopilot-worker', [
    'graph-engine autopilot worker-context "<runId>" "<ticketId>"',
    'pending_decisions',
    'graph-engine autopilot attach-decisions "<runId>" "<ticketId>" "<planNodeId>"',
    'Do not clarify anything with the person',
    'Use existing labels only',
    'Never run `create-label`',
    'Before updating the ticket',
    'graph-engine autopilot record-decision "<runId>" "<ticketId>" refine -',
    'right after the first `get-executable`',
    "before launching the plan node's subagent",
    'If `refined_at` is set, do not refine and record no refine decision',
    'right before and right after launching node subagents',
    'at every `wait-node` timeout',
    'graph-engine autopilot touch "<runId>" "<ticketId>"'
  ]);
});

test('autopilot-worker/SKILL.md states the approval gate and iteration limit rules', () => {
  assertAll(read('skills/autopilot-worker/SKILL.md'), 'autopilot-worker', [
    '`settings.autoApproveGates` on',
    '`autopilot-decision-approval`',
    'and only then record it with `graph-engine complete-node',
    "process-ticket's step 4 triage (`reopen-nodes`",
    'requirements-level rethink',
    'report `blocked` with reason `gate_rejected`',
    '`settings.autoApproveGates` off',
    '--awaiting-human "<gate name>: approve or reject"',
    'graph-engine grant-iterations "<ticketId>" "<loopTargetNodeId>" --extra 1',
    'at most once per loop target',
    'report `blocked` with reason `iteration_limit`',
    '`autopilot-decision-iteration`'
  ]);
});

test('autopilot-worker/SKILL.md states the release rules by position', () => {
  assertAll(read('skills/autopilot-worker/SKILL.md'), 'autopilot-worker', [
    '**`single`**',
    '`settings.mainReflection`',
    '`branch`: leave the committed branch as it is',
    '`pull_request`: push the branch and open a pull request',
    '`merge`: also merge it',
    '**`tree_child`**',
    'git merge "<target_branch>"',
    're-run the tests',
    'graph-engine autopilot merge-into-parent "<runId>" "<ticketId>"',
    'report `blocked` with reason `merge_conflict`',
    '**`tree_root`**: commit only',
    '`autopilot-decision-release`'
  ]);
});

test('autopilot-worker/SKILL.md states the handoff and report rules', () => {
  assertAll(read('skills/autopilot-worker/SKILL.md'), 'autopilot-worker', [
    'the carry-over items under the last heading of the review artifacts',
    'the remaining issues in the report',
    '`settings.autoCreateTickets` on',
    'graph-engine create-ticket "<title>" - --parent "<ticketId>"',
    '`PARENT_TICKET_UNSUPPORTED`',
    'do not retry without `--parent`',
    '`settings.autoCreateTickets` off',
    '--awaiting-human "handoff: <n> items"',
    '`autopilot-decision-handoff`',
    'Skip this step when the ticket ends `failed` or `blocked`: create no ticket',
    'graph-engine autopilot report "<runId>" "<ticketId>" --result <done|failed|blocked>',
    'at most 3 lines'
  ]);
});

test('autopilot-worker/SKILL.md keeps merge-up and finalize away from the nodes', () => {
  const body = read('skills/autopilot-worker/SKILL.md');
  const mergeUp = body.slice(body.indexOf('## 7. Role `merge-up`'), body.indexOf('## 8. Role `finalize`'));
  const finalize = body.slice(body.indexOf('## 8. Role `finalize`'));
  assert.ok(mergeUp.length > 0 && finalize.length > 0, 'both role sections must exist');
  for (const [name, section] of [['merge-up', mergeUp], ['finalize', finalize]]) {
    assert.ok(
      section.includes('Do not call `get-executable`, `complete-node`'),
      `the ${name} section must forbid node commands`
    );
  }
  assertAll(mergeUp, 'merge-up section', [
    'git merge "<merge_source_branch>"',
    'resolve the conflicts, re-run the tests',
    'report `blocked` with reason `merge_conflict`'
  ]);
  assertAll(finalize, 'finalize section', ["tree root's worktree", '`settings.mainReflection`']);
});

for (const [name, mode] of [['autopilot-ticket', 'ticket'], ['autopilot-tree', 'tree']]) {
  test(`${name}/SKILL.md only relays what next returns`, () => {
    assertAll(read(`skills/${name}/SKILL.md`), name, [
      `graph-engine autopilot start "<ticketId>" --mode ${mode} [--run "<runId>"]`,
      'Call `autopilot start` first, passing `--run <runId>`',
      'graph-engine autopilot next "<runId>"',
      'run `command` verbatim',
      '- `launch`:',
      '- `wait`:',
      '- `merge-up`:',
      'the finalize action',
      '- `done` or `stopped`:',
      "with the Bash tool's `run_in_background`, then end your turn",
      'Exit code 2: do not judge anything yourself; go back to `next`',
      // DFLT-00142 QA review 1: a failed launch (or merge-up) goes back to
      // next, which moves on once the engine has recorded the failure.
      mode === 'tree' ? '- `launch` or `merge-up` exits with code 1: do not retry it' : '- `launch` exits with code 1: do not retry it',
      'records the ticket as failed (`launch_failed`) after the second one in a row',
      'hands out the same command a third time after two failures in a row',
      "Never do the ticket's work here".replace("the ticket's", mode === 'tree' ? "any ticket's" : "the ticket's"),
      'Keep only the one-line results',
      'graph-engine autopilot summary "<runId>"',
      'running the same command again continues from the recorded state'
    ]);
  });
}

for (const name of ['autopilot-ticket', 'autopilot-tree']) {
  // DFLT-00182: start and launch report an untrusted folder, and the
  // orchestrator passes it on in one line without stopping.
  test(`${name}/SKILL.md passes on untrusted_folder without stopping`, () => {
    const body = read(`skills/${name}/SKILL.md`);
    const start = body.slice(body.indexOf('## 1. Start or resume the run'), body.indexOf('## 2.'));
    assertAll(start, `${name} step 1`, [
      '`untrusted_folder` (with or without `--run`)',
      'open that folder in Claude Code once and accept the trust prompt',
      'carry on without stopping or asking'
    ]);
    const launch = body.slice(body.indexOf('- `launch`:'), body.indexOf('- `wait`:'));
    assertAll(launch, `${name} launch`, [
      'When its line has `untrusted_folder`',
      'waiting at the workspace trust prompt',
      'carry on to `wait` as usual'
    ]);
  });
}

test('the existing skills and node types defer to the autopilot-worker overrides', () => {
  for (const file of [
    'skills/process-ticket/SKILL.md',
    'skills/refine-ticket/SKILL.md',
    'defaults/node-types/release.md',
    'defaults/node-types/approval_gate.md'
  ]) {
    const body = read(file).replace(/\s+/g, ' ');
    assert.ok(
      /the `autopilot-worker` skill\)[^.]{0,40}, that skill's override rules take precedence/.test(body),
      `${file} must note that autopilot-worker's overrides take precedence`
    );
  }
});

// The graph-ops status band (DFLT-00383), run with
// `claude plugin test packages/plugin`. The hooks the tests register stand
// for the engine: the session's ID, its environment, the status files on
// disk and the engine's own drawing.

import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const HOME = '/home/u'
const SID = '0f8e2c1a-1111-4222-8333-944455556666'
const OTHER = '7a7a7a7a-2222-4333-8444-955555555555'
const pathOf = (sid: string) => `${HOME}/.graph-ops/session-status/${sid}.json`

const BAND = {
  plugin: 'graph-ops',
  component: 'AbovePrompt',
  props: {
    hasSurvey: false,
    isWorking: false,
    maxRows: 10,
    bodyColumns: 100,
    scroll: { offset: 0, bodyRows: 10 },
    view: {},
  },
} as const

// The dialog's questions, as the tool's input holds them.
const QUESTIONS: unknown[] = [
  {
    question: 'Approve the plan?',
    header: 'Plan',
    options: [
      { label: 'Approve', description: 'go on' },
      { label: 'Reject', description: 'redo it' },
    ],
    multiSelect: false,
  },
]

const QUESTION = {
  plugin: 'graph-ops',
  component: 'AskUserQuestion',
  props: { tool: 'AskUserQuestion', questions: QUESTIONS },
} as const

type Files = Record<string, { text: string; mtimeMs: number }>

const worker = (sid: string, extra: Record<string, unknown> = {}) =>
  JSON.stringify({
    version: 1,
    session_id: sid,
    updated_at: '2026-11-01T09:00:00Z',
    language: 'ja',
    role: 'worker',
    ticket: { id: 'DFLT-00383', title: 'プロンプト上部に表示する' },
    phase: 'running',
    nodes: [{ id: 'DFLT-00383-04', name: '実装', type: 'implementation' }],
    ...extra,
  })

const controller = (sid: string) =>
  JSON.stringify({
    version: 1,
    session_id: sid,
    updated_at: '2026-11-01T09:00:00Z',
    language: 'en',
    role: 'controller',
    ticket: { id: 'DFLT-00100', title: 'Root of the tree' },
    phase: 'running',
    autopilot: {
      run_id: 'run-20261101-1',
      mode: 'tree',
      state: 'running',
      counts: { done: 3, in_progress: 1, remaining: 2, blocked: 1, failed: 0, skipped: 0 },
      current: { ticket_id: 'DFLT-00105', title: 'Child five' },
      awaiting_human: 'release gate: approve or reject',
      last: { ticket_id: 'DFLT-00104', title: 'Child four', outcome: 'done' },
    },
  })

/**
 * The world beneath the mod: HOME, the session's ID (changeable through
 * `session.id`), the files, an engine that draws "engine" wherever the
 * mod passes, tools that run `ran` (graph-engine writing its status from
 * Bash, say) and answer an empty result, and, given `waits`, a record of the
 * `$.clock.after` waits.
 */
const world = (
  on: On,
  files: Files,
  session: { id: string },
  { ran = () => undefined, waits }: { ran?: (tool: string) => void; waits?: number[] } = {},
) => {
  mock.env(on, { HOME })
  if (waits !== undefined) {
    // The zero waits `$.clock.after` asks for, on their way to the clock
    // (a matcher: mock.clock hooks the event too).
    on('clock.after', { ms: 0 }, (_, e, next) => {
      waits.push(e.ms)
      return next(e)
    })
  }
  const clock = mock.clock(on)
  on('tool.call', (_, e) => {
    ran(e.tool)
    return { result: {} }
  })
  on('session.id', () => ({ value: session.id }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('fs.exists', (_, e) => ({ value: files[e.path] !== undefined }))
  on('fs.stat', (_, e) => {
    const f = files[e.path]
    return f ? { value: { kind: 'file', size: f.text.length, mtimeMs: f.mtimeMs, isLink: false } } : { deny: 'ENOENT' }
  })
  on('fs.read', (_, e) => {
    const f = files[e.path]
    return f ? { value: f.text } : { deny: 'ENOENT' }
  })
  on('ui.render', ($, e) => {
    // The dialog is core's own node; everything else, a stand-in.
    if (e.component === 'AskUserQuestion') {
      return { type: 'engine', ref: 0 }
    }
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  return clock
}

const START = { cwd: '/work', surface: 'terminal', isInteractive: true } as const

test('a session with no status file draws nothing', async ($, on) => {
  world(on, {}, { id: SID })
  await $.session.start(START)
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    expect((await ui.find({ type: 'Text', text: /engine/ }))?.text).toBe('engine')
    expect(await ui.find({ type: 'Text', text: /DFLT/ })).toBeUndefined()
    await ui.unmount()
  }
  const q = await $.ui.mount({ ...QUESTION, surface: 'terminal' })
  expect(await q.find({ type: 'Text' })).toBeUndefined()
  await q.unmount()
})

test("a worker's band names the ticket and the running node", async ($, on) => {
  world(on, { [pathOf(SID)]: { text: worker(SID), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    expect((await ui.find({ type: 'Text', text: /DFLT-00383/ }))?.text).toBe('DFLT-00383 プロンプト上部に表示する')
    expect((await ui.find({ type: 'Text', text: /実行中/ }))?.text).toBe('実行中: 実装 (implementation)')
    await ui.unmount()
  }
})

test("a controller's band shows the tree, the running worker and the last result", async ($, on) => {
  world(on, { [pathOf(SID)]: { text: controller(SID), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /^autopilot/ }))?.text).toBe('autopilot tree: DFLT-00100 Root of the tree')
  expect((await ui.find({ type: 'Text', text: /^Tree:/ }))?.text).toBe('Tree: done 3 / in progress 1 / remaining 2 / blocked 1')
  expect((await ui.find({ type: 'Text', text: /^Worker:/ }))?.text).toBe(
    'Worker: DFLT-00105 Child five (waiting for a person: release gate: approve or reject)',
  )
  expect((await ui.find({ type: 'Text', text: /^Last:/ }))?.text).toBe('Last: DFLT-00104 Child four - done')
  await ui.unmount()
})

test('the band keeps to the rows it is given', async ($, on) => {
  world(on, { [pathOf(SID)]: { text: controller(SID), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, props: { ...BAND.props, maxRows: 2 }, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /^autopilot/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /^Tree:/ })).toBeUndefined()
  await ui.unmount()
})

test("another session's file, or a file naming another session, is never shown", async ($, on) => {
  world(
    on,
    {
      [pathOf(OTHER)]: { text: worker(OTHER), mtimeMs: 1 },
      // In this session's file name, but written for another session.
      [pathOf(SID)]: { text: worker(OTHER), mtimeMs: 1 },
    },
    { id: SID },
  )
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /DFLT/ })).toBeUndefined()
  await ui.unmount()
})

test('an AskUserQuestion dialog gets the ticket as its heading', async ($, on) => {
  world(on, { [pathOf(SID)]: { text: worker(SID, { phase: 'awaiting_human', detail: '計画の承認', nodes: [] }), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...QUESTION, surface })
    expect((await ui.find({ type: 'Text', text: /^\[DFLT-00383\]/ }))?.text).toMatch(/^\[DFLT-00383\] プロンプト/)
    await ui.unmount()
  }
})

test('a long heading stays one row of the dialog', async ($, on) => {
  const long = 'とても長いチケットのタイトル'.repeat(10)
  world(on, { [pathOf(SID)]: { text: worker(SID, { ticket: { id: 'DFLT-00383', title: long } }), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  const ui = await $.ui.mount({ ...QUESTION, surface: 'terminal' })
  const heading = await ui.find({ type: 'Text', text: /^\[DFLT-00383\]/ })
  expect(heading).toBeDefined()
  await ui.unmount()
})

test('an old status stays on screen: a person may answer days later', async ($, on) => {
  world(on, { [pathOf(SID)]: { text: worker(SID, { updated_at: '2020-01-01T00:00:00Z', phase: 'awaiting_approval' }), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /承認待ち/ }))?.text).toMatch(/^承認待ち/)
  await ui.unmount()
})

test('the band follows the file, and a new session ID (after /clear)', async ($, on) => {
  const files: Files = { [pathOf(SID)]: { text: worker(SID), mtimeMs: 1 } }
  const session = { id: SID }
  const clock = world(on, files, session)
  await $.session.start(START)
  let ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /実行中/ })).toBeDefined()
  await ui.unmount()

  // graph-engine rewrote the file: the next look picks it up.
  files[pathOf(SID)] = { text: worker(SID, { phase: 'releasing', nodes: [] }), mtimeMs: 2 }
  await clock.advance(2000)
  ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /リリース中/ }))?.text).toBe('リリース中')
  await ui.unmount()

  // The session goes on under a new ID with no status of its own yet.
  session.id = OTHER
  await clock.advance(2000)
  ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /DFLT/ })).toBeUndefined()
  await ui.unmount()

  files[pathOf(OTHER)] = { text: controller(OTHER), mtimeMs: 3 }
  await clock.advance(2000)
  ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /DFLT-00100/ })).toBeDefined()
  await ui.unmount()

  // Cleared: gone.
  delete files[pathOf(OTHER)]
  await clock.advance(2000)
  ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /DFLT/ })).toBeUndefined()
  await ui.unmount()
})

test('line breaks and escape sequences in a title are folded into one line', async ($, on) => {
  const bidi = String.fromCharCode(0x202e)
  world(on, { [pathOf(SID)]: { text: worker(SID, { ticket: { id: 'DFLT-00383', title: `evil\x1b[2J\ntitle${bidi}\tend` } }), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /DFLT-00383/ }))?.text).toBe('DFLT-00383 evil[2J title end')
  await ui.unmount()
})

test('a survey keeps the band', async ($, on) => {
  world(on, { [pathOf(SID)]: { text: worker(SID), mtimeMs: 1 } }, { id: SID })
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, props: { ...BAND.props, hasSurvey: true }, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /DFLT/ })).toBeUndefined()
  await ui.unmount()
})

test('a Bash call shows the new status at once, before the next poll', async ($, on) => {
  const files: Files = { [pathOf(SID)]: { text: worker(SID), mtimeMs: 1 } }
  const waits: number[] = []
  const clock = world(on, files, { id: SID }, {
    waits,
    // graph-engine, run from Bash, rewrites the status.
    ran: tool => {
      if (tool === 'Bash') {
        files[pathOf(SID)] = { text: worker(SID, { phase: 'releasing', nodes: [] }), mtimeMs: 3 }
      }
    },
  })
  await $.session.start(START)
  let ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /実行中/ }))?.text).toBe('実行中: 実装 (implementation)')
  await ui.unmount()

  // Another tool: the file changed, but nothing looks before the poll.
  files[pathOf(SID)] = { text: worker(SID, { phase: 'awaiting_approval', nodes: [] }), mtimeMs: 2 }
  const read = await $.tool.call({ tool: 'Read', file_path: '/work/a.txt' })
  expect(read.result).toEqual({})
  await clock.settle()
  ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /実行中/ }))?.text).toBe('実行中: 実装 (implementation)')
  expect(await ui.find({ type: 'Text', text: /承認待ち/ })).toBeUndefined()
  await ui.unmount()
  expect(waits).toEqual([])

  // Bash: its result comes back, and the band shows the new status with
  // the clock where it was, from a timer the hook asked for.
  const before = clock.now()
  const bash = await $.tool.call({ tool: 'Bash', command: 'graph-engine complete-node DFLT-00383-04 true' })
  expect(bash.result).toEqual({})
  await clock.settle()
  expect(clock.now()).toBe(before)
  ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /リリース中/ }))?.text).toBe('リリース中')
  await ui.unmount()
  expect(waits).toEqual([0])
})

test('a Bash call before session.start keeps its result, and the band still comes', async ($, on) => {
  const clock = world(on, { [pathOf(SID)]: { text: worker(SID), mtimeMs: 1 } }, { id: SID })
  const bash = await $.tool.call({ tool: 'Bash', command: 'ls' })
  expect(bash.result).toEqual({})
  await clock.settle()
  await $.session.start(START)
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /実行中/ }))?.text).toBe('実行中: 実装 (implementation)')
  await ui.unmount()
})

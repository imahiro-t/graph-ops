// The graph-ops mod (DFLT-00383): a band above the prompt naming the ticket
// this Claude Code session works on and what it is doing, and the same
// ticket over every AskUserQuestion dialog, so a person running several
// terminals at once always knows which ticket a question is about.
//
// graph-engine writes the status, one file per Claude Code session
// ($HOME/.graph-ops/session-status/<sessionId>.json); this module only reads
// its own session's. A session that runs no graph-ops skill has no file, and
// the mod then draws nothing.

import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { GraphOpsStatus } from '../types'
import { bandLines, fit, parseStatus, questionHeading, statusPath } from './status-band'

const status = atom({ plugin: 'graph-ops', key: 'status' } as const, null as GraphOpsStatus | null)

/** How often the status file is looked at, in milliseconds. */
const POLL_MS = 2000

/**
 * What the state was last read from: the home directory, the session ID and
 * the file's modification time. The ID is asked for again on every look:
 * after /clear the session goes on under a new ID, and the band must follow
 * it (and drop the old session's status).
 */
const view = { home: '', sessionId: '', mtime: -1, busy: false }

/** Looks at this session's status file and keeps what it says. */
async function refresh($: EngineInterface): Promise<void> {
  if (view.busy) {
    return
  }
  view.busy = true
  try {
    const id = await $.session.id()
    if (id !== view.sessionId) {
      view.sessionId = id
      view.mtime = -1
      await update($, status, () => null)
    }
    const path = statusPath(view.home, id)
    if (path === undefined) {
      return
    }
    // exists first: a session with no status is the common case, and a stat
    // of a missing file is a failed call (a debug log line every poll).
    const exists = await $.fs.exists(path).catch(() => false)
    const stat = exists ? await $.fs.stat(path).catch(() => undefined) : undefined
    if (stat === undefined || stat.kind !== 'file') {
      if (view.mtime !== 0) {
        view.mtime = 0
        await update($, status, () => null)
      }
      return
    }
    if (stat.mtimeMs === view.mtime) {
      return
    }
    const text = await $.fs.read(path).catch(() => undefined)
    view.mtime = stat.mtimeMs
    const next = text === undefined ? null : parseStatus(text, id)
    await update($, status, () => next)
  } finally {
    view.busy = false
  }
}

export const register: Register = on => {
  Object.assign(view, { home: '', sessionId: '', mtime: -1, busy: false })

  on('session.start', async ($, e, next) => {
    const result = await next(e)
    view.home = (await $.env.get('HOME')) ?? (await $.env.get('USERPROFILE')) ?? ''
    await refresh($).catch(() => undefined)
    $.clock.every(POLL_MS, () => {
      refresh($).catch(() => undefined)
    })
    return result
  })

  // graph-engine writes the status from Bash; look again as soon as a Bash
  // call ends rather than up to a poll later. Not awaited: the call's result
  // goes back at once, whatever the look costs (view.busy keeps looks from
  // overlapping).
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    const result = await next(e)
    void refresh($).catch(() => undefined)
    return result
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const s = await read($, status)
    if (s === null || e.props.hasSurvey) {
      return next(e)
    }
    const lines = bandLines(s, e.props.maxRows)
    if (lines.length === 0) {
      return next(e)
    }
    const { Box, Text } = $.ui.resolve(e)
    const columns = e.props.bodyColumns
    return (
      <Box flexDirection="column">
        {lines.map((line, i) =>
          i === 0 ? (
            <Text bold color="claude" wrap="truncate-end">
              {fit(line, columns)}
            </Text>
          ) : (
            <Text dimColor wrap="truncate-end">
              {fit(line, columns)}
            </Text>
          ),
        )}
      </Box>
    )
  })

  on('ui.render', { component: 'AskUserQuestion' }, async ($, e, next) => {
    const s = await read($, status)
    if (s === null) {
      return next(e)
    }
    const heading = questionHeading(s)
    if (heading === '') {
      return next(e)
    }
    const { Box, Text } = $.ui.resolve(e)
    // One row over the engine's own dialog, which stays the last (and only
    // other) thing in the tree: a dialog's tree is held to 12 rows on the
    // terminal, so the heading never wraps (truncate-end) and is cut short
    // before it reaches the surface.
    return (
      <Box flexDirection="column">
        <Text bold color="claude" wrap="truncate-end">
          {fit(heading, 160)}
        </Text>
        {await next(e)}
      </Box>
    )
  })
}

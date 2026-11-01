// The graph-ops status band's pure part (DFLT-00383): reading graph-engine's
// status file into the mod's state, and turning that state into the lines
// the band and the AskUserQuestion heading draw. No `$` here, so every rule
// is plain data in, plain data out.

import type {
  GraphOpsAutopilot,
  GraphOpsNode,
  GraphOpsPhase,
  GraphOpsRunTicket,
  GraphOpsStatus,
} from '../types'

/** The longest a drawn string is kept before the surface truncates it. */
const MAX_TEXT = 200

/** The most nodes a band names. */
const MAX_NODES = 8

const PHASES: readonly GraphOpsPhase[] = [
  'refining',
  'planning',
  'running',
  'awaiting_approval',
  'awaiting_human',
  'releasing',
  'finishing',
  'idle',
]

/** What a Claude Code session ID looks like when it can name a file. */
const SESSION_ID = /^[A-Za-z0-9_-]{1,128}$/

/**
 * The status file of session `sessionId` under `home`, or undefined when the
 * ID could name something else (graph-engine refuses to write such a file).
 */
export const statusPath = (home: string, sessionId: string): string | undefined => {
  if (!SESSION_ID.test(sessionId) || home === '') {
    return undefined
  }
  const base = home.replace(/[\\/]+$/, '')
  return `${base}/.graph-ops/session-status/${sessionId}.json`
}

/**
 * One line of plain text: tabs and line breaks become a space, every other
 * control character (C0, DEL, C1) and every invisible format character
 * (bidirectional controls, zero-width characters) is dropped, runs of white
 * space are collapsed, and the result is cut to `max` characters.
 *
 * graph-engine already cleans what it writes; this is the mod's own guard,
 * since the file is only a file.
 */
export const toLine = (value: unknown, max = MAX_TEXT): string => {
  if (typeof value !== 'string') {
    return ''
  }
  const line = value
    .replace(/[\t\n\v\f\r\u0085\u2028\u2029]/g, ' ')
    .replace(/[\u0000-\u001f\u007f-\u009f\u00ad\u061c\u180e\u200b-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9-\ufffb]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
  const chars = Array.from(line)
  return chars.length > max ? chars.slice(0, max).join('').trimEnd() : line
}

const count = (value: unknown): number =>
  typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0

const record = (value: unknown): Record<string, unknown> | undefined =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined

const runTicket = (value: unknown): GraphOpsRunTicket | null => {
  const t = record(value)
  const id = toLine(t?.ticket_id, 64)
  return t && id !== '' ? { id, title: toLine(t.title), outcome: toLine(t.outcome, 16) } : null
}

const autopilot = (value: unknown): GraphOpsAutopilot | null => {
  const a = record(value)
  const runId = toLine(a?.run_id, 128)
  if (!a || runId === '') {
    return null
  }
  const c = record(a.counts) ?? {}
  return {
    runId,
    mode: toLine(a.mode, 16),
    state: toLine(a.state, 16),
    done: count(c.done),
    inProgress: count(c.in_progress),
    remaining: count(c.remaining),
    blocked: count(c.blocked),
    failed: count(c.failed),
    skipped: count(c.skipped),
    current: runTicket(a.current),
    awaitingHuman: toLine(a.awaiting_human),
    last: runTicket(a.last),
  }
}

/**
 * The status in `text`, the file graph-engine wrote for `sessionId`, or null
 * when there is nothing to show: the text does not parse, it is another
 * session's, or it names no ticket. Its age is never a reason: a session can
 * wait for a person's approval for days, and that is when the band matters.
 */
export const parseStatus = (text: string, sessionId: string): GraphOpsStatus | null => {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    return null
  }
  const s = record(raw)
  if (!s || s.session_id !== sessionId) {
    return null
  }
  const ticket = record(s.ticket)
  const ticketId = toLine(ticket?.id, 64)
  const ap = autopilot(s.autopilot)
  if (ticketId === '' && ap === null) {
    return null
  }
  const phase = PHASES.find(p => p === s.phase) ?? ''
  const nodes: GraphOpsNode[] = (Array.isArray(s.nodes) ? s.nodes : [])
    .map(n => record(n))
    .filter((n): n is Record<string, unknown> => n !== undefined && toLine(n.id, 64) !== '')
    .slice(0, MAX_NODES)
    .map(n => ({ id: toLine(n.id, 64), name: toLine(n.name), type: toLine(n.type, 32) }))
  return {
    sessionId,
    language: toLine(s.language, 16),
    role: s.role === 'controller' ? 'controller' : 'worker',
    ticketId,
    ticketTitle: toLine(ticket?.title),
    phase,
    detail: toLine(s.detail),
    nodes,
    autopilot: ap,
  }
}

type Words = {
  phases: Record<GraphOpsPhase, string>
  tree: (a: GraphOpsAutopilot) => string
  worker: string
  awaiting: string
  last: string
  noWorker: string
  autopilot: string
}

const counts = (a: GraphOpsAutopilot, names: [string, string, string, string, string]): string => {
  const parts = [`${names[0]} ${a.done}`, `${names[1]} ${a.inProgress}`, `${names[2]} ${a.remaining}`]
  if (a.blocked > 0) parts.push(`${names[3]} ${a.blocked}`)
  if (a.failed > 0) parts.push(`${names[4]} ${a.failed}`)
  return parts.join(' / ')
}

const WORDS: Record<'en' | 'ja', Words> = {
  en: {
    phases: {
      refining: 'Refining',
      planning: 'Refined, waiting to be processed',
      running: 'Running',
      awaiting_approval: 'Awaiting approval',
      awaiting_human: 'Waiting for your answer',
      releasing: 'Releasing',
      finishing: 'Finishing',
      idle: 'Idle',
    },
    tree: a => `Tree: ${counts(a, ['done', 'in progress', 'remaining', 'blocked', 'failed'])}`,
    worker: 'Worker',
    awaiting: 'waiting for a person',
    last: 'Last',
    noWorker: 'Worker: none running',
    autopilot: 'autopilot',
  },
  ja: {
    phases: {
      refining: '要件整理中',
      planning: '整理済み・処理待ち',
      running: '実行中',
      awaiting_approval: '承認待ち',
      awaiting_human: '回答待ち',
      releasing: 'リリース中',
      finishing: '仕上げ中',
      idle: '待機中',
    },
    tree: a => `ツリー: ${counts(a, ['完了', '処理中', '残り', 'ブロック', '失敗'])}`,
    worker: '稼働中',
    awaiting: '人の判断待ち',
    last: '直近',
    noWorker: '稼働中: なし',
    autopilot: 'オートパイロット',
  },
}

const words = (language: string): Words => (language.toLowerCase().startsWith('ja') ? WORDS.ja : WORDS.en)

const ticketText = (id: string, title: string): string => (title === '' ? id : `${id} ${title}`)

/** What the session is doing, in one line: the phase and its detail or nodes. */
export const activityLine = (s: GraphOpsStatus): string => {
  const w = words(s.language)
  const phase = s.phase === '' ? '' : w.phases[s.phase]
  const nodes = s.nodes.map(n => (n.type === '' ? n.name || n.id : `${n.name || n.id} (${n.type})`)).join(', ')
  const what = [s.detail, nodes].filter(x => x !== '').join(' | ')
  return [phase, what].filter(x => x !== '').join(': ')
}

/** The heading line: the ticket, and for a controller that it is one. */
export const headingLine = (s: GraphOpsStatus): string => {
  const w = words(s.language)
  const ticket = s.ticketId === '' ? '' : ticketText(s.ticketId, s.ticketTitle)
  if (s.role === 'controller' && s.autopilot) {
    const mode = s.autopilot.mode === '' ? '' : ` ${s.autopilot.mode}`
    return `${w.autopilot}${mode}: ${ticket}`.trim()
  }
  return ticket
}

/**
 * The band's lines, at most `maxRows`: the ticket, what is happening, and
 * for a controller the tree's progress, the running worker and the latest
 * result.
 */
export const bandLines = (s: GraphOpsStatus, maxRows: number): string[] => {
  const w = words(s.language)
  const lines = [headingLine(s), activityLine(s)]
  const a = s.role === 'controller' ? s.autopilot : null
  if (a) {
    lines.push(w.tree(a))
    if (a.current) {
      const waiting = a.awaitingHuman === '' ? '' : ` (${w.awaiting}: ${a.awaitingHuman})`
      lines.push(`${w.worker}: ${ticketText(a.current.id, a.current.title)}${waiting}`)
    } else {
      lines.push(w.noWorker)
    }
    if (a.last) {
      lines.push(`${w.last}: ${ticketText(a.last.id, a.last.title)} - ${a.last.outcome}`)
    }
  }
  const shown = lines.filter(l => l !== '')
  return shown.slice(0, Math.max(1, Math.floor(maxRows)))
}

/** The one-line heading over an AskUserQuestion dialog. */
export const questionHeading = (s: GraphOpsStatus): string => {
  const ticket = s.ticketId === '' ? '' : `[${s.ticketId}] ${s.ticketTitle}`.trim()
  return [ticket, activityLine(s)].filter(x => x !== '').join(' - ')
}

/** `line` cut to `columns` cells, a wide (CJK) character counting two. */
export const fit = (line: string, columns: number): string => {
  const max = Math.max(1, Math.floor(columns))
  let width = 0
  let out = ''
  for (const ch of line) {
    const cp = ch.codePointAt(0) ?? 0
    const w = isWide(cp) ? 2 : 1
    if (width + w > max) {
      return out.length < line.length && max > 1 ? `${trimToWidth(out, max - 1)}…` : out
    }
    width += w
    out += ch
  }
  return out
}

const trimToWidth = (s: string, max: number): string => {
  let width = 0
  let out = ''
  for (const ch of s) {
    const w = isWide(ch.codePointAt(0) ?? 0) ? 2 : 1
    if (width + w > max) break
    width += w
    out += ch
  }
  return out
}

const isWide = (cp: number): boolean =>
  (cp >= 0x1100 && cp <= 0x115f) ||
  (cp >= 0x2e80 && cp <= 0xa4cf) ||
  (cp >= 0xac00 && cp <= 0xd7a3) ||
  (cp >= 0xf900 && cp <= 0xfaff) ||
  (cp >= 0xfe30 && cp <= 0xfe4f) ||
  (cp >= 0xff00 && cp <= 0xff60) ||
  (cp >= 0xffe0 && cp <= 0xffe6) ||
  (cp >= 0x1f300 && cp <= 0x1faff) ||
  (cp >= 0x20000 && cp <= 0x3fffd)

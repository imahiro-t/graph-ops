// The graph-ops mod's state contract (DFLT-00383): the status band's data,
// read from the file graph-engine writes for this Claude Code session
// ($HOME/.graph-ops/session-status/<sessionId>.json) and checked and cleaned
// by hooks/register.tsx before it is kept here.

export type GraphOpsPhase =
  | 'refining'
  | 'planning'
  | 'running'
  | 'awaiting_approval'
  | 'awaiting_human'
  | 'releasing'
  | 'finishing'
  | 'idle'

export type GraphOpsNode = { id: string; name: string; type: string }

export type GraphOpsRunTicket = { id: string; title: string; outcome: string }

export type GraphOpsAutopilot = {
  runId: string
  mode: string
  state: string
  done: number
  inProgress: number
  remaining: number
  blocked: number
  failed: number
  skipped: number
  current: GraphOpsRunTicket | null
  awaitingHuman: string
  last: GraphOpsRunTicket | null
}

export type GraphOpsStatus = {
  sessionId: string
  language: string
  role: 'worker' | 'controller'
  ticketId: string
  ticketTitle: string
  phase: GraphOpsPhase | ''
  detail: string
  nodes: GraphOpsNode[]
  autopilot: GraphOpsAutopilot | null
}

declare module 'claude-code' {
  interface PluginState {
    'graph-ops': { status: GraphOpsStatus | null }
  }
}

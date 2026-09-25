import { describe, expect, it } from 'vitest';
import { AutopilotRun } from '../types';
import { NO_AUTOPILOT, descendantsIndex, ticketAutopilotView } from './autopilotApi';

// DFLT-00142 phase 5: the rule the buttons follow must match the server's
// (autopilot.Registry.Begin): the newest run of a root and mode is the one a
// start takes over; an active run blocks its members, and a tree start also
// when it roots below the ticket.

const tree = [
  { id: 'A', parent_ticket_id: null },
  { id: 'B', parent_ticket_id: 'A' },
  { id: 'C', parent_ticket_id: 'B' },
  { id: 'D', parent_ticket_id: null }
];
const descendantsOf = descendantsIndex(tree);

function run(o: Partial<AutopilotRun>): AutopilotRun {
  return {
    run_id: 'run-x',
    project_id: 'p',
    mode: 'tree',
    root: 'B',
    state: 'running',
    active: true,
    heartbeat: '',
    tickets: {},
    members: ['B', 'C'],
    ...o
  };
}

describe('descendantsIndex', () => {
  it('returns every descendant, not the ticket itself', () => {
    expect([...descendantsOf('A')].sort()).toEqual(['B', 'C']);
    expect([...descendantsOf('C')]).toEqual([]);
  });
});

describe('ticketAutopilotView', () => {
  it('is NO_AUTOPILOT with no runs', () => {
    expect(ticketAutopilotView([], 'A', descendantsOf)).toBe(NO_AUTOPILOT);
  });

  it('blocks members for both modes and ancestors of the root for tree only', () => {
    const runs = [run({})];
    expect(ticketAutopilotView(runs, 'C', descendantsOf).blockedBy).toEqual({ ticket: 'B', tree: 'B' });
    expect(ticketAutopilotView(runs, 'A', descendantsOf).blockedBy).toEqual({ ticket: '', tree: 'B' });
    expect(ticketAutopilotView(runs, 'D', descendantsOf)).toBe(NO_AUTOPILOT);
  });

  it('gives the root the running badge and the current ticket processing', () => {
    const runs = [run({ current: 'B', tickets: { B: 'launched' } })];
    expect(ticketAutopilotView(runs, 'B', descendantsOf).badges).toEqual(['running', 'processing']);
    expect(ticketAutopilotView(runs, 'C', descendantsOf).badges).toEqual(['waiting']);
  });

  it('does not mark a reached, finished member as waiting', () => {
    const runs = [run({ tickets: { B: 'done', C: 'skipped' } })];
    expect(ticketAutopilotView(runs, 'C', descendantsOf).badges).toEqual([]);
  });

  it('is resumable only when the newest run of that root and mode is stopped or interrupted', () => {
    const stopped = run({ active: false, state: 'stopped', members: [] });
    const interrupted = run({ active: false, state: 'running', members: [] });
    const finished = run({ active: false, state: 'finished', members: [] });
    expect(ticketAutopilotView([stopped], 'B', descendantsOf).resumable).toEqual({ ticket: false, tree: true });
    expect(ticketAutopilotView([interrupted], 'B', descendantsOf).resumable.tree).toBe(true);
    // Newest first: a finished run hides an older stopped one.
    expect(ticketAutopilotView([finished, stopped], 'B', descendantsOf).resumable.tree).toBe(false);
    expect(ticketAutopilotView([stopped, finished], 'B', descendantsOf).resumable.tree).toBe(true);
  });
});

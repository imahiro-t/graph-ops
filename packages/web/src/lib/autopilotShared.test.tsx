// DFLT-00326: runs shared between members. Another member's run (mine:
// false) blocks starts like one's own, names that member in the reason and
// on the running badge, and is never offered for resuming; a response
// without started_by / mine (an older server) reads as this machine's runs.
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { AutopilotRun } from '../types';
import { AutopilotBadges } from '../components/AutopilotBadges';
import { AutopilotControls } from '../components/AutopilotControls';
import { NO_AUTOPILOT, descendantsIndex, startErrorMessage, starterLabel, ticketAutopilotView } from './autopilotApi';

// R -> X -> Y, R -> S
const tree = [
  { id: 'R', parent_ticket_id: null },
  { id: 'X', parent_ticket_id: 'R' },
  { id: 'Y', parent_ticket_id: 'X' },
  { id: 'S', parent_ticket_id: 'R' }
];
const descendantsOf = descendantsIndex(tree);

function run(o: Partial<AutopilotRun>): AutopilotRun {
  return {
    run_id: 'run-a',
    project_id: 'p',
    mode: 'tree',
    root: 'R',
    state: 'running',
    active: true,
    heartbeat: '',
    tickets: {},
    members: ['R', 'X', 'Y', 'S'],
    pending: ['X', 'Y', 'S'],
    ...o
  };
}

const alice = { name: 'Alice', name_is_fallback: false };

afterEach(async () => {
  cleanup();
  await i18n.changeLanguage('ja');
});

describe('ticketAutopilotView with other members', () => {
  it("blocks both modes inside another member's tree and names them", () => {
    const view = ticketAutopilotView([run({ started_by: alice, mine: false })], 'X', descendantsOf);
    expect(view.blockedBy).toEqual({ ticket: 'R', tree: 'R' });
    expect(view.blockedByStarter).toEqual({ ticket: alice, tree: alice });
    expect(view.runningBy).toBeNull();
  });

  it("blocks only the tree of an ancestor of another member's root", () => {
    const view = ticketAutopilotView(
      [run({ root: 'X', members: ['X', 'Y'], pending: ['Y'], started_by: alice, mine: false })],
      'R',
      descendantsOf
    );
    expect(view.blockedBy).toEqual({ ticket: '', tree: 'X' });
    expect(view.blockedByStarter).toEqual({ ticket: null, tree: alice });
  });

  it("puts the member's name on the running badge of their root", () => {
    const view = ticketAutopilotView([run({ started_by: alice, mine: false })], 'R', descendantsOf);
    expect(view.badges).toContain('running');
    expect(view.runningBy).toEqual(alice);
  });

  it('keeps its own runs unnamed', () => {
    const view = ticketAutopilotView([run({ started_by: { name: 'Me', name_is_fallback: false }, mine: true })], 'R', descendantsOf);
    expect(view.runningBy).toBeNull();
    expect(view.blockedByStarter).toEqual({ ticket: null, tree: null });
  });

  it("never offers to resume another member's stopped or interrupted run", () => {
    for (const state of ['stopped', 'running']) {
      const foreign = run({ state, active: false, members: [], pending: [], started_by: alice, mine: false });
      expect(ticketAutopilotView([foreign], 'R', descendantsOf)).toBe(NO_AUTOPILOT);
    }
  });

  it("judges resuming on its own newest run, past a newer run of another member", () => {
    const foreign = run({ run_id: 'run-b', state: 'finished', active: false, members: [], pending: [], started_by: alice, mine: false });
    const own = run({ run_id: 'run-a', state: 'stopped', active: false, members: [], pending: [], mine: true });
    expect(ticketAutopilotView([foreign, own], 'R', descendantsOf).resumable.tree).toBe(true);
  });

  it('treats runs without started_by and mine (an older server) as its own', () => {
    const stopped = run({ state: 'stopped', active: false, members: [], pending: [] });
    expect(ticketAutopilotView([stopped], 'R', descendantsOf).resumable.tree).toBe(true);
    const active = ticketAutopilotView([run({})], 'R', descendantsOf);
    expect(active.runningBy).toBeNull();
    expect(active.blockedByStarter).toEqual({ ticket: null, tree: null });
  });
});

describe.each(['ja', 'en'] as const)('shared-run texts (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('labels a fallback name as not set', () => {
    expect(starterLabel(i18n.t, alice)).toBe('Alice');
    const fallback = starterLabel(i18n.t, { name: 'taro@mac01', name_is_fallback: true });
    expect(fallback).toBe(i18n.t('autopilot.fallbackName', { name: 'taro@mac01' }));
    expect(fallback).toContain('taro@mac01');
    expect(fallback).not.toBe('taro@mac01');
  });

  it("shows '<name> is running' on another member's root, in the running badge's style", () => {
    const view = ticketAutopilotView([run({ started_by: alice, mine: false })], 'R', descendantsOf);
    render(<AutopilotBadges view={view} />);
    const badge = screen.getByTestId('autopilot-badge-running');
    expect(badge).toHaveTextContent(i18n.t('autopilot.badges.runningBy', { name: 'Alice' }));
    expect(badge.textContent).toContain('Alice');
    cleanup();
    render(<AutopilotBadges view={ticketAutopilotView([run({ mine: true })], 'R', descendantsOf)} />);
    const own = screen.getByTestId('autopilot-badge-running');
    expect(own).toHaveTextContent(i18n.t('autopilot.badges.running'));
    expect(own.className).toBe(badge.className);
  });

  // jsdom does no layout: this checks that a long name gets the classes
  // every badge wraps with (wrap-anywhere, min-w-0 inside a wrapping row),
  // so it wraps at 320px and 200% text like the other badges.
  it('lets a long member name wrap like every other badge', () => {
    const long = { name: 'a-very-long-member-name-without-any-spaces@a-very-long-host-name.example.internal', name_is_fallback: true };
    const view = ticketAutopilotView([run({ started_by: long, mine: false })], 'R', descendantsOf);
    render(<AutopilotBadges view={view} />);
    const badge = screen.getByTestId('autopilot-badge-running');
    expect(badge).toHaveClass('wrap-anywhere', 'min-w-0');
    expect(screen.getByTestId('autopilot-badges')).toHaveClass('flex-wrap', 'max-w-full', 'min-w-0');
    expect(badge.textContent).toContain(long.name);
  });

  it('adds the not-set mark to a fallback name on the badge', () => {
    const starter = { name: 'taro@mac01', name_is_fallback: true };
    const view = ticketAutopilotView([run({ started_by: starter, mine: false })], 'R', descendantsOf);
    render(<AutopilotBadges view={view} />);
    expect(screen.getByTestId('autopilot-badge-running')).toHaveTextContent(
      i18n.t('autopilot.fallbackName', { name: 'taro@mac01' })
    );
  });

  it("names the member and the root in the disabled reason (blockedBy)", () => {
    const view = ticketAutopilotView([run({ started_by: alice, mine: false })], 'X', descendantsOf);
    render(<AutopilotControls ticketId="X" status="TODO" view={view} />);
    expect(screen.getByTestId('autopilot-start')).toBeDisabled();
    const reason = screen.getByTestId('autopilot-disabled-reason');
    expect(reason).toHaveTextContent(i18n.t('autopilot.blockedBy', { root: 'R', name: 'Alice' }));
    expect(reason.textContent).toContain('Alice');
    expect(reason.textContent).toContain('R');
  });

  it("names the member and the descendant root for a tree start (blockedDescendantBy)", async () => {
    const view = ticketAutopilotView(
      [run({ root: 'X', members: ['X', 'Y'], pending: ['Y'], started_by: alice, mine: false })],
      'R',
      descendantsOf
    );
    const user = userEvent.setup();
    render(<AutopilotControls ticketId="R" status="TODO" view={view} />);
    // The ticket mode can still start, so the button is enabled.
    const button = screen.getByTestId('autopilot-start');
    expect(button).toBeEnabled();
    await user.click(button);
    const dialog = screen.getByRole('dialog');
    const treeChoice = within(dialog).getByRole('radio', { name: i18n.t('autopilot.modeOptions.tree') });
    const single = within(dialog).getByRole('radio', { name: i18n.t('autopilot.modeOptions.ticket') });
    expect(treeChoice).toBeDisabled();
    const reason = i18n.t('autopilot.blockedDescendantBy', { root: 'X', name: 'Alice' });
    expect(reason).toContain('Alice');
    expect(reason).toContain('X');
    expect(treeChoice).toHaveAccessibleDescription(reason);
    expect(single).toBeEnabled();
  });

  it('keeps the old wording for its own run', () => {
    const view = ticketAutopilotView([run({ mine: true })], 'X', descendantsOf);
    render(<AutopilotControls ticketId="X" status="TODO" view={view} />);
    expect(screen.getByTestId('autopilot-disabled-reason')).toHaveTextContent(i18n.t('autopilot.blocked', { root: 'R' }));
  });

  it('names who started the run in a refused start', () => {
    expect(startErrorMessage(i18n.t, 'AUTOPILOT_ALREADY_RUNNING', { started_by: 'Alice' })).toBe(
      i18n.t('autopilot.alreadyRunningBy', { name: 'Alice' })
    );
    expect(
      startErrorMessage(i18n.t, 'AUTOPILOT_ALREADY_RUNNING', { started_by: 'taro@mac01', name_is_fallback: true })
    ).toContain(i18n.t('autopilot.fallbackName', { name: 'taro@mac01' }));
    expect(startErrorMessage(i18n.t, 'AUTOPILOT_ALREADY_RUNNING')).toBe(i18n.t('errors.AUTOPILOT_ALREADY_RUNNING'));
  });
});

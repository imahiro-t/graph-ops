// DFLT-00142 accessibility review (iteration 1): unique ids per section,
// label-in-name, decorative icons, and what a sighted keyboard user can read.
import { act, cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact } from '../types';
import { AutopilotDecisions } from './AutopilotDecisions';
import { AutopilotControls, SETTLE_TIMEOUT_MS } from './AutopilotControls';
import { TicketFamily } from './TicketFamily';
import { NO_AUTOPILOT } from '../lib/autopilotApi';

function artifact(id: string, name: string): Artifact {
  return { id, ticket_id: 'T', node_id: 'N', name, type: 'text', created_at: '2026-09-25T10:00:00Z' };
}

describe('autopilot accessibility', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('ja');
  });
  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('gives every decisions section its own heading id (several tickets can be expanded)', () => {
    render(
      <>
        <AutopilotDecisions artifacts={[artifact('a1', 'autopilot-decision-refine')]} nodes={[]} />
        <AutopilotDecisions
          artifacts={[artifact('b1', 'autopilot-decision-refine'), artifact('b2', 'autopilot-tree-summary')]}
          nodes={[]}
        />
      </>
    );
    const sections = screen.getAllByTestId('autopilot-decisions');
    const ids = sections.map(s => s.getAttribute('aria-labelledby'));
    expect(new Set(ids).size).toBe(2);
    for (const s of sections) {
      expect(document.getElementById(s.getAttribute('aria-labelledby')!)).toBe(within(s).getByRole('heading'));
    }
    expect(screen.getByRole('region', { name: i18n.t('autopilot.decisions.title', { count: 2 }) })).toBe(sections[1]);
  });

  it('keeps the visible link text in the accessible name, in English too (SC 2.5.3)', async () => {
    for (const lang of ['ja', 'en']) {
      await i18n.changeLanguage(lang);
      const { unmount } = render(<AutopilotDecisions artifacts={[artifact('a1', 'autopilot-tree-summary')]} nodes={[]} />);
      const link = screen.getByRole('link');
      expect(link.getAttribute('aria-label')).toContain(i18n.t('ticketItem.openInNewTab'));
      unmount();
    }
  });

  it('hides the family icons and labels the child list with its heading', () => {
    render(
      <TicketFamily
        parent={{ id: 'P', title: 'parent', status: 'IN PROGRESS' }}
        childTickets={[{ id: 'C', title: 'child', status: 'TODO' }]}
      />
    );
    const family = screen.getByTestId('ticket-family');
    for (const svg of family.querySelectorAll('svg')) {
      expect(svg).toHaveAttribute('aria-hidden', 'true');
    }
    expect(screen.getByRole('list', { name: i18n.t('ticketItem.family.children', { count: 1 }) })).toBe(
      screen.getByTestId('ticket-family-children')
    );
  });

  it('shows what the person is waited on for as text next to the buttons', () => {
    render(
      <AutopilotControls
        ticketId="T"
        status="IN PROGRESS"
        view={{ ...NO_AUTOPILOT, badges: ['awaitingHuman'], awaiting: '計画承認の判断待ち', blockedBy: { ticket: 'T', tree: 'T' } }}
      />
    );
    expect(screen.getByTestId('autopilot-awaiting')).toHaveTextContent(
      i18n.t('autopilot.badges.awaitingTitle', { what: '計画承認の判断待ち' })
    );
  });

  // DFLT-00147: the start confirmation is an in-app modal dialog.
  it('confirms a start in a labelled, described modal dialog that starts on cancel, in both languages', async () => {
    for (const lang of ['ja', 'en']) {
      await i18n.changeLanguage(lang);
      const user = userEvent.setup();
      const { unmount } = render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
      const button = screen.getByTestId('autopilot-start');
      expect(button).toHaveAccessibleName(i18n.t('autopilot.button'));
      await user.click(button);

      const dialog = screen.getByRole('dialog', { name: i18n.t('autopilot.confirm.title', { mode: i18n.t('autopilot.modes.tree') }) });
      expect(dialog).toHaveAttribute('aria-modal', 'true');
      expect(dialog).toHaveAccessibleDescription(i18n.t('autopilot.confirm.tree', { id: 'T' }));
      const cancel = within(dialog).getByRole('button', { name: i18n.t('autopilot.confirm.cancel') });
      expect(cancel).toHaveFocus();
      expect(within(dialog).getByRole('button', { name: i18n.t('autopilot.confirm.start') })).toBeInTheDocument();
      // The start button stays enabled while it is open (see
      // AutopilotControls); the overlay keeps it out of reach.
      expect(button).toBeEnabled();

      await user.keyboard('{Escape}');
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(button).toHaveFocus();
      unmount();
    }
  });

  it('keeps the untrusted-folder notice after the start message clears, in both languages', async () => {
    for (const lang of ['ja', 'en']) {
      await i18n.changeLanguage(lang);
      vi.useFakeTimers({ shouldAdvanceTime: true });
      // Restore the fake timers and the fetch mock even when an assertion
      // fails, so a failure here does not leak into the later tests.
      try {
        vi.spyOn(globalThis, 'fetch').mockResolvedValue(
          new Response(
            JSON.stringify({ run_id: 'run-1', mode: 'tree', root: 'T', state: 'starting', created: true, resumed: false, untrusted_folder: '/work/t' }),
            { status: 200, headers: { 'Content-Type': 'application/json' } }
          )
        );
        const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
        render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
        await user.click(screen.getByTestId('autopilot-start'));
        await user.click(screen.getByRole('button', { name: i18n.t('autopilot.confirm.start') }));
        const notice = await screen.findByTestId('autopilot-untrusted');
        expect(notice).toHaveTextContent(i18n.t('autopilot.untrustedFolder', { path: '/work/t' }));
        await act(async () => {
          vi.advanceTimersByTime(20000);
        });
        expect(screen.queryByTestId('autopilot-message')).not.toBeInTheDocument();
        expect(screen.getByTestId('autopilot-untrusted')).toBeInTheDocument();
      } finally {
        cleanup();
        vi.restoreAllMocks();
        vi.useRealTimers();
      }
    }
  });

  // DFLT-00149: the resume dialog's confirm button says "Resume", not "Start".
  it('labels the confirm button of the resume dialog with a resume word, in both languages', async () => {
    const expected: Record<string, { resume: string; start: string }> = {
      ja: { resume: '再開する', start: '起動する' },
      en: { resume: 'Resume', start: 'Start' }
    };
    for (const lang of ['ja', 'en']) {
      await i18n.changeLanguage(lang);
      const user = userEvent.setup();
      const { unmount } = render(
        <AutopilotControls
          ticketId="T"
          status="DONE"
          view={{ ...NO_AUTOPILOT, resumable: { ticket: false, tree: true } }}
        />
      );
      await user.click(screen.getByTestId('autopilot-start'));
      const dialog = screen.getByRole('dialog', {
        name: i18n.t('autopilot.confirm.resumeTitle', { mode: i18n.t('autopilot.modes.tree') })
      });
      expect(i18n.t('autopilot.confirm.resumeStart')).toBe(expected[lang].resume);
      expect(within(dialog).getByTestId('autopilot-confirm-confirm')).toHaveTextContent(expected[lang].resume);
      expect(within(dialog).getByRole('button', { name: expected[lang].resume })).toBeInTheDocument();
      expect(within(dialog).queryByRole('button', { name: expected[lang].start })).not.toBeInTheDocument();
      unmount();

      // A fresh start keeps its "Start" label.
      const fresh = render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
      await user.click(screen.getByTestId('autopilot-start'));
      const freshDialog = screen.getByRole('dialog', {
        name: i18n.t('autopilot.confirm.title', { mode: i18n.t('autopilot.modes.tree') })
      });
      expect(within(freshDialog).getByRole('button', { name: expected[lang].start })).toBeInTheDocument();
      fresh.unmount();
    }
  });

  // DFLT-00149: a refresh after the start that hangs or fails does not leave
  // the button in its "starting" state.
  for (const [label, onSettled, advance] of [
    ['never settles', () => new Promise<void>(() => {}), true],
    ['rejects', () => Promise.reject(new Error('refresh failed')), false]
  ] as const) {
    it(`re-enables the start button when the refresh after a start ${label}`, async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        vi.spyOn(console, 'error').mockImplementation(() => {});
        vi.spyOn(console, 'warn').mockImplementation(() => {});
        vi.spyOn(globalThis, 'fetch').mockResolvedValue(
          new Response(
            JSON.stringify({ run_id: 'run-1', mode: 'tree', root: 'T', state: 'starting', created: true, resumed: false }),
            { status: 200, headers: { 'Content-Type': 'application/json' } }
          )
        );
        const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
        render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} onSettled={onSettled} />);
        const button = screen.getByTestId('autopilot-start');
        await user.click(button);
        await user.click(screen.getByTestId('autopilot-confirm-confirm'));
        await screen.findByTestId('autopilot-message');
        if (advance) {
          // Still waiting on the refresh: the button is in "starting".
          expect(button).toBeDisabled();
          expect(button).toHaveAttribute('aria-busy', 'true');
          await act(async () => {
            vi.advanceTimersByTime(SETTLE_TIMEOUT_MS);
          });
        }
        await vi.waitFor(() => expect(button).toBeEnabled());
      } finally {
        cleanup();
        vi.restoreAllMocks();
        vi.useRealTimers();
      }
    });
  }

  // DFLT-00181: the run scope is chosen in the dialog.
  it('names the scope choices with their legend and describes a disabled one with its reason, in both languages', async () => {
    for (const lang of ['ja', 'en']) {
      await i18n.changeLanguage(lang);
      const user = userEvent.setup();
      const { unmount } = render(
        <AutopilotControls ticketId="T" status="IN PROGRESS" view={{ ...NO_AUTOPILOT, blockedBy: { ticket: '', tree: 'C' } }} />
      );
      const button = screen.getByTestId('autopilot-start');
      expect(button).toBeEnabled();
      expect(button).not.toHaveAttribute('aria-describedby');
      await user.click(button);
      const dialog = screen.getByRole('dialog');
      // The description is the message only, not the choices.
      expect(dialog).toHaveAccessibleDescription(i18n.t('autopilot.confirm.ticket', { id: 'T' }));
      const group = within(dialog).getByRole('group', { name: i18n.t('autopilot.confirm.modeLegend') });
      const tree = within(group).getByRole('radio', { name: i18n.t('autopilot.modeOptions.tree') });
      const single = within(group).getByRole('radio', { name: i18n.t('autopilot.modeOptions.ticket') });
      expect(tree).toBeDisabled();
      expect(tree).toHaveAccessibleDescription(i18n.t('autopilot.blockedDescendant', { root: 'C' }));
      expect(single).toBeEnabled();
      expect(single).toBeChecked();
      expect(single).not.toHaveAttribute('aria-describedby');
      unmount();
    }
  });

  it('wraps Tab through the scope choice, the model cap and the two buttons, inside the dialog', async () => {
    const user = userEvent.setup();
    render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
    await user.click(screen.getByTestId('autopilot-start'));
    const tree = screen.getByTestId('autopilot-mode-tree');
    // DFLT-00375: the model cap select follows the scope choice.
    const model = screen.getByTestId('autopilot-model-select');
    const cancel = screen.getByTestId('autopilot-confirm-cancel');
    const confirm = screen.getByTestId('autopilot-confirm-confirm');
    expect(cancel).toHaveFocus();
    await user.tab();
    expect(confirm).toHaveFocus();
    await user.tab();
    // The radio group is one Tab stop, on its checked radio.
    expect(tree).toHaveFocus();
    await user.tab();
    expect(model).toHaveFocus();
    await user.tab();
    expect(cancel).toHaveFocus();
    await user.tab({ shift: true });
    expect(model).toHaveFocus();
    await user.tab({ shift: true });
    expect(tree).toHaveFocus();
    await user.tab({ shift: true });
    expect(confirm).toHaveFocus();
  });

  // DFLT-00233: with a 150-200% default font size on a narrow screen the start
  // dialog, with its scope choice, is taller than the window; its overlay
  // scrolls so everything in it can be reached.
  it('puts the whole start dialog, scope choice included, in a vertically scrolling overlay', async () => {
    const user = userEvent.setup();
    render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
    await user.click(screen.getByTestId('autopilot-start'));
    const overlay = screen.getByTestId('autopilot-confirm-overlay');
    const dialog = screen.getByRole('dialog');
    expect(overlay).toHaveClass('overflow-y-auto');
    expect(overlay).not.toHaveClass('items-center');
    expect(dialog.parentElement).toBe(overlay);
    expect(dialog).toHaveClass('m-auto', 'min-w-0');
    expect(dialog).toContainElement(screen.getByRole('group', { name: i18n.t('autopilot.confirm.modeLegend') }));
    expect(dialog).toContainElement(screen.getByTestId('autopilot-confirm-cancel'));
    expect(dialog).toContainElement(screen.getByTestId('autopilot-confirm-confirm'));
  });

  it('switches the scope with the arrow keys', async () => {
    const user = userEvent.setup();
    render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
    await user.click(screen.getByTestId('autopilot-start'));
    const tree = screen.getByTestId('autopilot-mode-tree');
    tree.focus();
    await user.keyboard('{ArrowDown}');
    expect(screen.getByTestId('autopilot-mode-ticket')).toBeChecked();
    expect(screen.getByRole('dialog', { name: i18n.t('autopilot.confirm.title', { mode: i18n.t('autopilot.modes.ticket') }) })).toBeInTheDocument();
  });

  it('keeps the focus fallback of the controls out of the Tab order', async () => {
    const user = userEvent.setup();
    render(
      <AutopilotControls
        ticketId="T"
        status="TODO"
        view={NO_AUTOPILOT}
        actions={
          <>
            <button type="button">refine</button>
            <button type="button">run</button>
          </>
        }
      />
    );
    // DFLT-00218: the fallback is the autopilot column; the row around it
    // takes no focus at all.
    const fallback = screen.getByTestId('autopilot-focus-fallback');
    expect(fallback).toHaveAttribute('tabindex', '-1');
    expect(screen.getByTestId('autopilot-controls')).not.toHaveAttribute('tabindex');
    // Tab stops only on the three buttons.
    await user.tab();
    expect(screen.getByRole('button', { name: 'refine' })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('button', { name: 'run' })).toHaveFocus();
    await user.tab();
    expect(screen.getByTestId('autopilot-start')).toHaveFocus();
    await user.tab();
    expect(document.body).toHaveFocus();
  });

  // DFLT-00218: the regular actions share the row but stay outside the focus
  // fallback, so its ring covers the autopilot alone.
  it('lays the regular actions out in the row, outside the focus fallback', () => {
    render(
      <AutopilotControls
        ticketId="T"
        status="IN PROGRESS"
        view={{ ...NO_AUTOPILOT, blockedBy: { ticket: 'R', tree: 'R' } }}
        actions={<div data-testid="regular-actions"><button type="button">refine</button></div>}
      />
    );
    const row = screen.getByTestId('autopilot-controls');
    const fallback = screen.getByTestId('autopilot-focus-fallback');
    const actions = screen.getByTestId('regular-actions');
    expect(row).toContainElement(actions);
    expect(fallback).not.toContainElement(actions);
    // The fallback ends the row, in a layout-only slot (DFLT-00219).
    expect(row.lastElementChild).toBe(screen.getByTestId('autopilot-slot'));
    expect(row.lastElementChild).toContainElement(fallback);
    expect(row.lastElementChild).not.toHaveAttribute('tabindex');
    expect(fallback).toContainElement(screen.getByTestId('autopilot-start'));
    expect(fallback).toContainElement(screen.getByTestId('autopilot-disabled-reason'));
  });
});

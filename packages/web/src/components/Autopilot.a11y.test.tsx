// DFLT-00142 accessibility review (iteration 1): unique ids per section,
// label-in-name, decorative icons, and what a sighted keyboard user can read.
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { Artifact } from '../types';
import { AutopilotDecisions } from './AutopilotDecisions';
import { AutopilotControls } from './AutopilotControls';
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
});

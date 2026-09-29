// DFLT-00290: with a 32px default font in a 160px window (5rem), the ticket
// family card (parent / children) and the autopilot decisions card in the
// expanded details padded 1rem on each side, which left them about 12px of
// content width: the "Parent ticket" label, the linked ticket's status chip
// and the decision labels and dates ran 3-84px past the viewport and were cut
// off by the ticket card's clip. From 7.5rem down (upto-7_5rem:, index.css;
// 240px and below at a 32px default font, 120px and below at 16px) both
// cards pad 0.25rem, and a family link (id, title, status chip) may wrap onto
// several lines. Wider than that nothing changes. jsdom does no layout (nor
// media queries), so this checks the classes; the geometry was measured in a
// real browser (see the ticket's implementation notes).
import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { Artifact } from '../types';
import { AutopilotDecisions } from './AutopilotDecisions';
import { TicketFamily } from './TicketFamily';

const EXTREME = 'upto-7_5rem:';

const decision: Artifact = {
  id: 'a1',
  ticket_id: 'TEST-00290',
  node_id: 'TEST-00290-01',
  name: 'autopilot-decision-refine',
  type: 'text',
  content: '',
  created_at: '2026-01-01T00:00:00Z'
};

const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

afterEach(async () => {
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('ticket detail cards in a 7.5rem window (%s)', lng => {
  it('the ticket family card pads 0.25rem and its links may wrap only from 7.5rem down', async () => {
    await i18n.changeLanguage(lng);
    render(
      <TicketFamily
        parent={{ id: 'TEST-00001', title: 'Parent title', status: 'DONE' }}
        childTickets={[{ id: 'TEST-00002', title: 'Child title', status: 'TODO' }]}
      />
    );
    const card = screen.getByTestId('ticket-family');
    expect(card).toHaveClass('p-4', `${EXTREME}p-1`);
    expectNoneOf(card, ['p-1', 'upto-15rem:p-1']);
    for (const link of [screen.getByTestId('ticket-family-parent'), screen.getByTestId('ticket-family-child')]) {
      expect(link).toHaveClass('inline-flex', 'max-w-full', `${EXTREME}flex-wrap`);
      expectNoneOf(link, ['flex-wrap', 'upto-15rem:flex-wrap']);
    }
  });

  it('the autopilot decisions card pads 0.25rem only from 7.5rem down', async () => {
    await i18n.changeLanguage(lng);
    render(<AutopilotDecisions artifacts={[decision]} nodes={[{ id: 'TEST-00290-01', name: 'plan' }]} />);
    const card = screen.getByTestId('autopilot-decisions');
    expect(card).toHaveClass('p-4', `${EXTREME}p-1`);
    expectNoneOf(card, ['p-1', 'upto-15rem:p-1']);
  });
});

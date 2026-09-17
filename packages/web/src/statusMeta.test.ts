// DFLT-00086: the status filter's predicate, which moved out of App.tsx
// when all four toolbar filters were unified on "nothing selected = all".
import { describe, expect, it } from 'vitest';
import { TICKET_STATUSES } from './types';
import { getStatusMeta, matchesStatusFilter } from './statusMeta';

const tickets = [
  { id: 'A', status: 'TODO' },
  { id: 'B', status: 'IN PROGRESS' },
  { id: 'C', status: 'DONE' },
  { id: 'D', status: 'CLOSED' },
  // Written straight to the DB by something that doesn't validate statuses.
  { id: 'E', status: 'WHATEVER' }
];

const matching = (selected: typeof TICKET_STATUSES) =>
  tickets.filter(t => matchesStatusFilter(t.status, selected)).map(t => t.id);

describe('matchesStatusFilter', () => {
  it('passes every ticket when nothing is selected', () => {
    expect(matching([])).toEqual(['A', 'B', 'C', 'D', 'E']);
  });

  it('passes every ticket when every status is selected too', () => {
    expect(matching(TICKET_STATUSES)).toEqual(['A', 'B', 'C', 'D', 'E']);
  });

  it('narrows to one status', () => {
    expect(matching(['IN PROGRESS'])).toEqual(['B']);
  });

  it('matches several statuses with OR', () => {
    expect(matching(['DONE', 'CLOSED'])).toEqual(['C', 'D']);
  });

  it('filters an unexpected value under TODO, the status it is displayed as', () => {
    expect(getStatusMeta('WHATEVER')).toBe(getStatusMeta('TODO'));
    expect(matching(['TODO'])).toEqual(['A', 'E']);
    expect(matching(['DONE'])).toEqual(['C']);
  });
});

// DFLT-00086: the assignee filter's options and predicate, after it stopped
// being a single-choice radio group and gained an "unassigned" bucket.
import { describe, expect, it } from 'vitest';
import {
  UNASSIGNED_ASSIGNEE,
  assigneeFilterOptions,
  isUnassignedOption,
  matchesAssigneeFilter
} from './assigneeFilter';

const tickets = [
  { id: 'A', assignee: '佐藤' },
  { id: 'B', assignee: '佐藤' },
  { id: 'C', assignee: '鈴木' },
  { id: 'D', assignee: null },
  { id: 'E', assignee: '田中' }
];

const matching = (selected: string[]) =>
  tickets.filter(t => matchesAssigneeFilter(t.assignee, selected)).map(t => t.id);

describe('assigneeFilterOptions', () => {
  it('puts the unassigned bucket first, then distinct names in order', () => {
    expect(assigneeFilterOptions(tickets.map(t => t.assignee))).toEqual([
      UNASSIGNED_ASSIGNEE,
      '佐藤',
      '田中',
      '鈴木'
    ]);
  });

  it('offers the unassigned bucket even when every ticket is assigned', () => {
    expect(assigneeFilterOptions(['佐藤'])).toEqual([UNASSIGNED_ASSIGNEE, '佐藤']);
  });

  it('treats null, undefined and "" as unassigned rather than as names', () => {
    expect(assigneeFilterOptions([null, undefined, ''])).toEqual([UNASSIGNED_ASSIGNEE]);
  });

  it('never lists the sentinel twice, even if it somehow arrives as a name', () => {
    expect(assigneeFilterOptions([UNASSIGNED_ASSIGNEE, '佐藤'])).toEqual([UNASSIGNED_ASSIGNEE, '佐藤']);
  });

  it('recognizes only the bucket as the bucket', () => {
    expect(isUnassignedOption(UNASSIGNED_ASSIGNEE)).toBe(true);
    for (const name of ['', '佐藤', 'unassigned', '未割り当て']) {
      expect(isUnassignedOption(name)).toBe(false);
    }
  });
});

describe('matchesAssigneeFilter', () => {
  it('passes every ticket when nothing is selected', () => {
    expect(matching([])).toEqual(['A', 'B', 'C', 'D', 'E']);
  });

  it('narrows to one assignee', () => {
    expect(matching(['佐藤'])).toEqual(['A', 'B']);
  });

  it('matches several assignees with OR', () => {
    expect(matching(['佐藤', '鈴木'])).toEqual(['A', 'B', 'C']);
  });

  it('narrows to unassigned tickets only', () => {
    expect(matching([UNASSIGNED_ASSIGNEE])).toEqual(['D']);
  });

  it('combines the unassigned bucket with named assignees', () => {
    expect(matching([UNASSIGNED_ASSIGNEE, '田中'])).toEqual(['D', 'E']);
  });

  it.each([null, undefined, ''])('treats %j as unassigned, not as a name', value => {
    expect(matchesAssigneeFilter(value, [UNASSIGNED_ASSIGNEE])).toBe(true);
    expect(matchesAssigneeFilter(value, ['佐藤'])).toBe(false);
  });

  it('keeps narrowing to a name that is no longer among the options', () => {
    // A ticket reassigned away from 田中 leaves the filter alone; the list
    // just goes empty instead of silently widening.
    expect(matching(['ユーザ不在'])).toEqual([]);
  });
});

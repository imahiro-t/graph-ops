// DFLT-00084: label chips on the ticket row and in the expanded detail.
import { render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Label, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const mk = (id: string, name: string, color: Label['color']): Label => ({
  id,
  project_id: 'proj-1',
  name,
  color,
  created_at: '',
  updated_at: ''
});
const BUG = mk('label-bug', 'バグ', 'red');
const FEAT = mk('label-feat', '機能追加', 'blue');
const UI = mk('label-ui', 'UI', 'purple');
const PERF = mk('label-perf', '性能', 'amber');

const makeTicket = (labels: Label[] | undefined): TicketDetail => ({
  id: 'TEST-00001',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status: 'TODO',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels,
  nodes: [],
  edges: [],
  artifacts: []
});

function renderItem(labels: Label[] | undefined, isExpanded = false) {
  return render(
    <TicketItem
      ticket={makeTicket(labels)}
      isExpanded={isExpanded}
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[BUG, FEAT, UI, PERF]}
    />
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TicketItem labels', () => {
  it('shows colored chips next to the title in the row', () => {
    renderItem([BUG, UI]);

    const header = screen.getByTestId('ticket-header-labels');
    const chips = within(header).getAllByTestId('label-chip');
    expect(chips.map(c => c.textContent)).toEqual(['バグ', 'UI']);
    expect(chips[0]).toHaveAttribute('data-label-color', 'red');
    expect(chips[0].className).toContain('bg-red-100');
    expect(chips[1]).toHaveAttribute('data-label-color', 'purple');
    expect(chips[1].className).toContain('bg-purple-100');
    // The title comes before the chips in the row.
    const title = screen.getByText('タイトル');
    expect(title.compareDocumentPosition(header) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('folds the 4th label onwards into "+N" whose title lists every label', () => {
    renderItem([BUG, FEAT, UI, PERF]);

    const header = screen.getByTestId('ticket-header-labels');
    expect(within(header).getAllByTestId('label-chip')).toHaveLength(3);
    const more = within(header).getByTestId('ticket-header-labels-more');
    expect(more).toHaveTextContent(i18n.t('ticket.labels.more', { count: 1 }));
    expect(more).toHaveTextContent('+1');
    for (const name of ['バグ', '機能追加', 'UI', '性能']) {
      expect(more.getAttribute('title')).toContain(name);
    }
  });

  it('gives "+N" a screen-reader text naming the folded labels, hiding the bare "+N" from it', () => {
    renderItem([BUG, FEAT, UI, PERF]);

    const more = screen.getByTestId('ticket-header-labels-more');
    const visible = within(more).getByText('+1');
    expect(visible).toHaveAttribute('aria-hidden', 'true');
    const srText = within(more).getByText(i18n.t('ticket.labels.moreSr', { count: 1, names: '性能' }));
    expect(srText).toHaveClass('sr-only');
    expect(srText).toHaveTextContent('性能');
  });

  it('shows no chips or "+N" for a ticket without labels', () => {
    renderItem([]);
    expect(screen.queryByTestId('ticket-header-labels')).not.toBeInTheDocument();
    expect(screen.queryByTestId('label-chip')).not.toBeInTheDocument();
    expect(screen.queryByTestId('ticket-header-labels-more')).not.toBeInTheDocument();
  });

  it('treats a ticket without a labels key as unlabeled', () => {
    renderItem(undefined);
    expect(screen.queryByTestId('ticket-header-labels')).not.toBeInTheDocument();
  });

  it('shows every label in color in the expanded detail', () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
    renderItem([BUG, FEAT, UI, PERF], true);

    const detail = screen.getByTestId('ticket-detail-labels');
    const chips = within(detail).getAllByTestId('label-chip');
    expect(chips.map(c => c.textContent)).toEqual(['バグ', '機能追加', 'UI', '性能']);
    expect(chips.map(c => c.getAttribute('data-label-color'))).toEqual(['red', 'blue', 'purple', 'amber']);
    expect(within(detail).getByRole('button', { name: new RegExp(`^${i18n.t('ticket.labels.edit')}`) })).toBeInTheDocument();
  });
});

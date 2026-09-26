// DFLT-00166: every lucide icon TicketItem draws is decorative -- its meaning
// is carried by the text next to it or by the name of the control around it
// -- so each one is aria-hidden="true". The icon-only header buttons
// (close/reopen/delete) keep an accessible name from their title.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, GraphNode, TicketDetail, TicketStatus } from '../types';
import { TicketItem } from './TicketItem';

const TICKET_ID = 'TEST-00166';

const NODE: GraphNode = {
  id: `${TICKET_ID}-01`,
  ticket_id: TICKET_ID,
  name: '計画作成',
  type: 'plan',
  status: 'DONE',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
};

const art = (id: string, name: string, type: Artifact['type'], content: string): Artifact => ({
  id,
  ticket_id: TICKET_ID,
  node_id: NODE.id,
  name,
  type,
  content,
  created_at: '2026-01-01T00:00:00Z'
});

const makeTicket = (status: TicketStatus = 'IN PROGRESS'): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: 'アイコンのテスト',
  description: '説明',
  status,
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [NODE],
  edges: [],
  artifacts: [art('a-plan', '実行計画', 'text', '# 計画\n'), art('a-spec', '仕様', 'gherkin', 'Feature: 例\n')]
});

const renderItem = (status?: TicketStatus, isExpanded = true) =>
  render(
    <TicketItem
      ticket={makeTicket(status)}
      isExpanded={isExpanded}
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );

const expectAllIconsHidden = (root: Element) => {
  const icons = root.querySelectorAll('svg.lucide');
  expect(icons.length).toBeGreaterThan(0);
  icons.forEach(icon => expect(icon).toHaveAttribute('aria-hidden', 'true'));
};

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })));
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TicketItem icon accessibility', () => {
  it('hides every icon on each tab of the expanded ticket', () => {
    const { container } = renderItem();
    const tabs = [
      i18n.t('ticketItem.tabs.nodes', { count: 1 }),
      i18n.t('ticketItem.tabs.gherkin', { count: 1 }),
      i18n.t('ticketItem.tabs.html', { count: 0 }),
      i18n.t('ticketItem.tabs.artifacts', { count: 2 })
    ];
    for (const name of tabs) {
      // The tab buttons are named by their text; the icon in front adds nothing.
      const tab = screen.getByRole('button', { name });
      expectAllIconsHidden(tab);
      fireEvent.click(tab);
      expectAllIconsHidden(container);
    }
  });

  it('hides the node type badge icon (drawn from a variable) in the node list', () => {
    renderItem();
    // NodeTypeBadge renders meta.icon through a local variable, so it is not
    // found by searching for lucide imports; check it where it is shown.
    const label = screen.getAllByText(i18n.t('nodeType.plan'))[0];
    const badge = label.closest('span[title]')!;
    expect(badge).toHaveAttribute('title', i18n.t('nodeType.plan'));
    expectAllIconsHidden(badge);
  });

  it('names the icon-only close and delete buttons from their title', () => {
    renderItem('IN PROGRESS', false);
    const close = screen.getByRole('button', { name: i18n.t('ticketItem.close.button') });
    const del = screen.getByRole('button', { name: i18n.t('ticketItem.delete.button') });
    expectAllIconsHidden(close);
    expectAllIconsHidden(del);
  });

  it('names the icon-only reopen button of a closed ticket from its title', () => {
    const { container } = renderItem('CLOSED', false);
    const reopen = screen.getByRole('button', { name: i18n.t('ticketItem.reopen.button') });
    expectAllIconsHidden(reopen);
    expectAllIconsHidden(container);
    expect(within(container).queryByRole('button', { name: i18n.t('ticketItem.close.button') })).toBeNull();
  });
});

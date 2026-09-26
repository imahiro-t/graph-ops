// DFLT-00166: every lucide icon TicketItem draws is decorative -- its meaning
// is carried by the text next to it or by the name of the control around it
// -- so each one is aria-hidden="true". The icon-only header buttons
// (close/reopen) are named through aria-label; the delete button is named
// after its ticket (DFLT-00193). DFLT-00171: none of them carries a title
// any more; the name (or a shorter tooltip) is shown by IconButton on hover
// and keyboard focus.
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, GraphNode, TicketDetail, TicketStatus } from '../types';
import { TicketItem } from './TicketItem';
import { openIconButtonTooltip, openIconButtonTooltips } from '../test/iconButtonTooltip';

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

  it('names the icon-only close button from its title and the delete button after its ticket', () => {
    renderItem('IN PROGRESS', false);
    const close = screen.getByRole('button', { name: i18n.t('ticketItem.close.button') });
    const del = screen.getByRole('button', {
      name: i18n.t('ticketItem.delete.ariaLabel', { id: TICKET_ID, title: 'アイコンのテスト' })
    });
    expectAllIconsHidden(close);
    expectAllIconsHidden(del);
  });

  // DFLT-00193: after a delete, focus can land on another card's delete
  // button, so its name carries the ticket's ID and title -- in the
  // "<action>: <target>" form LabelsEditor uses -- while the tooltip stays.
  it.each([
    ['ja', `チケットを削除: ${TICKET_ID} アイコンのテスト`, 'チケットを削除'],
    ['en', `Delete ticket: ${TICKET_ID} アイコンのテスト`, 'Delete ticket']
  ])('names the delete button with the ticket ID and title in %s, keeping the tooltip', async (lng, name, tooltip) => {
    await i18n.changeLanguage(lng);
    try {
      const { container } = renderItem('IN PROGRESS', false);
      const del = screen.getByRole('button', { name });
      expect(del).toBe(container.querySelector(`[data-focus-key="ticket-delete-${TICKET_ID}"]`));
      expect(del).not.toHaveAttribute('title');
      act(() => del.focus());
      expect(openIconButtonTooltip()).toHaveTextContent(new RegExp(`^${tooltip}$`));
    } finally {
      await i18n.changeLanguage('ja');
    }
  });

  it('names the icon-only reopen button of a closed ticket from its title', () => {
    const { container } = renderItem('CLOSED', false);
    const reopen = screen.getByRole('button', { name: i18n.t('ticketItem.reopen.button') });
    expectAllIconsHidden(reopen);
    expectAllIconsHidden(container);
    expect(within(container).queryByRole('button', { name: i18n.t('ticketItem.close.button') })).toBeNull();
  });
});

// DFLT-00171: the icon-only buttons of the ticket header row.
describe('TicketItem icon buttons (IconButton)', () => {
  const renderRow = (overrides: Partial<TicketDetail> = {}, myName = '') => {
    const onToggleExpand = vi.fn();
    const utils = render(
      <TicketItem
        ticket={{ ...makeTicket(), ...overrides }}
        isExpanded={false}
        onToggleExpand={onToggleExpand}
        onRefresh={vi.fn()}
        myName={myName}
        projectLabels={[]}
      />
    );
    return { ...utils, onToggleExpand };
  };

  it('names close, delete, copy ID and unassign through aria-label, with no title', () => {
    renderRow({ assignee: 'me' }, 'me');
    const names = [
      i18n.t('ticketItem.close.button'),
      i18n.t('ticketItem.delete.ariaLabel', { id: TICKET_ID, title: 'アイコンのテスト' }),
      i18n.t('ticketItem.copyId.button', { id: TICKET_ID }),
      i18n.t('ticketItem.selfAssign.unassign')
    ];
    for (const name of names) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveAttribute('aria-label', name);
      expect(button).not.toHaveAttribute('title');
    }
  });

  it('names the reopen button of a closed ticket through aria-label, with no title', () => {
    renderRow({ status: 'CLOSED' });
    const reopen = screen.getByRole('button', { name: i18n.t('ticketItem.reopen.button') });
    expect(reopen).toHaveAttribute('aria-label', i18n.t('ticketItem.reopen.button'));
    expect(reopen).not.toHaveAttribute('title');
  });

  it('shows the tooltip of the delete button on keyboard focus', async () => {
    const user = userEvent.setup();
    renderRow();
    const del = screen.getByRole('button', { name: i18n.t('ticketItem.delete.ariaLabel', { id: TICKET_ID, title: 'アイコンのテスト' }) });
    // Tab until the delete button has focus (the row has a few tab stops).
    for (let i = 0; i < 20 && document.activeElement !== del; i++) await user.tab();
    expect(del).toHaveFocus();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toBeVisible();
    expect(tooltip).toHaveTextContent(new RegExp(`^${i18n.t('ticketItem.delete.button')}$`));
    expect(tooltip).toHaveAttribute('aria-hidden', 'true');
  });

  it('does not toggle the row when the tooltip is clicked', async () => {
    const user = userEvent.setup();
    const { onToggleExpand } = renderRow();
    const del = screen.getByRole('button', { name: i18n.t('ticketItem.delete.ariaLabel', { id: TICKET_ID, title: 'アイコンのテスト' }) });
    await user.hover(del);
    await user.click(openIconButtonTooltip());
    expect(onToggleExpand).not.toHaveBeenCalled();
    // The tooltip is still up, and nothing else opened.
    expect(openIconButtonTooltips()).toHaveLength(1);
  });
});

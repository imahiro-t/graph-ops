// DFLT-00243: the artifact panel's four tabs form a WAI-ARIA tab pattern
// (tablist / tab / tabpanel with aria-selected) so screen reader users hear
// that they are tabs and which one is selected (WCAG 1.3.1, 4.1.2). The
// keyboard follows the APG tabs pattern with automatic activation: only the
// selected tab is in the Tab order, Left/Right move the selection (wrapping
// at the ends), Home/End jump to the first/last tab. The tab panel is
// focusable so an empty tab's panel can still be reached by keyboard.
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const artifact = (ticketId: string, id: string): Artifact => ({
  id,
  ticket_id: ticketId,
  node_id: 'n1',
  name: 'text artifact',
  type: 'text',
  content: '',
  created_at: '2026-01-01T00:00:00Z'
});

const makeTicket = (id: string): TicketDetail => ({
  id,
  project_id: 'proj-1',
  title: `タイトル ${id}`,
  description: '説明',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  // One text artifact: the Gherkin and HTML tabs are empty, and the
  // "download all" link is shown.
  artifacts: [artifact(id, `${id}-a1`)]
});

const item = (id: string) => (
  <TicketItem
    key={id}
    ticket={makeTicket(id)}
    isExpanded
    onToggleExpand={vi.fn()}
    onRefresh={vi.fn(async () => {})}
    myName=""
    projectLabels={[]}
  />
);

const renderTicket = () => render(item('TEST-00243'));

const tabLabels = () => [
  i18n.t('ticketItem.tabs.nodes', { count: 0 }),
  i18n.t('ticketItem.tabs.gherkin', { count: 0 }),
  i18n.t('ticketItem.tabs.html', { count: 0 }),
  i18n.t('ticketItem.tabs.artifacts', { count: 1 })
];

const getTabList = () => screen.getByRole('tablist', { name: i18n.t('ticketItem.tabListLabel') });
const getTabs = () => within(getTabList()).getAllByRole('tab');

const expectSelected = (index: number) => {
  const tabs = getTabs();
  tabs.forEach((tab, i) => {
    expect(tab).toHaveAttribute('aria-selected', i === index ? 'true' : 'false');
    expect(tab).toHaveAttribute('tabindex', i === index ? '0' : '-1');
  });
  const panel = screen.getByRole('tabpanel');
  expect(panel).toHaveAttribute('aria-labelledby', tabs[index].id);
  expect(panel).toHaveAccessibleName(tabLabels()[index]);
};

beforeEach(async () => {
  await i18n.changeLanguage('en');
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

describe('TicketItem artifact tabs ARIA structure', () => {
  it('names the tablist and puts the four tabs in it', () => {
    renderTicket();
    const tablist = getTabList();
    expect(tablist).toHaveAccessibleName('Nodes and artifacts');
    const tabs = getTabs();
    expect(tabs).toHaveLength(4);
    tabs.forEach((tab, i) => expect(tab).toHaveAccessibleName(tabLabels()[i]));
  });

  it('names the tablist in Japanese too', async () => {
    await i18n.changeLanguage('ja');
    renderTicket();
    expect(screen.getByRole('tablist', { name: 'ノードと成果物' })).toBeInTheDocument();
  });

  it('selects only the first tab initially and ties every tab to the panel', () => {
    renderTicket();
    expectSelected(0);
    const tabs = getTabs();
    const panel = screen.getByRole('tabpanel');
    expect(panel.id).not.toBe('');
    const ids = tabs.map(tab => tab.id);
    expect(ids.every(id => id !== '')).toBe(true);
    expect(new Set(ids).size).toBe(4);
    for (const tab of tabs) expect(tab).toHaveAttribute('aria-controls', panel.id);
  });

  it('moves aria-selected and aria-labelledby with a click', async () => {
    const user = userEvent.setup();
    renderTicket();
    await user.click(getTabs()[2]);
    expectSelected(2);
    expect(screen.getByRole('tabpanel')).toHaveTextContent(i18n.t('ticketItem.noHtmlYet'));
  });

  it('keeps the download link outside the tablist but inside the tab row', () => {
    renderTicket();
    const link = screen.getByRole('link', { name: i18n.t('ticketItem.downloadAllArtifacts') });
    expect(getTabList().contains(link)).toBe(false);
    expect(screen.getByTestId('ticket-artifact-tabs').contains(link)).toBe(true);
  });

  it('gives each expanded ticket its own tab and panel ids', () => {
    render(
      <>
        {item('TEST-00001')}
        {item('TEST-00002')}
      </>
    );
    const ids = [
      ...screen.getAllByRole('tab').map(tab => tab.id),
      ...screen.getAllByRole('tabpanel').map(panel => panel.id)
    ];
    expect(ids).toHaveLength(10);
    expect(new Set(ids).size).toBe(10);
  });
});

describe('TicketItem artifact tabs keyboard', () => {
  it('moves the selection and focus with the arrow keys, wrapping at the ends', async () => {
    const user = userEvent.setup();
    renderTicket();
    // Focus the selected tab directly so the test does not depend on the
    // controls before it in the Tab order.
    getTabs()[0].focus();

    await user.keyboard('{ArrowRight}');
    expectSelected(1);
    expect(getTabs()[1]).toHaveFocus();
    expect(screen.getByRole('tabpanel')).toHaveTextContent(i18n.t('ticketItem.noGherkinYet'));

    await user.keyboard('{ArrowLeft}');
    expectSelected(0);
    expect(getTabs()[0]).toHaveFocus();

    await user.keyboard('{ArrowLeft}');
    expectSelected(3);
    expect(getTabs()[3]).toHaveFocus();

    await user.keyboard('{ArrowRight}');
    expectSelected(0);
    expect(getTabs()[0]).toHaveFocus();
  });

  it('jumps to the first and last tab with Home and End', async () => {
    const user = userEvent.setup();
    renderTicket();
    await user.click(getTabs()[1]);

    await user.keyboard('{End}');
    expectSelected(3);
    expect(getTabs()[3]).toHaveFocus();

    await user.keyboard('{Home}');
    expectSelected(0);
    expect(getTabs()[0]).toHaveFocus();
  });

  it('leaves arrow keys with a modifier alone', async () => {
    const user = userEvent.setup();
    renderTicket();
    getTabs()[0].focus();
    for (const modifier of ['Control', 'Alt', 'Meta']) {
      await user.keyboard(`{${modifier}>}{ArrowRight}{/${modifier}}`);
      expectSelected(0);
    }
  });

  it('keeps only the selected tab in the Tab order and lets Tab reach an empty panel', async () => {
    const user = userEvent.setup();
    renderTicket();
    await user.click(getTabs()[1]); // Gherkin: empty, nothing focusable inside
    expect(getTabs()[1]).toHaveFocus();
    // Tab skips the unselected tabs: next comes the "download all" link,
    // which follows the tablist in the row, then the panel.
    await user.tab();
    expect(screen.getByRole('link', { name: i18n.t('ticketItem.downloadAllArtifacts') })).toHaveFocus();
    await user.tab();
    const panel = screen.getByRole('tabpanel');
    expect(panel).toHaveFocus();
    expect(panel).toHaveTextContent(i18n.t('ticketItem.noGherkinYet'));
    await user.tab({ shift: true });
    await user.tab({ shift: true });
    expect(getTabs()[1]).toHaveFocus();
  });

  it('shows the panel ring only on keyboard focus', () => {
    renderTicket();
    const panel = screen.getByRole('tabpanel');
    expect(panel).toHaveAttribute('tabindex', '0');
    expect(panel).toHaveClass('focus-visible:ring-2', 'focus-visible:ring-inset', 'focus:outline-hidden');
    // Each class on its own: no ring or border that shows without focus.
    for (const cls of ['ring-2', 'ring-inset', 'border', 'outline-solid']) expect(panel).not.toHaveClass(cls);
    // The panel keeps its existing layout classes.
    expect(panel).toHaveClass('p-4', 'flex-1', 'min-h-0', 'overflow-y-auto');
  });
});

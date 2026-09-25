// DFLT-00142: a ticket's parent and children in the expanded ticket detail,
// and following those links -- which expands the target, moves to its page
// and clears filters that hide it. fetch is served by test/fakeBackend.ts.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

let backend: FakeBackend;

function seed(paginationPageSize = 10) {
  backend = createFakeBackend({
    projects: [alpha],
    currentProjectId: alpha.id,
    labels: [],
    paginationPageSize,
    tickets: [
      { id: 'ALP-00001', project_id: alpha.id, title: '親チケット P', status: 'IN PROGRESS', priority: 'HIGH', labelIds: [] },
      { id: 'ALP-00002', project_id: alpha.id, title: '無関係', status: 'TODO', priority: 'MEDIUM', labelIds: [] },
      { id: 'ALP-00003', project_id: alpha.id, title: '子 C1', status: 'TODO', priority: 'LOW', labelIds: [], parentId: 'ALP-00001' },
      { id: 'ALP-00004', project_id: alpha.id, title: '子 C2', status: 'DONE', priority: 'LOW', labelIds: [], parentId: 'ALP-00001' }
    ]
  });
  installFakeBackend(backend);
}

const card = (id: string) => document.getElementById(`ticket-${id}`) as HTMLElement;

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('ALP-00001');
  return user;
}

async function expand(user: ReturnType<typeof userEvent.setup>, id: string) {
  await user.click(within(card(id)).getByTestId('ticket-header-row'));
  return within(card(id)).findByTestId('ticket-family');
}

describe('ticket parent/children in the Web UI', () => {
  beforeEach(async () => {
    // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
    await i18n.changeLanguage('ja');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('lists the children with their status and navigates child -> parent', async () => {
    seed();
    const user = await renderApp();

    const family = await expand(user, 'ALP-00001');
    expect(within(family).getByText(i18n.t('ticketItem.family.children', { count: 2 }))).toBeInTheDocument();
    expect(within(family).queryByText(i18n.t('ticketItem.family.parent'))).not.toBeInTheDocument();
    const children = within(family).getAllByTestId('ticket-family-child');
    expect(children.map(c => c.textContent)).toEqual([
      expect.stringContaining('ALP-00003'),
      expect.stringContaining('ALP-00004')
    ]);
    expect(children[0]).toHaveTextContent('子 C1');
    expect(children[0]).toHaveTextContent(i18n.t('status.todo'));
    expect(children[1]).toHaveTextContent(i18n.t('status.done'));

    await user.click(children[0]);
    const childFamily = await within(card('ALP-00003')).findByTestId('ticket-family');
    const parentLink = within(childFamily).getByTestId('ticket-family-parent');
    expect(parentLink).toHaveTextContent('ALP-00001');
    expect(parentLink).toHaveTextContent('親チケット P');
    expect(within(childFamily).getByText(i18n.t('ticketItem.family.parent'))).toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toBe(card('ALP-00003')));

    await user.click(parentLink);
    await waitFor(() => expect(document.activeElement).toBe(card('ALP-00001')));
    // The parent stays expanded (following a link never collapses).
    expect(within(card('ALP-00001')).getByTestId('ticket-family')).toBeInTheDocument();
  });

  it('shows no family section for a ticket that has neither parent nor children', async () => {
    seed();
    const user = await renderApp();
    await user.click(within(card('ALP-00002')).getByTestId('ticket-header-row'));
    // Wait for the detail to land, then check nothing was rendered.
    await within(card('ALP-00002')).findByText(i18n.t('ticketItem.description.title'));
    expect(within(card('ALP-00002')).queryByTestId('ticket-family')).not.toBeInTheDocument();
  });

  it('moves to the page of the opened ticket', async () => {
    seed(2);
    const user = await renderApp();
    // Page 1 holds ALP-00001/00002; the children are on page 2.
    expect(card('ALP-00003')).toBeNull();
    const family = await expand(user, 'ALP-00001');
    await user.click(within(family).getAllByTestId('ticket-family-child')[1]);
    await waitFor(() => expect(card('ALP-00004')).not.toBeNull());
    expect(await within(card('ALP-00004')).findByTestId('ticket-family-parent')).toHaveTextContent('ALP-00001');
  });

  it('clears the filters that would hide the opened ticket', async () => {
    seed();
    const user = await renderApp();
    const family = await expand(user, 'ALP-00001');
    const search = screen.getByPlaceholderText(i18n.t('toolbar.searchPlaceholder'));
    await user.type(search, '親チケット');
    expect(card('ALP-00003')).toBeNull();

    await user.click(within(family).getAllByTestId('ticket-family-child')[0]);
    await waitFor(() => expect(card('ALP-00003')).not.toBeNull());
    expect(search).toHaveValue('');
  });

  it('renders the headings in English too', async () => {
    await i18n.changeLanguage('en');
    seed();
    const user = await renderApp();
    const family = await expand(user, 'ALP-00001');
    expect(within(family).getByText('Child tickets (2)')).toBeInTheDocument();
    await user.click(within(family).getAllByTestId('ticket-family-child')[0]);
    const childFamily = await within(card('ALP-00003')).findByTestId('ticket-family');
    expect(within(childFamily).getByText('Parent ticket')).toBeInTheDocument();
    expect(childFamily.textContent).not.toMatch(/ticketItem\./);
  });
});

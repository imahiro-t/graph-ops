// DFLT-00375: the model cap chosen at launch -- beside the process-ticket
// button (sent to /api/claude/launch as `model` and as `--model` in the
// prompt) and in the autopilot start dialog (sent to the autopilot start,
// "not specified" as inherit). Refine and the free prompt row never send one.
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';
import { AutopilotControls } from './AutopilotControls';
import { NO_AUTOPILOT } from '../lib/autopilotApi';

const TICKET_ID = 'DFLT-00001';

const makeTicket = (): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: 'タイトル',
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
  artifacts: []
});

const renderTicket = () =>
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );

let fetchMock: ReturnType<typeof vi.fn>;

const bodiesTo = (path: string) =>
  fetchMock.mock.calls
    .filter(([url]) => String(url) === path)
    .map(([, init]) => JSON.parse(String((init as RequestInit).body)) as Record<string, unknown>);

beforeEach(async () => {
  await i18n.changeLanguage('ja');
  fetchMock = vi.fn((url: RequestInfo | URL) => {
    if (String(url).endsWith('/autopilot')) {
      return Promise.resolve(
        new Response(JSON.stringify({ run_id: 'run-1', mode: 'tree', root: TICKET_ID, state: 'starting', created: true, resumed: false }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' }
        })
      );
    }
    return Promise.resolve(new Response('{"started":true}', { status: 200, headers: { 'Content-Type': 'application/json' } }));
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

const processSelect = () => screen.getByTestId('process-ticket-model') as HTMLSelectElement;
const runButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.actions.run') });

describe('process-ticket model cap', () => {
  it.each([
    ['', `/graph-ops:process-ticket ${TICKET_ID}`, undefined],
    ['opus', `/graph-ops:process-ticket ${TICKET_ID} --model opus`, 'opus'],
    ['sonnet', `/graph-ops:process-ticket ${TICKET_ID} --model sonnet`, 'sonnet'],
    ['haiku', `/graph-ops:process-ticket ${TICKET_ID} --model haiku`, 'haiku']
  ])('choosing %j sends the prompt and model accordingly', async (choice, prompt, model) => {
    renderTicket();
    fireEvent.change(processSelect(), { target: { value: choice } });
    fireEvent.click(runButton());
    await vi.waitFor(() => expect(bodiesTo('/api/claude/launch')).toHaveLength(1));
    const body = bodiesTo('/api/claude/launch')[0];
    expect(body.prompt).toBe(prompt);
    if (model === undefined) {
      expect(body).not.toHaveProperty('model');
    } else {
      expect(body.model).toBe(model);
    }
  });

  it('never sends a model from refine or the prompt row', async () => {
    renderTicket();
    fireEvent.change(processSelect(), { target: { value: 'haiku' } });
    fireEvent.click(screen.getByRole('button', { name: i18n.t('ticketItem.actions.refine') }));
    await vi.waitFor(() => expect(bodiesTo('/api/claude/launch')).toHaveLength(1));
    fireEvent.change(screen.getByRole('textbox', { name: i18n.t('ticketItem.promptLabel') }), { target: { value: 'hello' } });
    fireEvent.click(screen.getByRole('button', { name: i18n.t('ticketItem.send') }));
    await vi.waitFor(() => expect(bodiesTo('/api/claude/launch')).toHaveLength(2));
    for (const body of bodiesTo('/api/claude/launch')) {
      expect(body).not.toHaveProperty('model');
      expect(String(body.prompt)).not.toContain('--model');
    }
  });

  it.each(['ja', 'en'] as const)('is a labelled, described native select with the four choices (%s)', async lng => {
    await i18n.changeLanguage(lng);
    renderTicket();
    const select = screen.getByRole('combobox', { name: i18n.t('modelCap.label') });
    expect(select).toBe(processSelect());
    expect(select.tagName).toBe('SELECT');
    expect(select).toHaveAccessibleDescription(i18n.t('modelCap.help'));
    expect(screen.getByText(i18n.t('modelCap.help'))).toBeVisible();
    expect(within(select).getAllByRole('option').map(o => o.textContent)).toEqual([
      i18n.t('modelCap.options.inherit'),
      'Opus',
      'Sonnet',
      'Haiku'
    ]);
    // Sits in the same row as the run button, and may wrap within it.
    expect(runButton().parentElement).toContainElement(select);
    expect(runButton().parentElement).toHaveClass('flex-wrap');
    expect(select).toHaveClass('max-w-full', 'min-w-0');
  });

  it('is reached with Tab right after the run button', async () => {
    const user = userEvent.setup();
    renderTicket();
    runButton().focus();
    await user.tab();
    // A native select: once focused, the keyboard changes it like any other.
    expect(processSelect()).toHaveFocus();
  });
});

describe('autopilot model cap', () => {
  const openDialog = async (user: ReturnType<typeof userEvent.setup>) => {
    await user.click(screen.getByTestId('autopilot-start'));
    return screen.getByTestId('autopilot-confirm');
  };

  it('sends the chosen model with the start', async () => {
    const user = userEvent.setup();
    render(<AutopilotControls ticketId={TICKET_ID} status="TODO" view={NO_AUTOPILOT} />);
    const dialog = await openDialog(user);
    const select = within(dialog).getByRole('combobox', { name: i18n.t('modelCap.label') });
    expect(select).toHaveAccessibleDescription(i18n.t('modelCap.help'));
    await user.selectOptions(select, 'sonnet');
    await user.click(screen.getByTestId('autopilot-confirm-confirm'));
    await vi.waitFor(() => expect(bodiesTo(`/api/tickets/${TICKET_ID}/autopilot`)).toHaveLength(1));
    expect(bodiesTo(`/api/tickets/${TICKET_ID}/autopilot`)[0]).toEqual({ mode: 'tree', model: 'sonnet' });
  });

  it('sends "not specified" as inherit, and resets the choice when the dialog opens again', async () => {
    const user = userEvent.setup();
    render(<AutopilotControls ticketId={TICKET_ID} status="TODO" view={NO_AUTOPILOT} />);
    let dialog = await openDialog(user);
    await user.selectOptions(within(dialog).getByTestId('autopilot-model-select'), 'haiku');
    await user.click(screen.getByTestId('autopilot-confirm-cancel'));
    dialog = await openDialog(user);
    const select = within(dialog).getByTestId('autopilot-model-select') as HTMLSelectElement;
    expect(select.value).toBe('');
    await user.click(screen.getByTestId('autopilot-confirm-confirm'));
    await vi.waitFor(() => expect(bodiesTo(`/api/tickets/${TICKET_ID}/autopilot`)).toHaveLength(1));
    expect(bodiesTo(`/api/tickets/${TICKET_ID}/autopilot`)[0]).toEqual({ mode: 'tree', model: 'inherit' });
  });

  it('places the choice under the scope radios in the dialog', async () => {
    const user = userEvent.setup();
    render(<AutopilotControls ticketId={TICKET_ID} status="TODO" view={NO_AUTOPILOT} />);
    const dialog = await openDialog(user);
    const scope = within(dialog).getByTestId('autopilot-mode');
    const model = within(dialog).getByTestId('autopilot-model');
    expect(scope.compareDocumentPosition(model) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

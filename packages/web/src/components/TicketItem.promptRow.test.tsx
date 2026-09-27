// DFLT-00226: the Action Footer's prompt row (the textarea and its Send
// button) stays inside the footer card with large text (200%) on a narrow
// screen (375px). Below sm the row may wrap, putting the Send button on a
// line of its own under the textarea, and the button may put its icon on a
// line of its own and break its label anywhere; the textarea may shrink
// below its intrinsic (cols) width. At 100% text and from sm up nothing
// changes: every layout class added here is either limited to max-sm: or has
// no effect while the row fits. jsdom does no layout, so this checks the
// classes; the widths themselves were measured in a real browser (see the
// ticket's implementation notes). It also checks that typing, sending (button
// and shortcut), the submitting state and the accessible names still work.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';
import { submittingName } from '../test/submittingName';

const TICKET_ID = 'TEST-00226';

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

// The launch request is held until the test settles it; everything else
// answers at once.
let settleLaunch: (res: Response) => void = () => {};
let fetchMock: ReturnType<typeof vi.fn>;

const launchCalls = () =>
  fetchMock.mock.calls.filter(([url]) => String(url) === '/api/claude/launch');

beforeEach(() => {
  fetchMock = vi.fn((url: RequestInfo | URL) => {
    if (String(url) === '/api/claude/launch') {
      return new Promise<Response>(resolve => {
        settleLaunch = resolve;
      });
    }
    return Promise.resolve(new Response('[]', { status: 200 }));
  });
  vi.stubGlobal('fetch', fetchMock);
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

const promptBox = () => screen.getByRole('textbox', { name: i18n.t('ticketItem.promptLabel') });
const sendButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.send') });

describe.each(['ja', 'en'] as const)('TicketItem prompt row layout (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('lets the row wrap below sm only', () => {
    renderTicket();
    const row = promptBox().parentElement!;
    expect(row).toBe(sendButton().parentElement);
    expect(row).toHaveClass('flex', 'max-sm:flex-wrap', 'gap-2');
    expect(row).not.toHaveClass('flex-wrap');
    expect(row).not.toHaveClass('sm:flex-wrap');
  });

  it('lets the textarea shrink and gives it a basis that wraps the row only with large text below sm', () => {
    renderTicket();
    const textarea = promptBox();
    expect(textarea).toHaveClass('flex-1', 'min-w-0', 'max-sm:basis-32');
    // An unconditional basis would change the layout from sm up.
    expect(textarea).not.toHaveClass('basis-32');
  });

  it('keeps the Send button within the row and lets it wrap its contents below sm only', () => {
    renderTicket();
    const button = sendButton();
    expect(button).toHaveClass(
      'flex',
      'max-w-full',
      'px-4',
      'max-sm:px-3',
      'max-sm:ml-auto',
      'max-sm:flex-wrap',
      'max-sm:[overflow-wrap:anywhere]'
    );
    // Each unconditional form is checked on its own, so one slipping in is
    // caught even if the others are absent.
    expect(button).not.toHaveClass('flex-wrap');
    expect(button).not.toHaveClass('sm:flex-wrap');
    expect(button).not.toHaveClass('[overflow-wrap:anywhere]');
    expect(button).not.toHaveClass('break-words');
    expect(button).not.toHaveClass('break-all');
    expect(button).not.toHaveClass('ml-auto');
    expect(button).not.toHaveClass('px-3');

    const icon = button.querySelector('svg');
    expect(icon).not.toBeNull();
    expect(icon).toHaveClass('shrink-0');
    expect(icon).toHaveAttribute('aria-hidden', 'true');
  });

  it('keeps the Send button disabled until text is typed', () => {
    renderTicket();
    expect(sendButton()).toBeDisabled();
    fireEvent.change(promptBox(), { target: { value: '続きを進めて' } });
    expect(sendButton()).toBeEnabled();
    expect(promptBox()).toHaveValue('続きを進めて');
  });

  it('sends with the button, is busy while sending, and clears the text on success', async () => {
    renderTicket();
    fireEvent.change(promptBox(), { target: { value: '続きを進めて' } });
    const button = sendButton();
    fireEvent.click(button);

    expect(launchCalls()).toHaveLength(1);
    expect(JSON.parse(String((launchCalls()[0][1] as RequestInit).body))).toMatchObject({
      prompt: '続きを進めて',
      ticketId: TICKET_ID
    });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button).toHaveAccessibleName(submittingName(i18n.t('ticketItem.send')));
    expect(button.querySelector('svg')).toHaveClass('shrink-0', 'animate-spin');

    await act(async () => {
      settleLaunch(new Response('{}', { status: 200 }));
    });
    expect(button).not.toHaveAttribute('aria-busy');
    expect(button).toHaveAccessibleName(i18n.t('ticketItem.send'));
    expect(promptBox()).toHaveValue('');
  });

  it('sends with the submit shortcut from the textarea', async () => {
    renderTicket();
    fireEvent.change(promptBox(), { target: { value: '続きを進めて' } });
    fireEvent.keyDown(promptBox(), { key: 'Enter', ctrlKey: true });
    expect(launchCalls()).toHaveLength(1);

    await act(async () => {
      settleLaunch(new Response('{}', { status: 200 }));
    });
  });
});

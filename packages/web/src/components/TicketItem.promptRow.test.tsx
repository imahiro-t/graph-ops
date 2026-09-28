// DFLT-00226: the Action Footer's prompt row (the textarea and its Send
// button) stays inside the footer card with large text (200%) on a narrow
// screen (375px). Below sm the row may wrap, putting the Send button on a
// line of its own under the textarea, and the button may put its icon on a
// line of its own and break its label anywhere; the textarea may shrink
// below its intrinsic (cols) width, and its small basis keeps the row on one
// line at 100% text down to 320px. Once wrapped, the button keeps some
// vertical padding instead of shrinking to its icon's height. At 100% text
// and from sm up the layout does not change: every layout class added here is
// either limited to max-sm: or has no effect while the row fits. jsdom does no layout, so this checks the
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
    // 6rem: at 100% text a 320px screen still fits the textarea and the
    // button on one line, while 200% text on a 375px screen wraps.
    expect(textarea).toHaveClass('flex-1', 'min-w-0', 'max-sm:basis-24');
    // An unconditional basis would change the layout from sm up, and the
    // earlier 8rem basis wrapped the row at 100% text on a 320px screen.
    expect(textarea).not.toHaveClass('basis-24');
    expect(textarea).not.toHaveClass('max-sm:basis-32');
  });

  it('keeps the Send button within the row, padded, and lets it wrap its contents below sm only', () => {
    renderTicket();
    const button = sendButton();
    expect(button).toHaveClass(
      'flex',
      'max-w-full',
      'px-4',
      'max-sm:px-3',
      'max-sm:py-2',
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
    expect(button).not.toHaveClass('py-2');

    const icon = button.querySelector('svg');
    expect(icon).not.toBeNull();
    expect(icon).toHaveClass('shrink-0');
    expect(icon).toHaveAttribute('aria-hidden', 'true');
  });

  // DFLT-00252: keyboard focus shows as a ring (like the buttons around them),
  // not only as the textarea's border colour. The Send button's ring sits off
  // it by the footer's background (white / slate-900) so it stands out from
  // the indigo fill. jsdom applies no :focus-visible styles, so this checks
  // the classes; the rings were checked in a real browser.
  it('shows a focus-visible ring on the Send button', () => {
    renderTicket();
    expect(sendButton()).toHaveClass(
      'focus:outline-none',
      'focus-visible:ring-2',
      'focus-visible:ring-blue-500',
      'dark:focus-visible:ring-blue-400',
      'focus-visible:ring-offset-2',
      'dark:focus-visible:ring-offset-slate-900'
    );
    // A ring on any focus (mouse clicks too) would change the pointer look.
    expect(sendButton()).not.toHaveClass('focus:ring-2');
  });

  it('shows a focus-visible ring on the textarea besides its border colour', () => {
    renderTicket();
    expect(promptBox()).toHaveClass(
      'focus:outline-none',
      'focus:border-indigo-500',
      'focus-visible:ring-2',
      'focus-visible:ring-blue-500',
      'dark:focus-visible:ring-blue-400'
    );
  });

  // DFLT-00259: in dark mode dark:border-slate-700 won over
  // focus:border-indigo-500, so the border stayed slate-700 on focus. With
  // dark:focus:border-indigo-500 the focused border is indigo-500
  // (rgb(99, 102, 241)) in both themes, on a click and on Tab, and goes back
  // to slate-700 (dark) / slate-300 (light) on blur -- measured in a real
  // browser, since jsdom resolves neither variants nor the cascade.
  it('turns the textarea border indigo on focus in dark mode as in light mode', () => {
    renderTicket();
    const textarea = promptBox();
    expect(textarea).toHaveClass('focus:border-indigo-500', 'dark:focus:border-indigo-500');
    // The resting borders and the focus-visible ring stay as they were.
    expect(textarea).toHaveClass('border-slate-300', 'dark:border-slate-700');
    expect(textarea).toHaveClass('focus-visible:ring-2', 'focus-visible:ring-blue-500', 'dark:focus-visible:ring-blue-400');
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

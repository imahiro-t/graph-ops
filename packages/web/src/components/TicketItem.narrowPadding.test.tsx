// DFLT-00227: with a 200% default font size on a 320px screen, the rem
// padding of the expanded details (p-6) and of the Action Footer card (p-4),
// nested inside the page's own, left the action row about 60px -- narrower
// than one of its buttons or the "Actions:" label, so they ran past the card.
// Both cards pad with p-3 instead while the viewport is under 15rem wide. A
// rem media query follows the browser's default font size alone: at 100% it
// matches only below 240px, at 150% below 360px and at 200% below 480px, so
// the default size and sm+ keep p-6 / p-4 and look as before. jsdom does no
// layout (nor media queries), so this checks the classes; the widths
// themselves were measured in a real browser, and the build output was
// checked for the generated rule (see the ticket's implementation notes).
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const NARROW_LARGE_TEXT_PADDING = '[@media(max-width:15rem)]:p-3';

const makeTicket = (): TicketDetail => ({
  id: 'TEST-00227',
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

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
const expectNoneOf = (el: HTMLElement, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

describe.each(['ja', 'en'] as const)('TicketItem padding with large text on a narrow screen (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('marks the expanded details and the Action Footer that hold the action row', () => {
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    const footer = screen.getByTestId('ticket-action-footer');
    expect(details).toContainElement(footer);
    // The footer is the card that holds the action row and the prompt row.
    expect(within(footer).getByTestId('autopilot-controls')).toBeInTheDocument();
    expect(within(footer).getByRole('textbox', { name: i18n.t('ticketItem.promptLabel') })).toBeInTheDocument();
    expect(within(footer).getByRole('button', { name: i18n.t('ticketItem.actions.refine') })).toBeInTheDocument();
  });

  it('pads the expanded details less only with large text on a narrow screen', () => {
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    expect(details).toHaveClass('p-6', NARROW_LARGE_TEXT_PADDING);
    // Unconditional or px-based forms would change the default size or sm+.
    expectNoneOf(details, ['p-3', 'p-2', 'p-4', 'max-sm:p-3', 'sm:p-6', 'sm:p-3']);
  });

  it('pads the Action Footer less only with large text on a narrow screen', () => {
    renderTicket();
    const footer = screen.getByTestId('ticket-action-footer');
    expect(footer).toHaveClass('p-4', NARROW_LARGE_TEXT_PADDING);
    expectNoneOf(footer, ['p-3', 'p-2', 'p-6', 'max-sm:p-3', 'sm:p-4', 'sm:p-3']);
  });
});

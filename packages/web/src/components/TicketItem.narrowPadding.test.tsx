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
//
// DFLT-00252: both also pad with p-3 below sm (640px, a px query), whatever
// the text size: a 200% root font size set on the page itself leaves the rem
// query at 240px, and at 320/360px the Send button's label broke onto several
// lines. The 15rem query stays, since with a very large default font (over
// about 267%) it matches above 640px too. From sm up at a normal default font
// size neither query matches, so sm+ keeps p-6 / p-4.
//
// DFLT-00259: in a window of 200 CSS px or less (160px at a 200% text size)
// both pad with p-2, which gives the untrusted-folder notice in the autopilot
// column room for its dismiss button (it had 0px of content width and the
// button ran 31px past it; see AutopilotControls.remText.test.tsx). A px
// query, not the 15rem one, which with a 32px default font also matches
// 320-336px; there, and at 100% from 320px up, the padding measured the same
// as before. In a real browser p-2 wins over max-sm:p-3 and the 15rem p-3
// (computed padding 16px at a 32px root).
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const NARROW_LARGE_TEXT_PADDING = 'upto-15rem:p-3';
const BELOW_SM_PADDING = 'max-sm:p-3';
const TINY_WINDOW_PADDING = '[@media(max-width:200px)]:p-2';

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

  it('pads the expanded details less only below sm or with large text on a narrow screen', () => {
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    expect(details).toHaveClass('p-6', BELOW_SM_PADDING, NARROW_LARGE_TEXT_PADDING, TINY_WINDOW_PADDING);
    // Unconditional or sm+ forms would change the look from sm up.
    expectNoneOf(details, ['p-3', 'p-2', 'p-4', 'sm:p-6', 'sm:p-3', 'max-sm:p-2', 'upto-15rem:p-2']);
  });

  it('pads the Action Footer less only below sm or with large text on a narrow screen', () => {
    renderTicket();
    const footer = screen.getByTestId('ticket-action-footer');
    expect(footer).toHaveClass('p-4', BELOW_SM_PADDING, NARROW_LARGE_TEXT_PADDING, TINY_WINDOW_PADDING);
    expectNoneOf(footer, ['p-3', 'p-2', 'p-6', 'sm:p-4', 'sm:p-3', 'max-sm:p-2', 'upto-15rem:p-2']);
  });
});

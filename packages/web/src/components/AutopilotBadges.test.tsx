// DFLT-00232: the autopilot badges in a ticket's header row. Their text was
// text-[11px], a size that ignores the browser's default font size (WCAG
// 1.4.4); it is 0.6875rem now -- 11px at the default 16px, so the default
// size looks as before, and larger with a larger default. The icons were
// already rem-sized (w-3 h-3 is 0.75rem) and grow with the text. The badges
// wrap, onto the next line and within a badge wider than its line, instead
// of running past the ticket card (whose overflow-clip hid them), and each
// badge is relative so that the sr-only text of "waiting for a person" is
// positioned inside it: without that it escaped the card's clip and made the
// page scroll sideways. jsdom does no layout, so this checks the classes;
// the page's scrollWidth, the badges' boxes and font sizes at 100-200% and
// 320-1280px were measured in a real browser, and the build output was
// checked for the generated rules (see the ticket's implementation notes).
import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { AutopilotBadge, NO_AUTOPILOT, TicketAutopilotView } from '../lib/autopilotApi';
import { AutopilotBadges } from './AutopilotBadges';

const ALL_BADGES: AutopilotBadge[] = ['running', 'processing', 'awaitingHuman', 'waiting'];

const view = (badges: AutopilotBadge[], awaiting = ''): TicketAutopilotView => ({
  ...NO_AUTOPILOT,
  badges,
  awaiting
});

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

afterEach(async () => {
  cleanup();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('AutopilotBadges (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('sizes every badge in rem, not px', () => {
    render(<AutopilotBadges view={view(ALL_BADGES, 'release approval')} />);
    for (const b of ALL_BADGES) {
      const badge = screen.getByTestId(`autopilot-badge-${b}`);
      expect(badge).toHaveClass('text-[0.6875rem]');
      expect(badge).not.toHaveClass('text-[11px]');
    }
  });

  it('sizes the icons in rem so they grow with the text', () => {
    render(<AutopilotBadges view={view(ALL_BADGES, 'release approval')} />);
    for (const b of ALL_BADGES) {
      const icon = screen.getByTestId(`autopilot-badge-${b}`).querySelector('svg');
      expect(icon).not.toBeNull();
      expect(icon).toHaveClass('w-3', 'h-3', 'shrink-0');
      expect(icon).toHaveAttribute('aria-hidden', 'true');
      expectNoneOf(icon!, ['w-[12px]', 'h-[12px]']);
    }
  });

  it('lets the badges wrap onto the next line instead of running past the card', () => {
    render(<AutopilotBadges view={view(ALL_BADGES, 'release approval')} />);
    const group = screen.getByTestId('autopilot-badges');
    expect(group).toHaveClass('flex', 'flex-wrap', 'min-w-0', 'max-w-full');
    expect(group).not.toHaveClass('shrink-0');
  });

  it('lets a badge wrap its own text when it is wider than its line', () => {
    render(<AutopilotBadges view={view(ALL_BADGES, 'release approval')} />);
    for (const b of ALL_BADGES) {
      const badge = screen.getByTestId(`autopilot-badge-${b}`);
      expect(badge).toHaveClass('min-w-0', 'break-words', '[overflow-wrap:anywhere]');
      expectNoneOf(badge, ['whitespace-nowrap', 'truncate', 'overflow-hidden', 'shrink-0']);
    }
  });

  it('keeps the "waiting for a person" title and its sr-only text, positioned inside the badge', () => {
    render(<AutopilotBadges view={view(['running', 'awaitingHuman'], 'release approval')} />);
    const badge = screen.getByTestId('autopilot-badge-awaitingHuman');
    const full = i18n.t('autopilot.badges.awaitingTitle', { what: 'release approval' });
    expect(badge).toHaveTextContent(i18n.t('autopilot.badges.awaitingHuman'));
    expect(badge).toHaveAttribute('title', full);
    const sr = within(badge).getByText(full);
    expect(sr).toHaveClass('sr-only');
    // The sr-only text is absolutely positioned: the badge must be its
    // containing block, or the card's overflow-clip does not clip it.
    expect(badge).toHaveClass('relative');
  });

  it('shows each badge with its text', () => {
    render(<AutopilotBadges view={view(ALL_BADGES)} />);
    for (const b of ALL_BADGES) {
      expect(screen.getByTestId(`autopilot-badge-${b}`)).toHaveTextContent(i18n.t(`autopilot.badges.${b}`));
    }
  });
});

it('renders nothing for a ticket no active run owns', () => {
  render(<AutopilotBadges view={NO_AUTOPILOT} />);
  expect(screen.queryByTestId('autopilot-badges')).not.toBeInTheDocument();
});

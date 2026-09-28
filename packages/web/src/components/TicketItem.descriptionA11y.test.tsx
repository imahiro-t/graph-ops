// DFLT-00262: the description card of an expanded ticket, for keyboard and
// assistive technology users.
// - The "Show all / Collapse" button tells whether the body is open
//   (aria-expanded), points at the body (aria-controls) and shows the same
//   focus-visible ring as the other buttons (WCAG 4.1.2 / 2.4.7).
// - While collapsed, the body is a max-h-56 scroll box, so it is a named,
//   focusable region with a focus ring that a keyboard can reach and scroll
//   (WCAG 2.1.1). Expanded, it does not scroll, so it is neither a tab stop
//   nor a landmark.
// - The refined time is text-[0.625rem] (10px at a 16px root) rather than
//   text-[10px], so it follows the browser's default font size (WCAG 1.4.4).
// jsdom does no layout or scrolling, so this checks attributes, classes and
// where the focus goes.
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatDateTime } from '../i18n/formatDate';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const REFINED_AT = '2026-01-02T03:04:05Z';

const makeTicket = (description = '説明の本文'): TicketDetail => ({
  id: 'TEST-00262',
  project_id: 'proj-1',
  title: 'タイトル',
  description,
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  refined_at: REFINED_AT,
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  artifacts: []
});

const renderExpanded = (description?: string) =>
  render(
    <TicketItem
      ticket={makeTicket(description)}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );

const FOCUS_RING = ['focus:outline-none', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'dark:focus-visible:ring-blue-400'];

const expandButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.description.expand') });
const collapseButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.description.collapse') });
const bodyRegionName = () => i18n.t('ticketItem.description.bodyRegion');

// The element the button's aria-controls points at.
const controlledBody = (button: HTMLElement) => {
  const id = button.getAttribute('aria-controls');
  expect(id).toBeTruthy();
  const body = document.getElementById(id as string);
  expect(body).not.toBeNull();
  return body as HTMLElement;
};

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('TicketItem description card accessibility (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('tells the collapsed state with aria-expanded and points at the body with aria-controls', () => {
    renderExpanded();
    const button = expandButton();
    expect(button).toHaveAttribute('aria-expanded', 'false');
    const body = controlledBody(button);
    expect(body).toHaveTextContent('説明の本文');
    expect(body).toContainElement(screen.getByTestId('markdown-viewer'));
  });

  it('gives the expand button the same focus-visible ring as the other buttons', () => {
    renderExpanded();
    expect(expandButton()).toHaveClass('rounded', ...FOCUS_RING);
  });

  it('makes the collapsed body a named, focusable scroll region with a focus ring', () => {
    renderExpanded();
    const region = screen.getByRole('region', { name: bodyRegionName() });
    expect(region).toBe(controlledBody(expandButton()));
    expect(region).toHaveAttribute('tabindex', '0');
    expect(region.tabIndex).toBe(0);
    expect(region).toHaveClass('break-words', 'max-h-56', 'overflow-y-auto', 'rounded-lg', ...FOCUS_RING);
  });

  it('lets the keyboard reach the collapsed body as the tab stop right after the expand button', async () => {
    const user = userEvent.setup();
    renderExpanded();
    const button = expandButton();
    const region = screen.getByRole('region', { name: bodyRegionName() });
    button.focus();
    expect(button).toHaveFocus();
    await user.tab();
    expect(region).toHaveFocus();
    expect(document.activeElement).toBe(region);
  });

  it('drops the tab stop and the region while expanded and restores them when collapsed again', () => {
    renderExpanded();
    const body = controlledBody(expandButton());

    fireEvent.click(expandButton());
    const collapse = collapseButton();
    expect(collapse).toHaveAttribute('aria-expanded', 'true');
    expect(controlledBody(collapse)).toBe(body);
    expect(body).not.toHaveAttribute('tabindex');
    expect(body).not.toHaveAttribute('role');
    expect(body).not.toHaveAttribute('aria-label');
    expect(screen.queryByRole('region', { name: bodyRegionName() })).toBeNull();
    expect(body).toHaveClass('break-words');
    expect(body).not.toHaveClass('max-h-56');
    expect(body).not.toHaveClass('overflow-y-auto');
    expect(body).not.toHaveClass('focus-visible:ring-2');

    fireEvent.click(collapse);
    const expand = expandButton();
    expect(expand).toHaveAttribute('aria-expanded', 'false');
    expect(controlledBody(expand)).toBe(body);
    expect(screen.getByRole('region', { name: bodyRegionName() })).toBe(body);
    expect(body).toHaveAttribute('tabindex', '0');
    expect(body).toHaveClass('max-h-56', 'overflow-y-auto', ...FOCUS_RING);
  });

  it('sizes the refined time in rem so it follows the default font size', () => {
    renderExpanded();
    const refinedText = screen.getByText(
      i18n.t('ticketItem.description.refinedAt', { time: formatDateTime(REFINED_AT, i18n.language) })
    );
    const refined = refinedText.parentElement as HTMLElement;
    expect(refined).toHaveClass('text-[0.625rem]');
    expect(refined).not.toHaveClass('text-[10px]');
  });

  it('shows only the placeholder, with no expand button or body region, when the description is empty', () => {
    renderExpanded('');
    expect(screen.getByText(i18n.t('ticketItem.description.empty'))).toBeVisible();
    expect(screen.queryByRole('button', { name: i18n.t('ticketItem.description.expand') })).toBeNull();
    expect(screen.queryByRole('region', { name: bodyRegionName() })).toBeNull();
  });
});

// DFLT-00262 / DFLT-00272: the description card of an expanded ticket, for
// keyboard and assistive technology users.
// - The "Full text" (ja「全文」) button keeps the same label open or closed
//   and tells whether the body is open with aria-expanded alone, as the APG
//   disclosure pattern recommends; a chevron hidden from assistive tech
//   turns (rotate-180) to show the state visually. It points at the body
//   (aria-controls) and shows the same focus-visible ring as the other
//   buttons (WCAG 4.1.2 / 2.4.7).
// - While collapsed, the body is a max-h-56 scroll box. Only when its text
//   actually overflows (scrollHeight > clientHeight) is it a named, focusable
//   region with a focus ring that a keyboard can reach and scroll (WCAG
//   2.1.1); a short body that fits adds no tab stop. A ResizeObserver on the
//   box and its content keeps this in step with the width and the text.
//   Expanded, it does not scroll, so it is neither a tab stop nor a landmark.
// - DFLT-00288: the button is shown only while the collapsed text overflows
//   or while the body is expanded (to collapse it again); a description that
//   fits while collapsed has no button, so no control that changes nothing
//   and no extra tab stop. While the button has focus it stays even if the
//   text starts to fit, so the focus never drops to <body> (a blur caused by
//   the page itself losing focus is ignored); it goes once the focus moves
//   away. The focus state is cleared whenever the button leaves the DOM
//   (description emptied, ticket closed), so it never keeps a later button.
// - The refined time is text-[0.625rem] (10px at a 16px root) rather than
//   text-[10px], so it follows the browser's default font size (WCAG 1.4.4).
// jsdom does no layout or scrolling, so scrollHeight / clientHeight are
// mocked, and this checks attributes, classes and where the focus goes.
import { act, fireEvent, render, screen } from '@testing-library/react';
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

const itemElement = (description?: string, isExpanded = true) => (
  <TicketItem
    ticket={makeTicket(description)}
    isExpanded={isExpanded}
    onToggleExpand={vi.fn()}
    onRefresh={vi.fn(async () => {})}
    myName=""
    projectLabels={[]}
  />
);

const renderExpanded = (description?: string) => render(itemElement(description));

const FOCUS_RING = ['focus:outline-hidden', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'dark:focus-visible:ring-blue-400'];

const fullTextButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.description.fullText') });
const queryFullTextButton = () => screen.queryByRole('button', { name: i18n.t('ticketItem.description.fullText') });
// The description body, found without the button (which a body that fits
// does not have): the box around the MarkdownViewer root.
const descriptionBody = () => screen.getByTestId('markdown-viewer').parentElement as HTMLElement;
const bodyRegionName = () => i18n.t('ticketItem.description.bodyRegion');

// The element the button's aria-controls points at.
const controlledBody = (button: HTMLElement) => {
  const id = button.getAttribute('aria-controls');
  expect(id).toBeTruthy();
  const body = document.getElementById(id as string);
  expect(body).not.toBeNull();
  return body as HTMLElement;
};

// jsdom reports 0 for both, so a body "fits" unless a test says otherwise.
// TicketItem reads scrollHeight / clientHeight only on the description body
// (the graph's box uses the widths), so a prototype getter is safe here.
const layout = { scrollHeight: 0, clientHeight: 0 };
const overflow = () => {
  layout.scrollHeight = 600;
  layout.clientHeight = 224;
};
const fit = () => {
  layout.scrollHeight = 100;
  layout.clientHeight = 100;
};

// A ResizeObserver stub that remembers its callbacks and observed elements,
// so a test can fire a resize after changing the mocked layout.
type ObserverRecord = { callback: ResizeObserverCallback; targets: Element[]; disconnected: boolean };
let observers: ObserverRecord[] = [];
const fireResize = () =>
  act(() => {
    for (const record of observers) {
      if (!record.disconnected) record.callback([], {} as ResizeObserver);
    }
  });

beforeEach(() => {
  layout.scrollHeight = 0;
  layout.clientHeight = 0;
  observers = [];
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  vi.stubGlobal(
    'ResizeObserver',
    class {
      private record: ObserverRecord;
      constructor(callback: ResizeObserverCallback) {
        this.record = { callback, targets: [], disconnected: false };
        observers.push(this.record);
      }
      observe(target: Element) {
        this.record.targets.push(target);
      }
      unobserve() {}
      disconnect() {
        this.record.disconnected = true;
      }
    }
  );
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => layout.scrollHeight);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(() => layout.clientHeight);
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

const expectNotRegion = (body: HTMLElement) => {
  expect(body).not.toHaveAttribute('tabindex');
  expect(body).not.toHaveAttribute('role');
  expect(body).not.toHaveAttribute('aria-label');
  expect(screen.queryByRole('region', { name: bodyRegionName() })).toBeNull();
};

describe.each(['ja', 'en'] as const)('TicketItem description card accessibility (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('tells the collapsed state with aria-expanded and points at the body with aria-controls', () => {
    overflow();
    renderExpanded();
    const button = fullTextButton();
    expect(button).toHaveAttribute('aria-expanded', 'false');
    const body = controlledBody(button);
    expect(body).toHaveTextContent('説明の本文');
    expect(body).toContainElement(screen.getByTestId('markdown-viewer'));
  });

  it('keeps the button label fixed and changes only aria-expanded when toggled', () => {
    overflow();
    renderExpanded();
    const button = fullTextButton();
    const label = lng === 'ja' ? '全文' : 'Full text';
    expect(i18n.t('ticketItem.description.fullText')).toBe(label);
    expect(button).toHaveAccessibleName(label);
    expect(button).toHaveTextContent(label, { normalizeWhitespace: true });
    expect(button.textContent).toBe(label);
    expect(button).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(button);
    expect(fullTextButton()).toBe(button);
    expect(button).toHaveAccessibleName(label);
    expect(button.textContent).toBe(label);
    expect(button).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(button);
    expect(button).toHaveAccessibleName(label);
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('shows the state with a chevron hidden from assistive tech that turns when expanded', () => {
    overflow();
    renderExpanded();
    const button = fullTextButton();
    const icons = button.querySelectorAll('svg');
    expect(icons).toHaveLength(1);
    const chevron = icons[0];
    expect(chevron).toHaveAttribute('aria-hidden', 'true');
    expect(chevron).toHaveClass('shrink-0', 'motion-safe:transition-transform');
    expect(chevron).not.toHaveClass('rotate-180');

    fireEvent.click(button);
    expect(chevron).toHaveClass('rotate-180');

    fireEvent.click(button);
    expect(chevron).not.toHaveClass('rotate-180');
  });

  it('gives the button the same focus-visible ring as the other buttons', () => {
    overflow();
    renderExpanded();
    expect(fullTextButton()).toHaveClass('rounded-sm', 'text-[0.6875rem]', ...FOCUS_RING);
  });

  it('makes an overflowing collapsed body a named, focusable scroll region with a focus ring', () => {
    overflow();
    renderExpanded();
    const region = screen.getByRole('region', { name: bodyRegionName() });
    expect(region).toBe(controlledBody(fullTextButton()));
    expect(region).toHaveAttribute('tabindex', '0');
    expect(region.tabIndex).toBe(0);
    expect(region).toHaveClass('wrap-break-word', 'max-h-56', 'overflow-y-auto', 'rounded-lg', ...FOCUS_RING);
  });

  it('lets the keyboard reach an overflowing collapsed body as the tab stop right after the button', async () => {
    overflow();
    const user = userEvent.setup();
    renderExpanded();
    const button = fullTextButton();
    const region = screen.getByRole('region', { name: bodyRegionName() });
    button.focus();
    expect(button).toHaveFocus();
    await user.tab();
    expect(region).toHaveFocus();
    expect(document.activeElement).toBe(region);
  });

  it.each([
    ['jsdom default (0 / 0)', () => {}],
    ['equal heights (100 / 100)', fit]
  ])('shows no button and adds no tab stop or region for a collapsed body that fits: %s', async (_label, setLayout) => {
    setLayout();
    const user = userEvent.setup();
    renderExpanded();
    expect(queryFullTextButton()).toBeNull();
    const body = descriptionBody();
    expect(body).toHaveAttribute('id');
    expect(body).toHaveTextContent('説明の本文');
    expect(body).toHaveClass('max-h-56', 'overflow-y-auto');
    expectNotRegion(body);

    await user.tab();
    await user.tab();
    await user.tab();
    expect(body).not.toHaveFocus();
    expect(body.contains(document.activeElement)).toBe(false);
  });

  it('becomes a region when the body starts to overflow, and stops being one when it fits again', () => {
    fit();
    renderExpanded();
    const body = descriptionBody();
    expectNotRegion(body);
    expect(queryFullTextButton()).toBeNull();

    overflow();
    fireResize();
    expect(screen.getByRole('region', { name: bodyRegionName() })).toBe(body);
    expect(body).toHaveAttribute('tabindex', '0');
    expect(controlledBody(fullTextButton())).toBe(body);

    fit();
    fireResize();
    expectNotRegion(body);
    expect(queryFullTextButton()).toBeNull();
  });

  it('observes both the body box and its content, and disconnects on unmount', () => {
    overflow();
    const { unmount } = renderExpanded();
    const body = controlledBody(fullTextButton());
    const viewer = screen.getByTestId('markdown-viewer');
    const record = observers.find(r => r.targets.includes(body));
    expect(record).toBeDefined();
    expect(record?.targets).toContain(viewer);
    expect(record?.disconnected).toBe(false);

    unmount();
    expect(record?.disconnected).toBe(true);
  });

  it('drops the tab stop and the region while expanded, even when overflowing, and restores them when collapsed again', () => {
    overflow();
    renderExpanded();
    const button = fullTextButton();
    const body = controlledBody(button);
    expect(screen.getByRole('region', { name: bodyRegionName() })).toBe(body);

    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(controlledBody(button)).toBe(body);
    expectNotRegion(body);
    expect(body).toHaveClass('wrap-break-word');
    expect(body).not.toHaveClass('max-h-56');
    expect(body).not.toHaveClass('overflow-y-auto');
    expect(body).not.toHaveClass('focus-visible:ring-2');

    fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'false');
    expect(controlledBody(button)).toBe(body);
    expect(screen.getByRole('region', { name: bodyRegionName() })).toBe(body);
    expect(body).toHaveAttribute('tabindex', '0');
    expect(body).toHaveClass('max-h-56', 'overflow-y-auto', ...FOCUS_RING);
  });

  it('shows the button for an overflowing collapsed body, collapsed and pointing at the body', () => {
    overflow();
    renderExpanded();
    const button = fullTextButton();
    expect(button).toHaveAttribute('aria-expanded', 'false');
    expect(controlledBody(button)).toBe(descriptionBody());
    expect(screen.getByRole('region', { name: bodyRegionName() })).toBe(descriptionBody());
  });

  it('keeps the button while expanded even when the body stops overflowing, and collapses it again', async () => {
    overflow();
    const user = userEvent.setup();
    renderExpanded();
    await user.click(fullTextButton());
    expect(fullTextButton()).toHaveAttribute('aria-expanded', 'true');

    fit();
    fireResize();
    const button = fullTextButton();
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expectNotRegion(descriptionBody());

    await user.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('neither measures nor observes while expanded, and measures again right after collapsing', () => {
    overflow();
    renderExpanded();
    const body = descriptionBody();
    fireEvent.click(fullTextButton());
    expect(observers.some(r => !r.disconnected && r.targets.includes(body))).toBe(false);

    // A transient "fits" while expanded is not recorded ...
    fit();
    fireResize();
    expect(fullTextButton()).toHaveAttribute('aria-expanded', 'true');
    expectNotRegion(body);

    // ... and collapsing (without moving the focus) measures again.
    overflow();
    fireEvent.click(fullTextButton());
    const button = fullTextButton();
    expect(button).toHaveAttribute('aria-expanded', 'false');
    const region = screen.getByRole('region', { name: bodyRegionName() });
    expect(region).toBe(body);
    expect(region).toHaveAttribute('tabindex', '0');
    expect(region).toHaveAttribute('aria-label', bodyRegionName());
  });

  it('drops the button and the region right after collapsing, without focus, a body that fits now', () => {
    overflow();
    renderExpanded();
    fireEvent.click(fullTextButton());
    expect(document.activeElement).toBe(document.body);

    fit();
    expect(fullTextButton()).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(fullTextButton());
    expect(queryFullTextButton()).toBeNull();
    const body = descriptionBody();
    expect(body).toHaveAttribute('id');
    expectNotRegion(body);
  });

  it('follows the window width: the button appears and goes as the collapsed body overflows and fits', () => {
    fit();
    renderExpanded();
    expect(queryFullTextButton()).toBeNull();

    overflow();
    fireResize();
    expect(fullTextButton()).toBeInTheDocument();

    fit();
    fireResize();
    expect(queryFullTextButton()).toBeNull();
  });

  it('follows a change of the description', () => {
    fit();
    const { rerender } = render(itemElement('短い説明'));
    expect(queryFullTextButton()).toBeNull();

    overflow();
    rerender(itemElement('長い説明\n\n'.repeat(40)));
    expect(fullTextButton()).toHaveAttribute('aria-expanded', 'false');

    fit();
    rerender(itemElement('短い説明'));
    expect(queryFullTextButton()).toBeNull();
  });

  it('keeps a focused button when the body starts to fit, and drops it once the focus moves away', async () => {
    overflow();
    const user = userEvent.setup();
    renderExpanded();
    const button = fullTextButton();
    act(() => button.focus());
    expect(button).toHaveFocus();

    fit();
    fireResize();
    expect(fullTextButton()).toBe(button);
    expect(document.activeElement).toBe(button);
    expect(document.activeElement).not.toBe(document.body);

    await user.tab();
    expect(queryFullTextButton()).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
  });

  it('keeps the focused button that collapsed a body that fits now, until the focus moves away', async () => {
    overflow();
    const user = userEvent.setup();
    renderExpanded();
    await user.click(fullTextButton());
    fit();

    const button = fullTextButton();
    await user.click(button);
    expect(fullTextButton()).toBe(button);
    expect(button).toHaveAttribute('aria-expanded', 'false');
    expect(document.activeElement).toBe(button);
    expectNotRegion(descriptionBody());

    await user.tab();
    expect(queryFullTextButton()).toBeNull();
  });

  it('ignores a blur caused by the page itself losing focus', () => {
    overflow();
    renderExpanded();
    const button = fullTextButton();
    act(() => button.focus());
    fit();
    fireResize();

    const hasFocus = vi.spyOn(document, 'hasFocus').mockReturnValue(false);
    fireEvent.blur(button);
    expect(fullTextButton()).toBe(button);

    hasFocus.mockRestore();
    fireEvent.blur(button);
    expect(queryFullTextButton()).toBeNull();
  });

  it('does not carry the focus state over when the ticket is closed and opened again', () => {
    overflow();
    const { rerender } = render(itemElement());
    act(() => fullTextButton().focus());
    expect(fullTextButton()).toHaveFocus();

    rerender(itemElement(undefined, false));
    expect(queryFullTextButton()).toBeNull();

    fit();
    rerender(itemElement(undefined, true));
    expect(queryFullTextButton()).toBeNull();
    expectNotRegion(descriptionBody());
  });

  it('does not carry the focus state over when the description is emptied and refilled', () => {
    overflow();
    const { rerender } = render(itemElement());
    act(() => fullTextButton().focus());

    rerender(itemElement(''));
    expect(queryFullTextButton()).toBeNull();

    fit();
    rerender(itemElement('短い説明'));
    expect(queryFullTextButton()).toBeNull();
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

  it('shows only the placeholder, with no button or body region, when the description is empty', () => {
    overflow();
    renderExpanded('');
    expect(screen.getByText(i18n.t('ticketItem.description.empty'))).toBeVisible();
    expect(screen.queryByRole('button', { name: i18n.t('ticketItem.description.fullText') })).toBeNull();
    expect(screen.queryByRole('region', { name: bodyRegionName() })).toBeNull();
  });
});

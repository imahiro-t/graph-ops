// DFLT-00293: in a 160px window at a 200% text size the label picker's panel
// (w-56, 14rem = 448px at a 32px root) ran past the ticket card, whose
// overflow clip cut it off. The panel now lines up with the button as before
// but is kept inside the nearest horizontally clipping ancestor (the card)
// and the window, less 0.25rem on each side (lib/popupPlacement.ts), and only
// when the width left is short of 14rem by more than 0.5px is it narrow: it
// then carries data-narrow, and only group-data-narrow: classes let the rows
// wrap and the names show in full. At full width every class works as
// before (w-56, px-3, whitespace-nowrap, truncate).
//
// jsdom does no layout, so the card's and the button's boxes, the window
// width and the root font size are stubbed; the real sizes were measured in
// a browser (see the ticket's implementation notes).
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Label } from '../types';

vi.mock('../lib/labelsApi', () => ({
  fetchLabels: vi.fn(),
  createLabel: vi.fn(),
  updateLabel: vi.fn(),
  deleteLabel: vi.fn(),
  setTicketLabels: vi.fn()
}));

import { setTicketLabels } from '../lib/labelsApi';
import { LABEL_PANEL_WIDTH_REM, LabelSelect } from './LabelSelect';

const mockedSet = setTicketLabels as unknown as ReturnType<typeof vi.fn>;

const mk = (id: string, name: string): Label => ({ id, project_id: 'p', name, color: 'blue', created_at: '', updated_at: '' });
const LONG = '14rem に収まらないとても長いラベル名 very-long-label-name-for-wrap';
const LABELS = [mk('l-ui', 'UI改善'), mk('l-bug', 'バグ'), mk('l-long', LONG)];

const NARROW_VARIANT = 'group-data-narrow:';

interface Layout {
  viewportWidth: number;
  rootFontSize: number;
  // The card's border box left edge and width, and its border width.
  cardLeft: number;
  cardWidth: number;
  cardBorder: number;
  // The picker's wrapper (the anchor) left edge.
  anchorLeft: number;
}

let layout: Layout;

function rect(left: number, width: number): DOMRect {
  return { left, right: left + width, width, top: 0, bottom: 20, height: 20, x: left, y: 0, toJSON: () => ({}) } as DOMRect;
}

beforeEach(() => {
  mockedSet.mockReset();
  mockedSet.mockResolvedValue(undefined);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.dataset.testid === 'card') return rect(layout.cardLeft, layout.cardWidth);
    if (this.querySelector(':scope > button[aria-controls]')) return rect(layout.anchorLeft, 100);
    return rect(0, 0);
  });
  vi.spyOn(HTMLElement.prototype, 'clientLeft', 'get').mockImplementation(function (this: HTMLElement) {
    return this.dataset.testid === 'card' ? layout.cardBorder : 0;
  });
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
    if (this === document.documentElement) return layout.viewportWidth;
    if (this.dataset.testid === 'card') return layout.cardWidth - 2 * layout.cardBorder;
    return 0;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  document.documentElement.style.fontSize = '';
});

function setLayout(next: Layout) {
  layout = next;
  document.documentElement.style.fontSize = `${next.rootFontSize}px`;
}

// 160px window, 32px root: the card's inside is 9..151 (as measured), the
// button starts at 17.
const TINY: Layout = { viewportWidth: 160, rootFontSize: 32, cardLeft: 8, cardWidth: 144, cardBorder: 1, anchorLeft: 17 };
// 1280px window, 16px root.
const WIDE: Layout = { viewportWidth: 1280, rootFontSize: 16, cardLeft: 24, cardWidth: 1232, cardBorder: 1, anchorLeft: 700 };

function renderInCard(projectLabels = LABELS) {
  // The card clips horizontally, like the ticket card's overflow-clip
  // (jsdom does not know `clip`, so `hidden` stands in: both are not visible).
  return render(
    <div data-testid="card" style={{ overflowX: 'hidden' }}>
      <div>
        <LabelSelect ticketId="T-1" labels={[]} projectLabels={projectLabels} onSaved={vi.fn()} />
      </div>
    </div>
  );
}

async function openPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: `${i18n.t('ticket.labels.edit')}: T-1` }));
  return screen.getByRole('group', { name: i18n.t('ticket.labels.groupLabel', { id: 'T-1' }) });
}

const px = (v: string) => parseFloat(v);

describe('LabelSelect panel placement (DFLT-00293)', () => {
  it('keeps w-56 as 14rem, the width the placement assumes', () => {
    expect(LABEL_PANEL_WIDTH_REM).toBe(14);
  });

  it('in a 160px window at 32px keeps the panel inside the card and marks it narrow', async () => {
    setLayout(TINY);
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    const left = px(panel.style.left);
    const maxWidth = px(panel.style.maxWidth);
    // 142px inside the card less 8px (0.25rem) on each side.
    expect(maxWidth).toBe(126);
    const start = TINY.anchorLeft + left;
    expect(start).toBeGreaterThanOrEqual(9 + 8);
    expect(start + maxWidth).toBeLessThanOrEqual(151 - 8);
    expect(start + maxWidth).toBeLessThanOrEqual(TINY.viewportWidth);
    expect(panel).toHaveAttribute('data-narrow');
  });

  it('keeps the full 14rem and the left edge at the button when there is room, without data-narrow', async () => {
    setLayout(WIDE);
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    expect(px(panel.style.left)).toBe(0);
    expect(px(panel.style.maxWidth)).toBeGreaterThanOrEqual(14 * 16);
    expect(panel).not.toHaveAttribute('data-narrow');
  });

  it('moves the panel left, at its full 14rem, when it would end past the card', async () => {
    // Card inside 25..999 (1024px window), button at 805: 805 + 224 > 995.
    setLayout({ viewportWidth: 1024, rootFontSize: 16, cardLeft: 24, cardWidth: 976, cardBorder: 1, anchorLeft: 805 });
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    expect(panel).not.toHaveAttribute('data-narrow');
    expect(805 + px(panel.style.left) + 224).toBe(999 - 4);
  });

  it('is not narrow when the width left is short of 14rem by less than 0.5px', async () => {
    // Inside the card: 224 + 2 * 4 - 0.3 = 231.7px, so the max width is 223.7px.
    setLayout({ viewportWidth: 1280, rootFontSize: 16, cardLeft: 100, cardWidth: 231.7 + 2, cardBorder: 1, anchorLeft: 105 });
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    expect(px(panel.style.maxWidth)).toBeCloseTo(223.7, 5);
    expect(panel).not.toHaveAttribute('data-narrow');
  });

  it('is narrow when the width left is short of 14rem by more than 0.5px', async () => {
    setLayout({ viewportWidth: 1280, rootFontSize: 16, cardLeft: 100, cardWidth: 231 + 2, cardBorder: 1, anchorLeft: 105 });
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    expect(px(panel.style.maxWidth)).toBe(223);
    expect(panel).toHaveAttribute('data-narrow');
  });

  it('works the placement out again on resize while open', async () => {
    setLayout(WIDE);
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    expect(panel).not.toHaveAttribute('data-narrow');
    setLayout(TINY);
    act(() => {
      fireEvent(window, new Event('resize'));
    });
    expect(panel).toHaveAttribute('data-narrow');
    expect(px(panel.style.maxWidth)).toBe(126);
    setLayout(WIDE);
    act(() => {
      fireEvent(window, new Event('resize'));
    });
    expect(panel).not.toHaveAttribute('data-narrow');
    expect(px(panel.style.left)).toBe(0);
  });

  it('keeps every full-width class; the narrow ones are all behind group-data-narrow:, and no container query is used', async () => {
    setLayout(WIDE);
    const user = userEvent.setup();
    renderInCard();
    const panel = await openPanel(user);
    expect(panel).toHaveClass('group', 'absolute', 'left-0', 'w-56', 'max-h-80', 'overflow-y-auto');
    const all = [panel, ...panel.querySelectorAll('*')];
    for (const el of all) {
      for (const cls of Array.from(el.classList)) expect(cls).not.toMatch(/@container/);
    }
    const rows = panel.querySelectorAll('label');
    expect(rows).toHaveLength(3);
    for (const row of rows) {
      expect(row).toHaveClass('px-3', 'whitespace-nowrap', `${NARROW_VARIANT}px-2`, `${NARROW_VARIANT}whitespace-normal`);
      expect(row).not.toHaveClass('px-2', 'whitespace-normal', 'flex-wrap');
      const checkbox = row.querySelector('input') as HTMLInputElement;
      // shrink-0 changes nothing on a one-line row, and keeps the box whole in a wrapped one.
      expect(checkbox).toHaveClass('shrink-0');
      const name = row.querySelector('span') as HTMLElement;
      expect(name).toHaveClass('truncate');
      const narrowOnly = ['min-w-0', 'overflow-visible', 'whitespace-normal', 'text-clip', 'wrap-anywhere'];
      for (const cls of narrowOnly) {
        expect(name).toHaveClass(`${NARROW_VARIANT}${cls}`);
        expect(name).not.toHaveClass(cls);
      }
    }
  });

  it('lets the "no labels" text break only when narrow', async () => {
    setLayout(TINY);
    const user = userEvent.setup();
    renderInCard([]);
    const panel = await openPanel(user);
    const text = screen.getByText(i18n.t('ticket.labels.noRegistered'));
    expect(panel).toContainElement(text);
    expect(text).toHaveClass('px-3', `${NARROW_VARIANT}px-2`, `${NARROW_VARIANT}wrap-anywhere`);
    expect(text).not.toHaveClass('wrap-anywhere');
    expect(panel).toHaveAttribute('data-narrow');
  });

  it('in a 160px window every checkbox can still be found by its name and toggled', async () => {
    setLayout(TINY);
    const user = userEvent.setup();
    renderInCard();
    await openPanel(user);
    for (const l of LABELS) {
      mockedSet.mockClear();
      const box = screen.getByRole('checkbox', { name: l.name });
      await user.click(box);
      expect(mockedSet).toHaveBeenCalledTimes(1);
      expect(mockedSet.mock.calls[0][2]).toContain(l.id);
    }
  });
});

// DFLT-00084: the ticket detail's label picker.
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
import { LabelSelect } from './LabelSelect';

const mockedSet = setTicketLabels as unknown as ReturnType<typeof vi.fn>;

const mk = (id: string, name: string, color: Label['color']): Label => ({
  id,
  project_id: 'proj-A',
  name,
  color,
  created_at: '',
  updated_at: ''
});
const BUG = mk('label-bug', 'バグ', 'red');
const FEAT = mk('label-feat', '機能追加', 'blue');
const UI = mk('label-ui', 'UI', 'purple');
const PERF = mk('label-perf', '性能', 'amber');
const PROJECT_LABELS = [BUG, FEAT, UI, PERF];

const editButtonName = () => `${i18n.t('ticket.labels.edit')}: TEST-00001`;

async function openPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: editButtonName() }));
  return screen.getByRole('group', { name: i18n.t('ticket.labels.groupLabel', { id: 'TEST-00001' }) });
}

describe('LabelSelect', () => {
  beforeEach(() => {
    mockedSet.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows every project label with the current ones checked, and PATCHes the new set when one is added', async () => {
    let resolveSave: (v: unknown) => void = () => {};
    mockedSet.mockImplementation(() => new Promise(r => (resolveSave = r)));
    const onSaved = vi.fn();
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG]} projectLabels={PROJECT_LABELS} onSaved={onSaved} />);

    await openPanel(user);
    const boxes = screen.getAllByRole('checkbox');
    expect(boxes).toHaveLength(4);
    expect(screen.getByRole('checkbox', { name: 'バグ' })).toBeChecked();
    for (const name of ['機能追加', 'UI', '性能']) {
      expect(screen.getByRole('checkbox', { name })).not.toBeChecked();
    }

    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    expect(mockedSet).toHaveBeenCalledTimes(1);
    const [, ticketId, ids] = mockedSet.mock.calls[0];
    expect(ticketId).toBe('TEST-00001');
    expect([...ids].sort()).toEqual(['label-bug', 'label-ui']);
    // Only aria-disabled while saving (a natively disabled checkbox would
    // drop keyboard focus); a further click is ignored.
    for (const box of screen.getAllByRole('checkbox')) {
      expect(box).toHaveAttribute('aria-disabled', 'true');
      expect(box).not.toBeDisabled();
    }
    await user.click(screen.getByRole('checkbox', { name: '性能' }));
    expect(mockedSet).toHaveBeenCalledTimes(1);

    resolveSave({});
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole('checkbox', { name: 'UI' })).toHaveAttribute('aria-disabled', 'false'));
  });

  it('PATCHes the remaining labels when one is removed', async () => {
    mockedSet.mockResolvedValue({});
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG, UI]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'バグ' }));

    expect(mockedSet).toHaveBeenCalledWith(expect.anything(), 'TEST-00001', ['label-ui']);
  });

  it('shows a save error and does not call onSaved when the PATCH fails', async () => {
    mockedSet.mockRejectedValue(new Error(i18n.t('errors.LABEL_NOT_FOUND')));
    const onSaved = vi.fn();
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} onSaved={onSaved} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      i18n.t('ticket.labels.saveError', { message: i18n.t('errors.LABEL_NOT_FOUND') })
    );
    expect(onSaved).not.toHaveBeenCalled();
  });

  it('points to Settings when the project has no labels', async () => {
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={[]} onSaved={vi.fn()} />);

    await openPanel(user);
    expect(screen.getByText(i18n.t('ticket.labels.noRegistered'))).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('does not propagate clicks, and Escape closes the panel returning focus to the button', async () => {
    const onParentClick = vi.fn();
    const user = userEvent.setup();
    render(
      <div onClick={onParentClick}>
        <LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />
      </div>
    );

    const button = screen.getByRole('button', { name: editButtonName() });
    await user.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(onParentClick).not.toHaveBeenCalled();

    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(button).toHaveFocus();
    expect(onParentClick).not.toHaveBeenCalled();
  });

  it('keeps keyboard focus on the checkbox across a save, so labels can be toggled in a row and Escape still closes', async () => {
    let resolveSave: (v: unknown) => void = () => {};
    mockedSet.mockImplementation(() => new Promise(r => (resolveSave = r)));
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />);

    await openPanel(user);
    const bug = screen.getByRole('checkbox', { name: 'バグ' });
    bug.focus();
    await user.keyboard(' ');
    expect(mockedSet).toHaveBeenLastCalledWith(expect.anything(), 'TEST-00001', ['label-bug']);
    expect(bug).toHaveFocus();
    resolveSave({});
    await waitFor(() => expect(bug).toHaveAttribute('aria-disabled', 'false'));
    expect(bug).toHaveFocus();

    await user.tab();
    const feat = screen.getByRole('checkbox', { name: '機能追加' });
    expect(feat).toHaveFocus();
    await user.keyboard(' ');
    expect(mockedSet).toHaveBeenLastCalledWith(expect.anything(), 'TEST-00001', ['label-bug', 'label-feat']);
    resolveSave({});
    await waitFor(() => expect(feat).toHaveAttribute('aria-disabled', 'false'));
    expect(feat).toHaveFocus();

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: editButtonName() })).toHaveFocus();
  });

  it('keeps a current label missing from a stale projectLabels when adding another', async () => {
    mockedSet.mockResolvedValue({});
    const NEW = mk('label-new', '新規', 'green');
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[NEW]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));

    expect(mockedSet).toHaveBeenCalledWith(expect.anything(), 'TEST-00001', ['label-new', 'label-ui']);
  });
});

// DFLT-00295: the open panel is fitted into the first ancestor that clips
// horizontally (the ticket card). jsdom does no layout, so the clip's and the
// anchor's measurements are stubbed; jsdom has no root font size either, so
// the panel's own width (14rem) is taken at 16px: 224px. The clip is marked
// with overflow-x (jsdom's computed style reports the longhand only).
describe('LabelSelect panel position (DFLT-00295)', () => {
  const NATURAL = 224;
  const INSET = 4;

  type Box = { left: number; clientLeft: number; clientWidth: number };

  const setup = (box: Box, anchorLeft: number) => {
    const user = userEvent.setup();
    const view = render(
      <div data-testid="clip" style={{ overflowX: 'clip' }}>
        <div>
          <LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />
        </div>
      </div>
    );
    const clip = screen.getByTestId('clip');
    const current = { ...box };
    const clipRect = vi.fn(() => ({ left: current.left, right: current.left + current.clientWidth, top: 0, bottom: 0, width: current.clientWidth, height: 0, x: current.left, y: 0, toJSON: () => ({}) }) as DOMRect);
    clip.getBoundingClientRect = clipRect;
    Object.defineProperty(clip, 'clientWidth', { configurable: true, get: () => current.clientWidth });
    Object.defineProperty(clip, 'clientLeft', { configurable: true, get: () => current.clientLeft });
    const button = screen.getByRole('button', { name: editButtonName() });
    const anchor = button.parentElement as HTMLElement;
    anchor.getBoundingClientRect = () => ({ left: anchorLeft, right: anchorLeft, top: 0, bottom: 0, width: 0, height: 0, x: anchorLeft, y: 0, toJSON: () => ({}) }) as DOMRect;
    const setBox = (next: Box) => Object.assign(current, next);
    return { user, view, clip, clipRect, button, anchor, setBox };
  };

  // What the panel should be for a clip `box` and an anchor at `anchorLeft`.
  const expected = (box: Box, anchorLeft: number) => {
    const min = box.left + box.clientLeft + INSET;
    const max = min + box.clientWidth - 2 * INSET;
    const width = Math.min(NATURAL, max - min);
    const left = Math.max(min, Math.min(anchorLeft, max - width));
    return { width, left: left - anchorLeft };
  };

  const WIDE: Box = { left: 0, clientLeft: 1, clientWidth: 1000 };
  const NARROW: Box = { left: 10, clientLeft: 1, clientWidth: 150 };

  const expectFitted = (panel: HTMLElement, box: Box, anchorLeft: number) => {
    const { width, left } = expected(box, anchorLeft);
    expect(panel.style.width).toBe(`${width}px`);
    expect(panel.style.left).toBe(`${left}px`);
    const min = box.left + box.clientLeft + INSET;
    const max = min + box.clientWidth - 2 * INSET;
    // Narrower than 14rem, inside the clip on both sides.
    expect(width).toBeLessThan(NATURAL);
    expect(width).toBeLessThanOrEqual(box.clientWidth - 2 * INSET);
    expect(anchorLeft + left).toBeGreaterThanOrEqual(min);
    expect(anchorLeft + left + width).toBeLessThanOrEqual(max);
  };

  const expectUntouched = (panel: HTMLElement) => {
    expect(panel.style.width).toBe('');
    expect(panel.style.left).toBe('');
    expect(panel).toHaveClass('absolute', 'left-0', 'top-full', 'w-56');
  };

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('leaves the panel at left-0 w-56 when the clip is wide enough', async () => {
    const { user } = setup(WIDE, 100);
    const panel = await openPanel(user);
    expectUntouched(panel);
  });

  it('narrows the panel and moves it left to fit into a narrow clip', async () => {
    const { user } = setup(NARROW, 60);
    const panel = await openPanel(user);
    expectFitted(panel, NARROW, 60);
    // 150 - 2 * 4 wide, from the clip's inside left edge (10 + 1 + 4).
    expect(panel.style.width).toBe('142px');
    expect(panel.style.left).toBe('-45px');
  });

  it('only moves the panel left when it is as wide as it should be but runs past the clip', async () => {
    const box: Box = { left: 0, clientLeft: 0, clientWidth: 400 };
    const { user } = setup(box, 300);
    const panel = await openPanel(user);
    // 224px wide, its right edge at the clip's 396.
    expect(panel.style.width).toBe(`${NATURAL}px`);
    expect(panel.style.left).toBe(`${396 - NATURAL - 300}px`);
  });

  it('sets no position when the clip measures 0 wide', async () => {
    const { user } = setup({ left: 0, clientLeft: 0, clientWidth: 0 }, 0);
    const panel = await openPanel(user);
    expectUntouched(panel);
  });

  it('refits the open panel when the window is resized, both ways', async () => {
    const { user, setBox } = setup(WIDE, 60);
    const panel = await openPanel(user);
    expectUntouched(panel);

    setBox(NARROW);
    fireEvent(window, new Event('resize'));
    expectFitted(panel, NARROW, 60);

    setBox(WIDE);
    fireEvent(window, new Event('resize'));
    expectUntouched(panel);
  });

  it('stops measuring on resize once the panel is closed', async () => {
    const add = vi.spyOn(window, 'addEventListener');
    const remove = vi.spyOn(window, 'removeEventListener');
    const { user, clipRect, button, setBox } = setup(NARROW, 60);
    await openPanel(user);
    const handlers = add.mock.calls.filter(([type]) => type === 'resize').map(([, fn]) => fn);
    expect(handlers).toHaveLength(1);

    await user.click(button);
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(remove).toHaveBeenCalledWith('resize', handlers[0]);

    clipRect.mockClear();
    setBox(WIDE);
    fireEvent(window, new Event('resize'));
    expect(clipRect).not.toHaveBeenCalled();
  });

  it('removes its resize listener when unmounted while open', async () => {
    const add = vi.spyOn(window, 'addEventListener');
    const remove = vi.spyOn(window, 'removeEventListener');
    const { user, view, clipRect } = setup(NARROW, 60);
    await openPanel(user);
    const handler = add.mock.calls.find(([type]) => type === 'resize')?.[1];
    expect(handler).toBeDefined();

    view.unmount();
    expect(remove).toHaveBeenCalledWith('resize', handler);
    clipRect.mockClear();
    expect(() => fireEvent(window, new Event('resize'))).not.toThrow();
    expect(clipRect).not.toHaveBeenCalled();
  });

  it('keeps the panel in the anchor around the button, apart from a save error', async () => {
    mockedSet.mockReset();
    mockedSet.mockRejectedValue(new Error(i18n.t('errors.LABEL_NOT_FOUND')));
    const { user, button, anchor } = setup(NARROW, 60);
    const panel = await openPanel(user);
    const before = { width: panel.style.width, left: panel.style.left };
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    const alert = await screen.findByRole('alert');

    expect(anchor).toHaveClass('relative', 'min-w-0', 'max-w-full');
    expect(button.parentElement).toBe(anchor);
    expect(panel.parentElement).toBe(anchor);
    expect(anchor).not.toContainElement(alert);
    expect(alert.parentElement).toBe(anchor.parentElement);
    expect(anchor.parentElement).not.toHaveClass('relative');
    // The wrapper is display: contents, so the error is laid out after the
    // button by the surrounding row and cannot move it.
    expect(anchor.parentElement).toHaveClass('contents');
    expect(anchor.nextElementSibling).toBe(alert);
    expect({ width: panel.style.width, left: panel.style.left }).toEqual(before);
  });

  it('truncates a long name in a narrowed panel and keeps the checkbox size', async () => {
    const { user } = setup(NARROW, 60);
    await openPanel(user);
    const box = screen.getByRole('checkbox', { name: 'バグ' });
    expect(box).toHaveClass('shrink-0');
    const name = box.nextElementSibling as HTMLElement;
    expect(name).toHaveClass('truncate', 'min-w-0');
    expect(box.parentElement).toHaveClass('flex', 'whitespace-nowrap', 'px-3', 'gap-2', 'upto-15rem:px-2', 'upto-15rem:gap-1.5');
  });
});

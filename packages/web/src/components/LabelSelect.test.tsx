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
const U0 = '2026-09-30T00:00:00Z';

const editButtonName = () => `${i18n.t('ticket.labels.edit')}: TEST-00001`;

async function openPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: editButtonName() }));
  return screen.getByRole('group', { name: i18n.t('ticket.labels.groupLabel', { id: 'TEST-00001' }) });
}

// DFLT-00321: the same keyboard focus line as the other controls (a 2px
// blue-500 / dark:blue-400 outline on focus-visible), and no ring-0 override.
// The ring-0 class is assembled at run time so Tailwind does not pick it up
// from this file.
const NO_RING = ['focus', 'ring-0'].join(':');
const CHECKBOX_FOCUS = [
  'focus:outline-hidden',
  'focus-visible:outline-solid',
  'focus-visible:outline-2',
  'focus-visible:outline-offset-0',
  'focus-visible:outline-blue-500',
  'dark:focus-visible:outline-blue-400'
];

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
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={onSaved} />);

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
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG, UI]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={vi.fn()} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'バグ' }));

    expect(mockedSet).toHaveBeenCalledWith(expect.anything(), 'TEST-00001', ['label-ui'], U0);
  });

  it('shows a save error and does not call onSaved when the PATCH fails', async () => {
    mockedSet.mockRejectedValue(new Error(i18n.t('errors.LABEL_NOT_FOUND')));
    const onSaved = vi.fn();
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={onSaved} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      i18n.t('ticket.labels.saveError', { message: i18n.t('errors.LABEL_NOT_FOUND') })
    );
    expect(onSaved).not.toHaveBeenCalled();
  });

  it('points to Settings when the project has no labels', async () => {
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={[]} updatedAt={U0} onSaved={vi.fn()} />);

    await openPanel(user);
    expect(screen.getByText(i18n.t('ticket.labels.noRegistered'))).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('does not propagate clicks, and Escape closes the panel returning focus to the button', async () => {
    const onParentClick = vi.fn();
    const user = userEvent.setup();
    render(
      <div onClick={onParentClick}>
        <LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={vi.fn()} />
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
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={vi.fn()} />);

    await openPanel(user);
    const bug = screen.getByRole('checkbox', { name: 'バグ' });
    bug.focus();
    await user.keyboard(' ');
    expect(mockedSet).toHaveBeenLastCalledWith(expect.anything(), 'TEST-00001', ['label-bug'], U0);
    expect(bug).toHaveFocus();
    resolveSave({});
    await waitFor(() => expect(bug).toHaveAttribute('aria-disabled', 'false'));
    expect(bug).toHaveFocus();

    await user.tab();
    const feat = screen.getByRole('checkbox', { name: '機能追加' });
    expect(feat).toHaveFocus();
    await user.keyboard(' ');
    expect(mockedSet).toHaveBeenLastCalledWith(expect.anything(), 'TEST-00001', ['label-bug', 'label-feat'], U0);
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
    render(<LabelSelect ticketId="TEST-00001" labels={[NEW]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={vi.fn()} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));

    expect(mockedSet).toHaveBeenCalledWith(expect.anything(), 'TEST-00001', ['label-new', 'label-ui'], U0);
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

  const setup = (box: Box, initialAnchorLeft: number) => {
    const user = userEvent.setup();
    const tree = (labels: Label[]) => (
      <div data-testid="clip" style={{ overflowX: 'clip' }}>
        <div data-testid="row">
          <LabelSelect ticketId="TEST-00001" labels={labels} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={vi.fn()} />
        </div>
      </div>
    );
    const view = render(tree([]));
    // Re-renders with the ticket's labels changed, as the parent's re-fetch
    // does after a save.
    const setLabels = (labels: Label[]) => view.rerender(tree(labels));
    const clip = screen.getByTestId('clip');
    const current = { ...box };
    const clipRect = vi.fn(() => ({ left: current.left, right: current.left + current.clientWidth, top: 0, bottom: 0, width: current.clientWidth, height: 0, x: current.left, y: 0, toJSON: () => ({}) }) as DOMRect);
    clip.getBoundingClientRect = clipRect;
    Object.defineProperty(clip, 'clientWidth', { configurable: true, get: () => current.clientWidth });
    Object.defineProperty(clip, 'clientLeft', { configurable: true, get: () => current.clientLeft });
    const button = screen.getByRole('button', { name: editButtonName() });
    const anchor = button.parentElement as HTMLElement;
    let anchorLeft = initialAnchorLeft;
    anchor.getBoundingClientRect = () => ({ left: anchorLeft, right: anchorLeft, top: 0, bottom: 0, width: 0, height: 0, x: anchorLeft, y: 0, toJSON: () => ({}) }) as DOMRect;
    const setBox = (next: Box) => Object.assign(current, next);
    // Moves the button, as a chip added before it does.
    const setAnchorLeft = (left: number) => (anchorLeft = left);
    return { user, view, clip, clipRect, button, anchor, setBox, setAnchorLeft, setLabels };
  };

  // What the panel should be for a clip `box` and an anchor at `anchorLeft`.
  const expected = (box: Box, anchorLeft: number) => {
    const min = box.left + box.clientLeft + INSET;
    const max = min + box.clientWidth - 2 * INSET;
    const maxWidth = max - min;
    const width = Math.min(NATURAL, maxWidth);
    const left = Math.max(min, Math.min(anchorLeft, max - width));
    return { maxWidth, width, left: left - anchorLeft };
  };

  const WIDE: Box = { left: 0, clientLeft: 1, clientWidth: 1000 };
  const NARROW: Box = { left: 10, clientLeft: 1, clientWidth: 150 };

  const expectFitted = (panel: HTMLElement, box: Box, anchorLeft: number) => {
    const { maxWidth, width, left } = expected(box, anchorLeft);
    expect(panel.style.maxWidth).toBe(`${maxWidth}px`);
    expect(panel.style.left).toBe(`${left}px`);
    expect(panel).toHaveAttribute('data-narrow');
    const min = box.left + box.clientLeft + INSET;
    const max = min + box.clientWidth - 2 * INSET;
    // Narrower than 14rem, inside the clip on both sides.
    expect(width).toBeLessThan(NATURAL);
    expect(width).toBeLessThanOrEqual(box.clientWidth - 2 * INSET);
    expect(anchorLeft + left).toBeGreaterThanOrEqual(min);
    expect(anchorLeft + left + width).toBeLessThanOrEqual(max);
  };

  // DFLT-00293: where it fits, the panel keeps its left edge at the button
  // and its full 14rem (w-56); the max-width only caps it at the clip.
  const expectAtButton = (panel: HTMLElement) => {
    expect(panel.style.left).toBe('0px');
    expect(parseFloat(panel.style.maxWidth)).toBeGreaterThanOrEqual(NATURAL);
    expect(panel).not.toHaveAttribute('data-narrow');
    expect(panel).toHaveClass('absolute', 'left-0', 'top-full', 'w-56');
  };

  const expectNoStyle = (panel: HTMLElement) => {
    expect(panel.style.maxWidth).toBe('');
    expect(panel.style.left).toBe('');
    expect(panel).not.toHaveAttribute('data-narrow');
    expect(panel).toHaveClass('absolute', 'left-0', 'top-full', 'w-56');
  };

  // DFLT-00311: animation frames only queue up and run when a test steps
  // them. jsdom (pretendToBeVisual) would otherwise run them every 16ms, and
  // a test of the events above could pass because a frame happened to refit
  // the panel. None of those tests steps a frame, so they show the events
  // alone refit it.
  let frames = new Map<number, FrameRequestCallback>();
  let nextFrame = 0;
  const step = () => {
    const due = [...frames.values()];
    frames = new Map();
    for (const callback of due) callback(0);
  };
  const pending = () => frames.size;

  beforeEach(() => {
    frames = new Map();
    nextFrame = 0;
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
      frames.set(++nextFrame, callback);
      return nextFrame;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => {
      frames.delete(id);
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('leaves the panel at left-0 w-56 when the clip is wide enough', async () => {
    const { user } = setup(WIDE, 100);
    const panel = await openPanel(user);
    expectAtButton(panel);
  });

  it('narrows the panel and moves it left to fit into a narrow clip', async () => {
    const { user } = setup(NARROW, 60);
    const panel = await openPanel(user);
    expectFitted(panel, NARROW, 60);
    // 150 - 2 * 4 wide, from the clip's inside left edge (10 + 1 + 4).
    expect(panel.style.maxWidth).toBe('142px');
    expect(panel.style.left).toBe('-45px');
  });

  it('only moves the panel left when it is as wide as it should be but runs past the clip', async () => {
    const box: Box = { left: 0, clientLeft: 0, clientWidth: 400 };
    const { user } = setup(box, 300);
    const panel = await openPanel(user);
    // 224px wide (capped at 392px only), its right edge at the clip's 396.
    expect(panel.style.maxWidth).toBe('392px');
    expect(panel).not.toHaveAttribute('data-narrow');
    expect(panel.style.left).toBe(`${396 - NATURAL - 300}px`);
  });

  it('sets no position when the clip measures 0 wide', async () => {
    const { user } = setup({ left: 0, clientLeft: 0, clientWidth: 0 }, 0);
    const panel = await openPanel(user);
    expectNoStyle(panel);
  });

  it('refits the open panel when the window is resized, both ways', async () => {
    const { user, setBox } = setup(WIDE, 60);
    const panel = await openPanel(user);
    expectAtButton(panel);

    setBox(NARROW);
    fireEvent(window, new Event('resize'));
    expectFitted(panel, NARROW, 60);

    setBox(WIDE);
    fireEvent(window, new Event('resize'));
    expectAtButton(panel);
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

  it('refits the open panel when a saved label moves the button (the labels prop changes)', async () => {
    // At 320px the button sat at the right end of its line, so the panel was
    // moved left; a label saved with the panel open pushed the button onto
    // the next line, and the panel kept the old left, past the clip's left
    // edge. Less the inset, the clip's inside runs from 17 to 303 here.
    const box: Box = { left: 12, clientLeft: 1, clientWidth: 294 };
    const { user, setAnchorLeft, setLabels } = setup(box, 130);
    const panel = await openPanel(user);
    expect(panel.style.maxWidth).toBe('286px');
    expect(panel).not.toHaveAttribute('data-narrow');
    expect(panel.style.left).toBe(`${expected(box, 130).left}px`);
    expect(panel.style.left).toBe('-51px');

    setAnchorLeft(25);
    setLabels([BUG]);
    // Fits from the button's new place: 224px wide from 25 ends at 249, so
    // back at the button -- nothing of the old left is left behind.
    expectAtButton(panel);

    setAnchorLeft(200);
    setLabels([BUG, UI]);
    expect(panel.style.left).toBe('-121px');
    // 224px from 79 ends at the inside's right edge, 303.
    expect(200 + parseFloat(panel.style.left)).toBeGreaterThanOrEqual(17);
  });

  it('refits the open panel when a save error appears', async () => {
    mockedSet.mockReset();
    mockedSet.mockRejectedValue(new Error(i18n.t('errors.LABEL_NOT_FOUND')));
    const { user, setAnchorLeft } = setup(NARROW, 60);
    const panel = await openPanel(user);
    expectFitted(panel, NARROW, 60);
    setAnchorLeft(40);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    await screen.findByRole('alert');
    expectFitted(panel, NARROW, 40);
  });

  describe('with a ResizeObserver', () => {
    type Callback = () => void;
    const observers: { callback: Callback; targets: Element[]; disconnected: boolean }[] = [];

    beforeEach(() => {
      observers.length = 0;
      vi.stubGlobal(
        'ResizeObserver',
        class {
          private entry: (typeof observers)[number];
          constructor(callback: Callback) {
            this.entry = { callback, targets: [], disconnected: false };
            observers.push(this.entry);
          }
          observe(el: Element) {
            this.entry.targets.push(el);
          }
          unobserve() {}
          disconnect() {
            this.entry.disconnected = true;
          }
        }
      );
    });

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    const live = () => observers.filter(o => !o.disconnected);

    it('watches the anchor, the row it sits in and the clip, and refits when they resize', async () => {
      const { user, clip, anchor, setBox, setAnchorLeft } = setup(WIDE, 60);
      const panel = await openPanel(user);
      expectAtButton(panel);
      expect(live()).toHaveLength(1);
      // The wrapper is display: contents, so its parent (the row) is watched.
      expect(live()[0].targets).toEqual([anchor, screen.getByTestId('row'), clip]);

      // A change of the text size or of the row moves the button; nothing
      // else fires.
      setBox(NARROW);
      setAnchorLeft(40);
      live()[0].callback();
      expectFitted(panel, NARROW, 40);
    });

    it('disconnects when the panel closes or unmounts', async () => {
      const { user, view, button } = setup(NARROW, 60);
      await openPanel(user);
      expect(live()).toHaveLength(1);
      await user.click(button);
      expect(live()).toHaveLength(0);

      await openPanel(user);
      expect(live()).toHaveLength(1);
      view.unmount();
      expect(live()).toHaveLength(0);
    });
  });

  // DFLT-00311: the button moving without any change of size (an item before
  // the labels getting wider) fires none of the events above.
  describe('while open, every animation frame (DFLT-00311)', () => {
    it.each([
      // WIDE: from 100 the panel fits as it is; at 900 it keeps its 224px and
      // only moves left, its right edge at the clip's inside 997.
      { name: 'WIDE', box: WIDE, from: 100, before: { maxWidth: '992px', left: '0px' }, to: 900, after: { maxWidth: '992px', left: '-127px' } },
      { name: 'NARROW', box: NARROW, from: 60, before: { maxWidth: '142px', left: '-45px' }, to: 40, after: { maxWidth: '142px', left: '-25px' } }
    ])('refits the panel on the next frame when only the button moves ($name, $from to $to)', async ({ box, from, before, to, after }) => {
      const { user, setAnchorLeft } = setup(box, from);
      const panel = await openPanel(user);
      expect({ maxWidth: panel.style.maxWidth, left: panel.style.left }).toEqual(before);

      setAnchorLeft(to);
      // Nothing but a frame refits it.
      expect({ maxWidth: panel.style.maxWidth, left: panel.style.left }).toEqual(before);
      step();
      expect({ maxWidth: panel.style.maxWidth, left: panel.style.left }).toEqual(after);
      const { maxWidth, left } = expected(box, to);
      expect(panel.style.maxWidth).toBe(`${maxWidth}px`);
      expect(panel.style.left).toBe(`${left}px`);
    });

    it('drops the inline style on the next frame when the button moves back to where the panel fits', async () => {
      const { user, setAnchorLeft } = setup(WIDE, 100);
      const panel = await openPanel(user);
      setAnchorLeft(900);
      step();
      expect(panel.style.maxWidth).toBe('992px');
      expect(panel.style.left).toBe('-127px');

      setAnchorLeft(100);
      step();
      expectAtButton(panel);
    });

    it('rewrites nothing on frames where nothing changed', async () => {
      const { user } = setup(NARROW, 60);
      const panel = await openPanel(user);
      const remove = vi.spyOn(panel.style, 'removeProperty');
      const set = vi.spyOn(panel.style, 'setProperty');
      step();
      step();
      step();
      expect(remove).not.toHaveBeenCalled();
      expect(set).not.toHaveBeenCalled();
      expect(panel.style.maxWidth).toBe('142px');
      expect(panel.style.left).toBe('-45px');
    });

    it('rewrites nothing on the frames after a frame refitted the panel', async () => {
      const { user, setAnchorLeft } = setup(NARROW, 60);
      const panel = await openPanel(user);
      setAnchorLeft(40);
      step();
      expectFitted(panel, NARROW, 40);

      const remove = vi.spyOn(panel.style, 'removeProperty');
      const set = vi.spyOn(panel.style, 'setProperty');
      step();
      step();
      step();
      expect(remove).not.toHaveBeenCalled();
      expect(set).not.toHaveBeenCalled();
      expect(panel.style.maxWidth).toBe('142px');
      expect(panel.style.left).toBe('-25px');
    });

    it('rewrites nothing on the frame right after a resize refitted the panel', async () => {
      const { user, setBox } = setup(WIDE, 60);
      const panel = await openPanel(user);
      setBox(NARROW);
      fireEvent(window, new Event('resize'));
      expectFitted(panel, NARROW, 60);

      const remove = vi.spyOn(panel.style, 'removeProperty');
      const set = vi.spyOn(panel.style, 'setProperty');
      step();
      expect(remove).not.toHaveBeenCalled();
      expect(set).not.toHaveBeenCalled();
      expectFitted(panel, NARROW, 60);
    });

    it('rewrites nothing when the button and the clip move together (the page scrolls)', async () => {
      const { user, setBox, setAnchorLeft } = setup(NARROW, 60);
      const panel = await openPanel(user);
      const remove = vi.spyOn(panel.style, 'removeProperty');
      const set = vi.spyOn(panel.style, 'setProperty');
      setBox({ ...NARROW, left: NARROW.left + 30 });
      setAnchorLeft(60 + 30);
      step();
      expect(remove).not.toHaveBeenCalled();
      expect(set).not.toHaveBeenCalled();
      expect(panel.style.maxWidth).toBe('142px');
      expect(panel.style.left).toBe('-45px');
    });

    it('leaves no frame behind and measures nothing once the panel is closed', async () => {
      const { user, button, clipRect, setAnchorLeft } = setup(NARROW, 60);
      await openPanel(user);
      expect(pending()).toBeGreaterThanOrEqual(1);

      await user.click(button);
      expect(screen.queryByRole('group')).not.toBeInTheDocument();
      expect(pending()).toBe(0);

      const calls = clipRect.mock.calls.length;
      setAnchorLeft(40);
      step();
      step();
      step();
      expect(clipRect.mock.calls.length).toBe(calls);
    });

    it('leaves no frame behind when unmounted while open', async () => {
      const { user, view } = setup(NARROW, 60);
      await openPanel(user);
      expect(pending()).toBeGreaterThanOrEqual(1);
      view.unmount();
      expect(pending()).toBe(0);
    });

    it('sets no position on any frame when the clip measures 0 wide', async () => {
      const { user, setAnchorLeft } = setup({ left: 0, clientLeft: 0, clientWidth: 0 }, 0);
      const panel = await openPanel(user);
      setAnchorLeft(40);
      step();
      step();
      step();
      expectNoStyle(panel);
    });
  });

  it('keeps the panel in the anchor around the button, apart from a save error', async () => {
    mockedSet.mockReset();
    mockedSet.mockRejectedValue(new Error(i18n.t('errors.LABEL_NOT_FOUND')));
    const { user, button, anchor } = setup(NARROW, 60);
    const panel = await openPanel(user);
    const before = { maxWidth: panel.style.maxWidth, left: panel.style.left };
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
    expect({ maxWidth: panel.style.maxWidth, left: panel.style.left }).toEqual(before);
  });

  it('lets a long name wrap in a narrowed panel and keeps the checkbox size (DFLT-00293)', async () => {
    const { user } = setup(NARROW, 60);
    const panel = await openPanel(user);
    expect(panel).toHaveAttribute('data-narrow');
    expect(panel).toHaveClass('group');
    const box = screen.getByRole('checkbox', { name: 'バグ' });
    expect(box).toHaveClass('shrink-0');
    const name = box.nextElementSibling as HTMLElement;
    expect(name).toHaveClass('truncate', 'group-data-narrow:min-w-0', 'group-data-narrow:whitespace-normal', 'group-data-narrow:wrap-anywhere');
    expect(box.parentElement).toHaveClass('flex', 'whitespace-nowrap', 'px-3', 'group-data-narrow:px-2', 'group-data-narrow:whitespace-normal');
  });

  it('gives each checkbox the same keyboard focus line as the other controls (DFLT-00321)', async () => {
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG]} projectLabels={PROJECT_LABELS} updatedAt={U0} onSaved={vi.fn()} />);
    await openPanel(user);
    const boxes = screen.getAllByRole('checkbox');
    expect(boxes).toHaveLength(4);
    for (const box of boxes) {
      expect(box).toHaveClass(...CHECKBOX_FOCUS);
      expect(box).not.toHaveClass(NO_RING);
    }
  });
});

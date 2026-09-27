// DFLT-00086: the control all four toolbar filters are built from, on its
// own. How each filter's selection narrows the ticket list -- and that all
// four really do use this component -- is covered by App.filters.test.tsx.
import { useState } from 'react';
import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { MultiSelectFilter, MultiSelectFilterOption } from './MultiSelectFilter';

// Real i18n keys, so a renamed/removed key fails here rather than silently
// rendering its own name. Status is as good a stand-in as any of the four.
const KEYS = {
  panelId: 'toolbar-status-filter-panel',
  allKey: 'toolbar.statusAll',
  selectedKey: 'toolbar.statusSelected',
  groupLabelKey: 'toolbar.statusGroupLabel'
};

const OPTIONS: MultiSelectFilterOption<string>[] = [
  { value: 'a', label: 'あ' },
  { value: 'b', label: 'い' },
  { value: 'c', label: 'う' }
];

function Harness({
  options = OPTIONS,
  emptyKey,
  initialSelected = [],
  onChange
}: {
  options?: MultiSelectFilterOption<string>[];
  emptyKey?: string;
  initialSelected?: string[];
  onChange?: (next: string[]) => void;
}) {
  const [selected, setSelected] = useState<string[]>(initialSelected);
  return (
    <MultiSelectFilter
      {...KEYS}
      emptyKey={emptyKey}
      options={options}
      selected={selected}
      onChange={next => {
        onChange?.(next);
        setSelected(next);
      }}
    />
  );
}

const trigger = () => screen.getByRole('button', { name: /^ステータス: / });
const clearButton = () => screen.getByRole('button', { name: i18n.t('toolbar.filterClear') });

describe('MultiSelectFilter', () => {
  it('reads "All" with nothing selected and "N selected" afterwards', async () => {
    const user = userEvent.setup();
    render(<Harness />);

    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusAll'));
    await user.click(trigger());
    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 1 }));
    await user.click(screen.getByRole('checkbox', { name: 'い' }));
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 2 }));

    // Even with everything checked it stays "N selected": "All" is reserved
    // for the empty selection, the state that actually means no filtering.
    await user.click(screen.getByRole('checkbox', { name: 'う' }));
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 3 }));
  });

  it('exposes aria-expanded/aria-controls and a labelled group', async () => {
    const user = userEvent.setup();
    render(<Harness />);

    // Closed: the panel is not rendered, so there must be no aria-controls
    // pointing at it (DFLT-00087).
    expect(trigger()).toHaveAttribute('aria-expanded', 'false');
    expect(trigger()).not.toHaveAttribute('aria-controls');
    expect(document.getElementById(KEYS.panelId)).toBeNull();
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(screen.getByTestId(`${KEYS.panelId}-trigger`)).toBe(trigger());

    await user.click(trigger());
    expect(trigger()).toHaveAttribute('aria-expanded', 'true');
    expect(trigger()).toHaveAttribute('aria-controls', KEYS.panelId);
    const panel = screen.getByRole('group', { name: i18n.t('toolbar.statusGroupLabel') });
    expect(panel).toHaveAttribute('id', KEYS.panelId);

    await user.keyboard('{Escape}');
    expect(trigger()).not.toHaveAttribute('aria-controls');
  });

  it('keeps the panel open while toggling, and the selection in option order', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<Harness onChange={onChange} />);

    await user.click(trigger());
    // Clicked out of order; the selection comes back in the panel's order.
    await user.click(screen.getByRole('checkbox', { name: 'う' }));
    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    expect(onChange).toHaveBeenLastCalledWith(['a', 'c']);
    expect(screen.getByRole('group')).toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: 'う' }));
    expect(onChange).toHaveBeenLastCalledWith(['a']);
    expect(screen.getByRole('group')).toBeInTheDocument();
  });

  it('clears the selection with the clear button, which is disabled while empty', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<Harness onChange={onChange} />);

    await user.click(trigger());
    expect(clearButton()).toBeDisabled();

    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    await user.click(screen.getByRole('checkbox', { name: 'い' }));
    expect(clearButton()).toBeEnabled();

    await user.click(clearButton());
    expect(onChange).toHaveBeenLastCalledWith([]);
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusAll'));
    for (const box of screen.getAllByRole('checkbox')) expect(box).not.toBeChecked();
    expect(clearButton()).toBeDisabled();
  });

  it('moves focus to the trigger on clear, so Escape still closes the panel', async () => {
    // DFLT-00087: the clear button disables itself when pressed; before the
    // fix focus fell to <body>, outside the Escape handler.
    const user = userEvent.setup();
    render(<Harness />);

    await user.click(trigger());
    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    await user.click(clearButton());
    expect(document.activeElement).toBe(trigger());
    expect(clearButton()).toBeDisabled();

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(trigger());
  });

  it('keeps selected values that are not among the options', async () => {
    // DFLT-00087: 'z' has no checkbox (think of an assignee who dropped out
    // of the polled options). Checking or unchecking others must keep it.
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<Harness initialSelected={['z']} onChange={onChange} />);
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 1 }));

    await user.click(trigger());
    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    expect(onChange).toHaveBeenLastCalledWith(['a', 'z']);
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 2 }));

    await user.click(screen.getByRole('checkbox', { name: 'う' }));
    expect(onChange).toHaveBeenLastCalledWith(['a', 'c', 'z']);

    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    expect(onChange).toHaveBeenLastCalledWith(['c', 'z']);
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 2 }));

    // Only the clear button can drop a value that has no checkbox.
    await user.click(clearButton());
    expect(onChange).toHaveBeenLastCalledWith([]);
    expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusAll'));
  });

  it('still shows the clear button, disabled, when there are no options at all', async () => {
    // The deliberate choice (see MultiSelectFilter.tsx): the panel's shape is
    // the same for every filter in every state, rather than the clear button
    // appearing only once a filter happens to have options.
    const user = userEvent.setup();
    render(<Harness options={[]} emptyKey="toolbar.labelEmpty" />);

    await user.click(trigger());
    expect(screen.getByText(i18n.t('toolbar.labelEmpty'))).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(clearButton()).toBeDisabled();
  });

  // DFLT-00264: "select all" next to "clear", in the same footer row.
  describe('select all', () => {
    const selectAllButton = () => screen.getByRole('button', { name: i18n.t('toolbar.filterSelectAll') });

    it('sits in the footer row before the clear button', async () => {
      const user = userEvent.setup();
      render(<Harness />);
      await user.click(trigger());

      const footer = clearButton().parentElement!;
      expect(selectAllButton().parentElement).toBe(footer);
      expect(within(footer).getAllByRole('button')).toEqual([selectAllButton(), clearButton()]);
    });

    it('lays the footer out to wrap by content width, not in fixed halves', async () => {
      // jsdom has no layout, so -- like the panel placement tests -- the
      // classes are checked; the real wrapping was checked in a browser.
      const user = userEvent.setup();
      render(<Harness />);
      await user.click(trigger());

      expect(clearButton().parentElement).toHaveClass('flex', 'flex-wrap');
      for (const button of [selectAllButton(), clearButton()]) {
        expect(button).toHaveClass('flex-auto', 'break-keep', '[overflow-wrap:anywhere]');
        expect(button).not.toHaveClass('w-full');
        expect(button).not.toHaveClass('flex-1');
        expect(button).not.toHaveClass('min-w-0');
      }
    });

    it('selects every option in display order from nothing, keeping the panel open', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();
      render(<Harness onChange={onChange} />);

      await user.click(trigger());
      expect(selectAllButton()).toBeEnabled();
      await user.click(selectAllButton());

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenLastCalledWith(['a', 'b', 'c']);
      expect(screen.getByRole('group')).toBeInTheDocument();
      for (const box of screen.getAllByRole('checkbox')) expect(box).toBeChecked();
      // Still "N selected", never folded back to "All".
      expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 3 }));
      expect(trigger()).not.toHaveTextContent(i18n.t('toolbar.statusAll'));
    });

    it('returns the display order even when the partial selection was in another order', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();
      render(<Harness initialSelected={['c', 'a']} onChange={onChange} />);

      await user.click(trigger());
      expect(selectAllButton()).toBeEnabled();
      await user.click(selectAllButton());
      expect(onChange).toHaveBeenLastCalledWith(['a', 'b', 'c']);
    });

    it('keeps selected values that are not among the options, after them in their order', async () => {
      // 'x' and 'y' have no checkbox -- assignees who dropped out with the poll.
      const onChange = vi.fn();
      const user = userEvent.setup();
      render(
        <Harness
          options={OPTIONS.slice(0, 2)}
          initialSelected={['x', 'b', 'y']}
          onChange={onChange}
        />
      );

      await user.click(trigger());
      await user.click(selectAllButton());
      expect(onChange).toHaveBeenLastCalledWith(['a', 'b', 'x', 'y']);
      expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 4 }));
    });

    it('is disabled when every option is already selected', async () => {
      const user = userEvent.setup();
      render(<Harness options={OPTIONS.slice(0, 2)} initialSelected={['b', 'a']} />);
      await user.click(trigger());
      expect(selectAllButton()).toBeDisabled();
    });

    it.each([
      ['nothing', []],
      ['some options', ['a']],
      ['only values outside the options', ['x']]
    ])('is enabled when %s is selected', async (_, initialSelected) => {
      const user = userEvent.setup();
      render(<Harness options={OPTIONS.slice(0, 2)} initialSelected={initialSelected} />);
      await user.click(trigger());
      expect(selectAllButton()).toBeEnabled();
    });

    it('is disabled when there are no options at all', async () => {
      const user = userEvent.setup();
      render(<Harness options={[]} emptyKey="toolbar.labelEmpty" />);
      await user.click(trigger());
      expect(screen.getByText(i18n.t('toolbar.labelEmpty'))).toBeInTheDocument();
      expect(selectAllButton()).toBeDisabled();
    });

    it('moves focus to the first checkbox, so Escape still closes the panel', async () => {
      // Pressing it disables it; without moving focus it would fall to
      // <body>, outside the Escape handler (as with the clear button,
      // DFLT-00087).
      const user = userEvent.setup();
      render(<Harness />);

      await user.click(trigger());
      await user.click(selectAllButton());
      expect(selectAllButton()).toBeDisabled();
      expect(document.activeElement).not.toBe(document.body);
      expect(document.activeElement).toBe(screen.getAllByRole('checkbox')[0]);

      await user.keyboard('{Escape}');
      expect(screen.queryByRole('group')).not.toBeInTheDocument();
      expect(document.activeElement).toBe(trigger());
    });

    it('becomes pressable again when a new option appears after it was pressed', async () => {
      // The assignee options follow the 15-second poll: "select all" is a
      // snapshot, so an assignee who shows up later is not selected and the
      // button can add them. The selection itself is left as it was.
      const onChange = vi.fn();
      const user = userEvent.setup();
      const { rerender } = render(<Harness options={OPTIONS.slice(0, 2)} onChange={onChange} />);

      await user.click(trigger());
      await user.click(selectAllButton());
      expect(onChange).toHaveBeenLastCalledWith(['a', 'b']);
      expect(selectAllButton()).toBeDisabled();

      rerender(<Harness options={OPTIONS} onChange={onChange} />);
      expect(onChange).toHaveBeenCalledTimes(1);
      expect(screen.getByRole('checkbox', { name: 'う' })).not.toBeChecked();
      expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusSelected', { count: 2 }));
      expect(selectAllButton()).toBeEnabled();

      await user.click(selectAllButton());
      expect(onChange).toHaveBeenLastCalledWith(['a', 'b', 'c']);
      expect(selectAllButton()).toBeDisabled();
    });

    it('becomes pressable again once an option is unchecked, and clear still works', async () => {
      const onChange = vi.fn();
      const user = userEvent.setup();
      render(<Harness onChange={onChange} />);

      await user.click(trigger());
      await user.click(selectAllButton());
      await user.click(screen.getByRole('checkbox', { name: 'う' }));
      expect(onChange).toHaveBeenLastCalledWith(['a', 'b']);
      expect(selectAllButton()).toBeEnabled();

      await user.click(clearButton());
      expect(onChange).toHaveBeenLastCalledWith([]);
      expect(trigger()).toHaveTextContent(i18n.t('toolbar.statusAll'));
      expect(document.activeElement).toBe(trigger());
    });
  });

  it('closes on an outside click', async () => {
    const user = userEvent.setup();
    render(<Harness />);

    await user.click(trigger());
    await user.click(screen.getByTestId(`${KEYS.panelId}-overlay`));
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(trigger()).toHaveAttribute('aria-expanded', 'false');
  });

  it('closes on Escape and returns focus to the trigger', async () => {
    const user = userEvent.setup();
    render(<Harness />);

    await user.click(trigger());
    await user.click(screen.getByRole('checkbox', { name: 'あ' }));
    await user.keyboard('{Escape}');

    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(trigger()).toHaveAttribute('aria-expanded', 'false');
    expect(trigger()).toHaveFocus();
  });

  it('names a checkbox after its visible text unless an optionLabel is given', async () => {
    const user = userEvent.setup();
    render(
      <Harness
        options={[
          { value: 'plain', label: 'ふつう' },
          { value: 'chip', label: <span data-testid="chip">飾り</span>, optionLabel: '飾り' }
        ]}
      />
    );

    await user.click(trigger());
    const panel = screen.getByRole('group');
    // No aria-label at all on the plain option: the visible text is the
    // accessible name, so the two can never disagree (WCAG 2.5.3).
    expect(screen.getByRole('checkbox', { name: 'ふつう' })).not.toHaveAttribute('aria-label');
    expect(screen.getByRole('checkbox', { name: '飾り' })).toHaveAttribute('aria-label', '飾り');
    expect(within(panel).getByTestId('chip')).toBeInTheDocument();
  });

  it('draws the trigger\'s dropdown arrow in slate-500 (light) and slate-400 (dark) for WCAG 1.4.11', () => {
    render(<Harness />);
    const arrow = trigger().querySelector('svg.lucide-chevron-down');
    expect(arrow).not.toBeNull();
    expect(arrow).toHaveClass('text-slate-500', 'dark:text-slate-400');
    expect(arrow).not.toHaveClass('text-slate-400');
    expect(arrow).not.toHaveClass('dark:text-slate-500');
    expect(arrow).toHaveClass('w-3.5', 'h-3.5', 'shrink-0');
    expect(arrow).toHaveAttribute('aria-hidden', 'true');
  });

  // DFLT-00220: on a narrow screen the toolbar wraps and a filter can sit
  // near the right edge, where a left-hung panel widened the page. jsdom has
  // no layout, so the trigger's and panel's boxes and the window width are
  // stubbed; the real-browser measurements are in the ticket's notes.
  describe('panel alignment near the right edge of the window', () => {
    const PANEL_WIDTH = 224; // w-56 at the default 16px root font size
    let triggerLeft = 0;
    const TRIGGER_WIDTH = 110;

    beforeEach(() => {
      triggerLeft = 16;
      Object.defineProperty(document.documentElement, 'clientWidth', { configurable: true, get: () => 320 });
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
        if (this.dataset.testid === `${KEYS.panelId}-trigger`) {
          return DOMRect.fromRect({ x: triggerLeft, y: 0, width: TRIGGER_WIDTH, height: 30 });
        }
        if (this.id === KEYS.panelId) return DOMRect.fromRect({ x: 0, y: 0, width: PANEL_WIDTH, height: 200 });
        return DOMRect.fromRect({ x: 0, y: 0, width: 0, height: 0 });
      });
    });

    afterEach(() => {
      delete (document.documentElement as unknown as { clientWidth?: number }).clientWidth;
      vi.restoreAllMocks();
    });

    const panel = () => screen.getByRole('group');

    it('hangs the panel from the left edge of the trigger when it fits', async () => {
      const user = userEvent.setup();
      render(<Harness />);
      await user.click(trigger());
      expect(panel()).toHaveClass('left-0', 'w-56', 'max-w-[calc(100vw-2rem)]');
      expect(panel()).not.toHaveClass('right-0');
    });

    it('hangs it from the right edge when a left-hung panel would pass the window edge', async () => {
      const user = userEvent.setup();
      triggerLeft = 176; // 176 + 224 > 320, and 176 + 110 - 224 >= 0
      render(<Harness />);
      await user.click(trigger());
      expect(panel()).toHaveClass('right-0');
      expect(panel()).not.toHaveClass('left-0');
    });

    it('shifts it inside the window when neither edge has room', async () => {
      const user = userEvent.setup();
      triggerLeft = 100; // 100 + 224 > 320, but 100 + 110 - 224 < 0
      render(<Harness />);
      await user.click(trigger());
      expect(panel()).not.toHaveClass('left-0');
      expect(panel()).not.toHaveClass('right-0');
      // The offset is from the trigger's left edge: the panel's right edge
      // lands 16px inside the 320px window, and its left edge stays >= 0.
      const offset = parseFloat(panel().style.left);
      expect(offset).toBe(320 - 16 - PANEL_WIDTH - triggerLeft);
      expect(triggerLeft + offset).toBeGreaterThanOrEqual(0);
      expect(triggerLeft + offset + PANEL_WIDTH).toBeLessThanOrEqual(320);
    });

    it('re-decides on resize, with one listener per opening', async () => {
      const user = userEvent.setup();
      const add = vi.spyOn(window, 'addEventListener');
      const remove = vi.spyOn(window, 'removeEventListener');
      render(<Harness />);
      await user.click(trigger());
      expect(panel()).toHaveClass('left-0');

      // Re-renders while open (checking boxes) must not re-attach it.
      await user.click(screen.getByRole('checkbox', { name: 'あ' }));
      await user.click(screen.getByRole('checkbox', { name: 'あ' }));
      expect(add.mock.calls.filter(([type]) => type === 'resize')).toHaveLength(1);

      triggerLeft = 176;
      act(() => {
        window.dispatchEvent(new Event('resize'));
      });
      expect(panel()).toHaveClass('right-0');

      await user.click(trigger());
      expect(remove.mock.calls.filter(([type]) => type === 'resize')).toHaveLength(1);
    });

    it('re-decides while open when checking a box moves the trigger to another line', async () => {
      const user = userEvent.setup();
      triggerLeft = 176;
      // "All" -> "1 selected" widens the trigger, and the toolbar re-wraps
      // it to the start of the next line.
      render(<Harness onChange={() => { triggerLeft = 16; }} />);
      await user.click(trigger());
      expect(panel()).toHaveClass('right-0');

      await user.click(screen.getByRole('checkbox', { name: 'あ' }));
      expect(panel()).toHaveClass('left-0');
      expect(panel()).not.toHaveClass('right-0');
    });
  });
});

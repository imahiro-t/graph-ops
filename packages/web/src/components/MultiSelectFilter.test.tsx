// DFLT-00086: the control all four toolbar filters are built from, on its
// own. How each filter's selection narrows the ticket list -- and that all
// four really do use this component -- is covered by App.filters.test.tsx.
import { useState } from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
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
  onChange
}: {
  options?: MultiSelectFilterOption<string>[];
  emptyKey?: string;
  onChange?: (next: string[]) => void;
}) {
  const [selected, setSelected] = useState<string[]>([]);
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

    expect(trigger()).toHaveAttribute('aria-expanded', 'false');
    expect(trigger()).toHaveAttribute('aria-controls', KEYS.panelId);
    expect(screen.queryByRole('group')).not.toBeInTheDocument();

    await user.click(trigger());
    expect(trigger()).toHaveAttribute('aria-expanded', 'true');
    const panel = screen.getByRole('group', { name: i18n.t('toolbar.statusGroupLabel') });
    expect(panel).toHaveAttribute('id', KEYS.panelId);
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
});

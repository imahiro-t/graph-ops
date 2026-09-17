// DFLT-00084: the toolbar's label filter component on its own. How its
// selection narrows the ticket list is covered by App.labels.test.tsx.
//
// Since DFLT-00086 the open/close, checkbox and clear-button behaviour lives
// in MultiSelectFilter (see its own test); what is left to check here is
// that labels reach it as options with the chip as visible content and the
// label's name as the checkbox's accessible name.
import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Label } from '../types';
import { LabelFilter } from './LabelFilter';

const mk = (id: string, name: string, color: Label['color']): Label => ({
  id,
  project_id: 'proj-A',
  name,
  color,
  created_at: '',
  updated_at: ''
});
const LABELS = [mk('label-bug', 'バグ', 'red'), mk('label-feat', '機能追加', 'blue'), mk('label-ui', 'UI', 'purple')];

function Harness({ onChange }: { onChange?: (ids: string[]) => void }) {
  const [selected, setSelected] = useState<string[]>([]);
  return (
    <LabelFilter
      labels={LABELS}
      selectedIds={selected}
      onChange={next => {
        onChange?.(next);
        setSelected(next);
      }}
    />
  );
}

describe('LabelFilter', () => {
  it('starts as "all", selects several labels and updates the button text', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<Harness onChange={onChange} />);

    const button = screen.getByRole('button', { name: i18n.t('toolbar.labelAll') });
    expect(button).toHaveAttribute('aria-expanded', 'false');
    await user.click(button);
    expect(screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') })).toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: 'バグ' }));
    expect(onChange).toHaveBeenLastCalledWith(['label-bug']);
    expect(screen.getByRole('button', { name: i18n.t('toolbar.labelSelected', { count: 1 }) })).toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: '機能追加' }));
    expect(onChange).toHaveBeenLastCalledWith(['label-bug', 'label-feat']);
    expect(screen.getByRole('button', { name: i18n.t('toolbar.labelSelected', { count: 2 }) })).toBeInTheDocument();

    await user.click(screen.getByRole('checkbox', { name: '機能追加' }));
    expect(onChange).toHaveBeenLastCalledWith(['label-bug']);
    expect(screen.getByRole('button', { name: i18n.t('toolbar.labelSelected', { count: 1 }) })).toBeInTheDocument();
  });

  it('clears the selection with the clear button', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<Harness onChange={onChange} />);

    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.labelAll') }));
    await user.click(screen.getByRole('checkbox', { name: 'バグ' }));
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.filterClear') }));

    expect(onChange).toHaveBeenLastCalledWith([]);
    expect(screen.getByRole('button', { name: i18n.t('toolbar.labelAll') })).toBeInTheDocument();
    for (const box of screen.getAllByRole('checkbox')) {
      expect(box).not.toBeChecked();
    }
  });

  it('closes on Escape and returns focus to the trigger', async () => {
    const user = userEvent.setup();
    render(<Harness />);

    const button = screen.getByRole('button', { name: i18n.t('toolbar.labelAll') });
    await user.click(button);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    await user.keyboard('{Escape}');

    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    const updated = screen.getByRole('button', { name: i18n.t('toolbar.labelSelected', { count: 1 }) });
    expect(updated).toHaveAttribute('aria-expanded', 'false');
    expect(updated).toHaveFocus();
  });

  it('draws each option as a colored chip named after the label', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.labelAll') }));

    const chips = screen.getAllByTestId('label-chip');
    expect(chips.map(c => c.textContent)).toEqual(['バグ', '機能追加', 'UI']);
    expect(chips[0]).toHaveAttribute('data-label-color', 'red');
    // The chip is the visible content, so the checkbox needs the name
    // spelled out for it -- and it has to be exactly the visible text.
    expect(screen.getByRole('checkbox', { name: 'バグ' })).toBeInTheDocument();
  });

  it('says so when the project has no labels, with the clear button disabled', async () => {
    const user = userEvent.setup();
    render(<LabelFilter labels={[]} selectedIds={[]} onChange={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.labelAll') }));
    expect(screen.getByText(i18n.t('toolbar.labelEmpty'))).toBeInTheDocument();
    expect(screen.getByRole('button', { name: i18n.t('toolbar.filterClear') })).toBeDisabled();
  });
});

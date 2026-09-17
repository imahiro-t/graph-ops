// DFLT-00083: the priority badge shows one symbol (↑ / − / ↓) and carries
// its label in the tooltip and aria-label; its selector offers exactly
// HIGH/MEDIUM/LOW, with no "unset" choice.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketPriority } from '../types';
import { PrioritySelect } from './PrioritySelect';

const renderSelect = (priority: TicketPriority, onChange = vi.fn<(next: TicketPriority) => void>()) => {
  const { container } = render(<PrioritySelect ticketId="T-1" priority={priority} onChange={onChange} />);
  const select = container.querySelector('select') as HTMLSelectElement;
  const symbol = screen.getByTestId('priority-symbol');
  const badge = select.parentElement as HTMLElement;
  return { container, select, symbol, badge, onChange };
};

afterEach(async () => {
  await i18n.changeLanguage('ja');
});

describe('PrioritySelect', () => {
  it.each([
    ['HIGH', '↑', 'rose', '高'],
    ['MEDIUM', '−', 'amber', '中'],
    ['LOW', '↓', 'sky', '低']
  ] as const)('Japanese UI: %s shows %s in %s with tooltip/aria-label "%s"', (priority, symbol, color, label) => {
    const { select, symbol: symbolEl, badge } = renderSelect(priority);

    expect(symbolEl.textContent).toBe(symbol);
    expect(symbolEl).toHaveAttribute('aria-hidden', 'true');
    // The visible badge text is that one character and nothing else.
    expect(badge.firstElementChild).toBe(symbolEl);
    expect(badge.className).toContain(`bg-${color}-`);
    expect(badge.className).toContain(`text-${color}-`);
    expect(badge).toHaveAttribute('title', label);
    expect(select).toHaveAttribute('title', label);
    expect(select).toHaveAttribute('aria-label', `優先度: ${label}`);
    expect(screen.getByRole('combobox', { name: `優先度: ${label}` })).toBe(select);
  });

  it.each([
    ['HIGH', '↑', 'High'],
    ['MEDIUM', '−', 'Medium'],
    ['LOW', '↓', 'Low']
  ] as const)('English UI: %s shows %s with tooltip/aria-label "%s"', async (priority, symbol, label) => {
    await i18n.changeLanguage('en');
    const { select, symbol: symbolEl, badge } = renderSelect(priority);

    expect(symbolEl.textContent).toBe(symbol);
    expect(badge).toHaveAttribute('title', label);
    expect(select).toHaveAttribute('aria-label', `Priority: ${label}`);
  });

  it('draws only the MEDIUM symbol understated (font-normal), keeping the amber chip', () => {
    const medium = renderSelect('MEDIUM');
    expect(medium.symbol).toHaveClass('font-normal');
    expect(medium.badge.className).toContain('bg-amber-');
    medium.container.remove();

    for (const p of ['HIGH', 'LOW'] as const) {
      const { symbol, container } = renderSelect(p);
      expect(symbol).not.toHaveClass('font-normal');
      expect(symbol).toHaveClass('font-bold');
      container.remove();
    }
  });

  it('uses U+2212 for the MEDIUM symbol, never a hyphen', () => {
    const { symbol } = renderSelect('MEDIUM');
    expect(symbol.textContent?.codePointAt(0)).toBe(0x2212);
    expect(symbol.textContent).not.toContain('-');
  });

  it('offers exactly HIGH, MEDIUM, LOW with symbol + label, and no empty/unset option', () => {
    const { select } = renderSelect('MEDIUM');
    const options = Array.from(select.options);

    expect(options.map(o => o.value)).toEqual(['HIGH', 'MEDIUM', 'LOW']);
    expect(options.map(o => o.textContent)).toEqual(['↑ 高', '− 中', '↓ 低']);
    expect(options.some(o => o.value === '')).toBe(false);
    expect(options.some(o => o.textContent?.includes('未設定'))).toBe(false);
    expect(select.value).toBe('MEDIUM');
  });

  it('calls onChange with the chosen level', async () => {
    const user = userEvent.setup();
    const { select, onChange } = renderSelect('HIGH');

    await user.selectOptions(select, 'LOW');

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('LOW');
  });

  it('is a keyboard-focusable native select, with a focus ring drawn on the badge', async () => {
    const user = userEvent.setup();
    const { select, badge } = renderSelect('HIGH');

    await user.tab();

    expect(select.tagName).toBe('SELECT');
    expect(document.activeElement).toBe(select);
    expect(badge).toHaveClass('focus-within:ring-2');
  });

  it('renders an unexpected value as MEDIUM without crashing', () => {
    const { select, symbol, badge } = renderSelect('' as TicketPriority);

    expect(symbol.textContent).toBe('−');
    expect(badge.className).toContain('bg-amber-');
    expect(select.value).toBe('MEDIUM');
  });
});

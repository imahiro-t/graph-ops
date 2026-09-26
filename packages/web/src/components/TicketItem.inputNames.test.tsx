// DFLT-00205: the close-reason input and the custom Claude prompt textarea
// have a persistent accessible name of their own (aria-label from i18n), so
// they stay findable by name after typing replaces the placeholder.
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const makeTicket = (): TicketDetail => ({
  id: 'TEST-00205',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  artifacts: []
});

const renderTicket = (isExpanded: boolean) =>
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded={isExpanded}
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn() as () => Promise<void>}
      myName=""
      projectLabels={[]}
    />
  );

beforeEach(() => {
  // Expanded rows may fetch (autopilot decisions etc.); keep them quiet.
  vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('TicketItem input accessible names (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('names the close-reason input apart from its placeholder, before and after typing', () => {
    renderTicket(false);
    fireEvent.click(screen.getByRole('button', { name: i18n.t('ticketItem.close.button') }));

    const name = i18n.t('ticketItem.close.reasonInputLabel');
    expect(name).not.toBe(i18n.t('ticketItem.close.reasonPlaceholder'));

    const input = screen.getByRole('textbox', { name });
    expect(input).toHaveAttribute('placeholder', i18n.t('ticketItem.close.reasonPlaceholder'));
    expect(input).not.toHaveAttribute('aria-required');

    fireEvent.change(input, { target: { value: '重複のため' } });
    const after = screen.getByRole('textbox', { name });
    expect(after).toBe(input);
    expect(after).toHaveValue('重複のため');
  });

  it('names the custom prompt textarea apart from its placeholder, before and after typing', () => {
    renderTicket(true);

    const name = i18n.t('ticketItem.promptLabel');
    expect(name).not.toBe(i18n.t('ticketItem.promptPlaceholder'));

    const textarea = screen.getByRole('textbox', { name });
    expect(textarea.tagName).toBe('TEXTAREA');
    expect(textarea).toHaveAttribute('placeholder', i18n.t('ticketItem.promptPlaceholder'));

    fireEvent.change(textarea, { target: { value: '続きを進めて' } });
    const after = screen.getByRole('textbox', { name });
    expect(after).toBe(textarea);
    expect(after).toHaveValue('続きを進めて');
  });
});

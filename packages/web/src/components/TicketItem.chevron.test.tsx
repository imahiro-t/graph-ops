// DFLT-00152: the ticket list's two chevrons (the header row's and the
// expanded panel's node-row one) are named buttons (type="button", an
// accessible name with the ticket/node ID via i18n, aria-expanded matching
// the row) with aria-hidden icons, and activating one -- by mouse or by
// Enter/Space -- toggles its row exactly once. The chevrons have no onClick
// of their own; their click bubbles to the row's toggle handler.
//
// Keyboard activation goes through user-event (fireEvent.keyDown alone does
// not make jsdom synthesize the button's click). This file does not touch the
// clipboard, so userEvent.setup()'s clipboard stub is harmless here.
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import ja from '../i18n/locales/ja/translation.json';
import en from '../i18n/locales/en/translation.json';
import { GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const TICKET_ID = 'TEST-00152';

const makeNode = (id: string, name: string): GraphNode => ({
  id,
  ticket_id: TICKET_ID,
  name,
  type: 'plan',
  status: 'TODO',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
});

const NODE_1 = makeNode('TEST-00152-01', '計画作成');
const NODE_2 = makeNode('TEST-00152-02', '計画レビュー');

const makeTicket = (): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: 'シェブロンのテスト',
  description: '説明',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [NODE_1, NODE_2],
  edges: [],
  artifacts: []
});

const ticketToggleName = () => i18n.t('ticketItem.toggleTicket', { id: TICKET_ID });
const nodeToggleName = (id: string) => i18n.t('ticketItem.toggleNode', { id });

const headerChevron = () => screen.getByRole('button', { name: ticketToggleName() });
const nodeChevron = (id: string) => screen.getByRole('button', { name: nodeToggleName(id) });

const renderItem = (isExpanded: boolean) => {
  const onToggleExpand = vi.fn();
  const utils = render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded={isExpanded}
      onToggleExpand={onToggleExpand}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );
  return { ...utils, onToggleExpand };
};

// Holds isExpanded in state the way the ticket list does.
const StatefulItem: React.FC = () => {
  const [isExpanded, setIsExpanded] = useState(false);
  return (
    <TicketItem
      ticket={makeTicket()}
      isExpanded={isExpanded}
      onToggleExpand={() => setIsExpanded(v => !v)}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );
};

const expectIconsHidden = (button: HTMLElement) => {
  const icons = button.querySelectorAll('svg');
  expect(icons.length).toBeGreaterThan(0);
  icons.forEach(icon => expect(icon).toHaveAttribute('aria-hidden', 'true'));
};

// TicketItem measures its graph panel with a ResizeObserver when expanded,
// which jsdom does not implement (same stub as TicketItem.labels.test.tsx).
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.getSelection()?.removeAllRanges();
});

describe('TicketItem header chevron', () => {
  it('is a type="button" with an accessible name containing the ticket ID and aria-expanded matching the row', () => {
    const { rerender, onToggleExpand } = renderItem(false);
    expect(ticketToggleName()).toContain(TICKET_ID);
    let chevron = headerChevron();
    expect(chevron).toBe(screen.getByTestId('ticket-toggle-expand'));
    expect(chevron).toHaveAttribute('type', 'button');
    expect(chevron).toHaveAttribute('aria-expanded', 'false');
    expectIconsHidden(chevron);

    rerender(
      <TicketItem
        ticket={makeTicket()}
        isExpanded={true}
        onToggleExpand={onToggleExpand}
        onRefresh={vi.fn()}
        myName=""
        projectLabels={[]}
      />
    );
    chevron = headerChevron();
    expect(chevron).toHaveAttribute('type', 'button');
    expect(chevron).toHaveAttribute('aria-expanded', 'true');
    expectIconsHidden(chevron);
  });

  it('toggles the row exactly once on a mouse click', () => {
    const { onToggleExpand } = renderItem(false);
    const chevron = headerChevron();
    fireEvent.mouseDown(chevron, { detail: 1 });
    fireEvent.click(chevron, { detail: 1 });
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
  });

  it('toggles the row exactly once per Enter and per Space', async () => {
    const user = userEvent.setup();
    const { onToggleExpand } = renderItem(false);
    headerChevron().focus();
    expect(headerChevron()).toHaveFocus();

    await user.keyboard('{Enter}');
    expect(onToggleExpand).toHaveBeenCalledTimes(1);
    await user.keyboard(' ');
    expect(onToggleExpand).toHaveBeenCalledTimes(2);
  });

  it('flips aria-expanded on each click when the row state is held by the parent', async () => {
    const user = userEvent.setup();
    render(<StatefulItem />);
    expect(headerChevron()).toHaveAttribute('aria-expanded', 'false');

    fireEvent.mouseDown(headerChevron(), { detail: 1 });
    fireEvent.click(headerChevron(), { detail: 1 });
    expect(headerChevron()).toHaveAttribute('aria-expanded', 'true');

    fireEvent.mouseDown(headerChevron(), { detail: 1 });
    fireEvent.click(headerChevron(), { detail: 1 });
    expect(headerChevron()).toHaveAttribute('aria-expanded', 'false');

    headerChevron().focus();
    await user.keyboard('{Enter}');
    expect(headerChevron()).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard(' ');
    expect(headerChevron()).toHaveAttribute('aria-expanded', 'false');
  });

  it('shows a focus-visible ring (lighter blue in dark mode) and no ring on a plain focus, keeping its colours', () => {
    for (const isExpanded of [false, true]) {
      const { unmount } = renderItem(isExpanded);
      const tokens = headerChevron().className.split(/\s+/);
      for (const cls of [
        'rounded',
        'focus:outline-none',
        'focus-visible:ring-2',
        'focus-visible:ring-blue-500',
        'dark:focus-visible:ring-blue-400',
        // The existing colours, hover colours and shrink-0 stay.
        'text-slate-500',
        'dark:text-slate-400',
        'hover:text-slate-600',
        'dark:hover:text-slate-300',
        'shrink-0',
      ]) {
        expect(tokens).toContain(cls);
      }
      // Only focus-visible draws a ring, so a mouse click shows none.
      expect(tokens.filter(c => c.startsWith('focus:ring') || c.startsWith('dark:focus:ring'))).toEqual([]);
      unmount();
    }
  });
});

describe('TicketItem node-row chevron', () => {
  it('is a type="button" with an accessible name containing the node ID, collapsed at first, with hidden icons', () => {
    renderItem(true);
    for (const node of [NODE_1, NODE_2]) {
      expect(nodeToggleName(node.id)).toContain(node.id);
      const chevron = nodeChevron(node.id);
      expect(chevron).toBe(screen.getByTestId(`node-toggle-expand-${node.id}`));
      expect(chevron).toHaveAttribute('type', 'button');
      expect(chevron).toHaveAttribute('aria-expanded', 'false');
      expectIconsHidden(chevron);
    }
    // Distinct from the header chevron's name.
    expect(nodeToggleName(NODE_1.id)).not.toBe(ticketToggleName());
  });

  it('switches the node exactly once per mouse click, and only that node', () => {
    const { onToggleExpand } = renderItem(true);
    fireEvent.click(nodeChevron(NODE_1.id), { detail: 1 });
    expect(nodeChevron(NODE_1.id)).toHaveAttribute('aria-expanded', 'true');
    expectIconsHidden(nodeChevron(NODE_1.id));
    expect(nodeChevron(NODE_2.id)).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(nodeChevron(NODE_1.id), { detail: 1 });
    expect(nodeChevron(NODE_1.id)).toHaveAttribute('aria-expanded', 'false');
    // A node-row click never toggles the ticket row.
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it('switches the node exactly once per Enter and per Space', async () => {
    const user = userEvent.setup();
    renderItem(true);
    nodeChevron(NODE_1.id).focus();
    expect(nodeChevron(NODE_1.id)).toHaveFocus();

    await user.keyboard('{Enter}');
    expect(nodeChevron(NODE_1.id)).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard(' ');
    expect(nodeChevron(NODE_1.id)).toHaveAttribute('aria-expanded', 'false');
  });

  it('still toggles the node on a click elsewhere in its row', () => {
    renderItem(true);
    const nodeIdEl = screen.getByText(NODE_1.id);
    fireEvent.click(nodeIdEl, { detail: 1 });
    expect(nodeChevron(NODE_1.id)).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(nodeIdEl, { detail: 1 });
    expect(nodeChevron(NODE_1.id)).toHaveAttribute('aria-expanded', 'false');
  });

  it('shows a focus-visible ring (lighter blue in dark mode) and no ring on a plain focus', () => {
    renderItem(true);
    for (const node of [NODE_1, NODE_2]) {
      const tokens = nodeChevron(node.id).className.split(/\s+/);
      for (const cls of [
        'rounded',
        'focus:outline-none',
        'focus-visible:ring-2',
        'focus-visible:ring-blue-500',
        'dark:focus-visible:ring-blue-400',
      ]) {
        expect(tokens).toContain(cls);
      }
      // Only focus-visible draws a ring, so a mouse click shows none.
      expect(tokens.filter(c => c.startsWith('focus:ring') || c.startsWith('dark:focus:ring'))).toEqual([]);
    }
  });
});

describe('Chevron translations', () => {
  it('define non-empty toggleTicket / toggleNode texts with {{id}} in ja and en', () => {
    for (const locale of [ja, en]) {
      const ticketItem = (locale as { ticketItem: Record<string, unknown> }).ticketItem;
      for (const key of ['toggleTicket', 'toggleNode']) {
        const text = ticketItem[key];
        expect(typeof text).toBe('string');
        expect((text as string).trim()).not.toBe('');
        expect(text as string).toContain('{{id}}');
      }
      expect(ticketItem.toggleTicket).not.toBe(ticketItem.toggleNode);
    }
  });
});

// DFLT-00327: a node somebody is running shows who, since when, and when
// their session last answered -- on a line of its own under the node's row,
// which wraps anywhere, so it holds at 320px with 200% text.
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatAgo } from '../i18n/formatDate';
import { GraphNode, TicketDetail } from '../types';
import { NodeClaimLine } from './NodeClaimLine';
import { TicketItem } from './TicketItem';

const T = 'TEST-00327';
const NOW = Date.parse('2026-09-29T12:00:00Z');

const node = (id: string, status: GraphNode['status'], extra: Partial<GraphNode> = {}): GraphNode => ({
  id,
  ticket_id: T,
  name: `node ${id}`,
  type: 'implementation',
  status,
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-09-29T11:00:00Z',
  updated_at: '2026-09-29T11:00:00Z',
  ...extra
});

const claimed = (extra: Partial<GraphNode> = {}) =>
  node(`${T}-01`, 'IN PROGRESS', {
    claimed_by_name: 'Alice',
    claimed_at: '2026-09-29T11:55:00Z',
    claim_heartbeat: '2026-09-29T11:58:30Z',
    claim_lease: 'live',
    ...extra
  });

describe('NodeClaimLine', () => {
  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('names the claimer with claim and heartbeat times (ja)', async () => {
    await i18n.changeLanguage('ja');
    render(<NodeClaimLine node={claimed()} now={NOW} />);
    expect(screen.getByTestId(`node-claim-${T}-01`).textContent).toBe('Alice が実行中 · 取得 5 分前 · 最終応答 1 分前');
  });

  it('marks a stand-in name and a silent session (en)', async () => {
    await i18n.changeLanguage('en');
    render(
      <NodeClaimLine
        node={claimed({ claimed_by_name: 'taro@mac01', claimed_by_name_is_fallback: true, claim_lease: 'expired', claim_heartbeat: '2026-09-29T09:30:00Z' })}
        now={NOW}
      />
    );
    const line = screen.getByTestId(`node-claim-${T}-01`).textContent;
    expect(line).toContain(i18n.t('autopilot.fallbackName', { name: 'taro@mac01' }));
    expect(line).toContain('last heartbeat 2 h ago');
    expect(line).toContain('not responding');
  });

  it('shows nothing for a node nobody claimed by name, or one that is not running', () => {
    const { container } = render(
      <>
        <NodeClaimLine node={node('a', 'IN PROGRESS', { claim_lease: 'legacy' })} now={NOW} />
        <NodeClaimLine node={node('b', 'TODO', { claimed_by_name: 'Alice' })} now={NOW} />
      </>
    );
    expect(container.textContent).toBe('');
  });

  it('wraps anywhere, so it holds at 320px with 200% text', () => {
    render(<NodeClaimLine node={claimed({ claimed_by_name: 'x'.repeat(128) })} now={NOW} />);
    expect(screen.getByTestId(`node-claim-${T}-01`).className).toContain('wrap-anywhere');
  });
});

describe('formatAgo', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('ja');
  });
  it('says just now, minutes, then hours', () => {
    const t = i18n.t.bind(i18n);
    expect(formatAgo(t, '2026-09-29T11:59:30Z', NOW)).toBe('たった今');
    expect(formatAgo(t, '2026-09-29T11:01:00Z', NOW)).toBe('59 分前');
    expect(formatAgo(t, '2026-09-29T10:00:00Z', NOW)).toBe('2 時間前');
    expect(formatAgo(t, 'not a date', NOW)).toBe('');
  });
});

describe('TicketItem node rows', () => {
  it('show the claim line under a running node', async () => {
    await i18n.changeLanguage('ja');
    // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
    vi.useFakeTimers({ now: NOW, toFake: ['Date'] });
    try {
      const ticket: TicketDetail = {
        id: T,
        project_id: 'proj-1',
        title: 't',
        description: '',
        status: 'IN PROGRESS',
        auto_executable: true,
        blocked: false,
        created_at: '2026-09-29T11:00:00Z',
        updated_at: '2026-09-29T11:00:00Z',
        priority: 'MEDIUM',
        labels: [],
        nodes: [claimed(), node(`${T}-02`, 'TODO')],
        edges: [],
        artifacts: []
      };
      render(
        <TicketItem ticket={ticket} isExpanded onToggleExpand={vi.fn()} onRefresh={vi.fn(async () => {})} myName="" projectLabels={[]} />
      );
      expect(screen.getByTestId(`node-claim-${T}-01`).textContent).toContain('Alice が実行中');
      expect(screen.queryByTestId(`node-claim-${T}-02`)).toBeNull();
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });
});

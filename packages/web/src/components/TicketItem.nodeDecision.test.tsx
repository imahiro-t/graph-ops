// DFLT-00329: a manual node that has been decided shows who decided it and
// when -- on a line of its own under the node's row, which wraps anywhere,
// so it holds at 320px with 200% text.
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatDateTime } from '../i18n/formatDate';
import { translateErrorCode } from '../lib/apiError';
import { GraphNode, TicketDetail } from '../types';
import { NodeDecisionLine } from './NodeDecisionLine';
import { TicketItem } from './TicketItem';

const T = 'TEST-00329';
const AT = '2026-09-29T12:34:00Z';

const node = (id: string, type: GraphNode['type'], status: GraphNode['status'], extra: Partial<GraphNode> = {}): GraphNode => ({
  id,
  ticket_id: T,
  name: `node ${id}`,
  type,
  status,
  iteration_count: 0,
  max_iterations: 3,
  is_manual: true,
  created_at: '2026-09-29T11:00:00Z',
  updated_at: '2026-09-29T11:00:00Z',
  ...extra
});

const decided = (type: GraphNode['type'], status: GraphNode['status'], extra: Partial<GraphNode> = {}) =>
  node(`${T}-01`, type, status, { decided_by_name: 'Alice', decided_at: AT, ...extra });

const line = () => screen.getByTestId(`node-decision-${T}-01`).textContent;

describe('NodeDecisionLine', () => {
  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('names who approved or rejected an approval gate, with the time (ja)', async () => {
    await i18n.changeLanguage('ja');
    const when = formatDateTime(AT, 'ja');
    const { unmount } = render(<NodeDecisionLine node={decided('approval_gate', 'DONE')} />);
    expect(line()).toBe(`承認: Alice · ${when}`);
    unmount();
    render(<NodeDecisionLine node={decided('approval_gate', 'REJECTED')} />);
    expect(line()).toBe(`却下: Alice · ${when}`);
  });

  it('says "completed" for a release and other manual nodes (en)', async () => {
    await i18n.changeLanguage('en');
    const { unmount } = render(<NodeDecisionLine node={decided('release', 'DONE')} />);
    expect(line()).toBe(`Completed by Alice · ${formatDateTime(AT, 'en')}`);
    unmount();
    render(<NodeDecisionLine node={decided('sign_off' as GraphNode['type'], 'AWAITING FIX')} />);
    expect(line()).toContain('Sent back by Alice');
  });

  it('marks a stand-in name and an autopilot decision', async () => {
    await i18n.changeLanguage('ja');
    render(<NodeDecisionLine node={decided('approval_gate', 'DONE', { decided_by_name: 'taro@mac01', decided_by_name_is_fallback: true, decided_by_autopilot: true })} />);
    expect(line()).toContain(i18n.t('autopilot.fallbackName', { name: 'taro@mac01' }));
    expect(line()).toContain('（オートパイロット）');
  });

  it('leaves out a time that does not parse', async () => {
    await i18n.changeLanguage('en');
    render(<NodeDecisionLine node={decided('approval_gate', 'DONE', { decided_at: 'not a time' })} />);
    expect(line()).toBe('Approved by Alice');
  });

  it('shows nothing for an undecided, automatic or rewound node', () => {
    const { container } = render(
      <>
        <NodeDecisionLine node={node('a', 'approval_gate', 'TODO')} />
        <NodeDecisionLine node={node('b', 'approval_gate', 'DONE')} />
        <NodeDecisionLine node={node('c', 'review_gate', 'DONE', { is_manual: false, decided_by_name: 'Alice' })} />
        <NodeDecisionLine node={node('d', 'approval_gate', 'TODO', { decided_by_name: 'Alice' })} />
      </>
    );
    expect(container.textContent).toBe('');
  });

  it('wraps anywhere, so it holds at 320px with 200% text', () => {
    render(<NodeDecisionLine node={decided('approval_gate', 'DONE', { decided_by_name: 'x'.repeat(128) })} />);
    expect(screen.getByTestId(`node-decision-${T}-01`).className).toContain('wrap-anywhere');
  });
});

describe('TicketItem', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })));
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows the decision under the decided node', () => {
    const ticket: TicketDetail = {
      id: T,
      project_id: 'proj-1',
      title: 'タイトル',
      description: '説明',
      status: 'IN PROGRESS',
      auto_executable: true,
      blocked: false,
      created_at: '2026-09-29T11:00:00Z',
      updated_at: '2026-09-29T11:00:00Z',
      priority: 'MEDIUM',
      labels: [],
      nodes: [decided('approval_gate', 'DONE')],
      edges: [],
      artifacts: []
    };
    render(<TicketItem ticket={ticket} isExpanded onToggleExpand={vi.fn()} onRefresh={vi.fn() as () => Promise<void>} myName="" projectLabels={[]} />);
    expect(line()).toContain('Alice');
  });
});

describe('conflict messages', () => {
  it('say another member may have decided first, and that a write conflict can be retried', async () => {
    for (const lng of ['ja', 'en']) {
      await i18n.changeLanguage(lng);
      const t = i18n.t.bind(i18n);
      expect(translateErrorCode(t, 'INVALID_NODE_STATE')).toMatch(lng === 'ja' ? /別のメンバー/ : /another member/);
      expect(translateErrorCode(t, 'CONCURRENT_WRITE_CONFLICT')).not.toBe(translateErrorCode(t, 'UNKNOWN'));
      expect(translateErrorCode(t, 'CONCURRENT_WRITE_CONFLICT')).toMatch(lng === 'ja' ? /もう一度/ : /try again/);
    }
    await i18n.changeLanguage('ja');
  });
});

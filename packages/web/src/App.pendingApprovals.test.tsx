// DFLT-00144: the project switcher's menu badges every project that has
// tickets awaiting approval with their count, refetched each time the menu
// opens, and stays fully usable when that count cannot be had.
//
// fetch is served by test/fakeBackend.ts; each test sets what
// GET /api/projects/pending-approvals answers through
// backend.pendingApprovals.
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, FakePendingApprovals, createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { PENDING_APPROVAL_BADGE_CLASSES } from './components/PendingApprovalBadge';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'AAA', local_path: '/work/alpha', created_at: '', updated_at: '' };
const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BBB', local_path: '/work/beta', created_at: '', updated_at: '' };
const gamma: Project = { id: 'p-gamma', name: 'Gamma', prefix: 'CCC', local_path: '/work/gamma', created_at: '', updated_at: '' };

const PATH = '/api/projects/pending-approvals';

let backend: FakeBackend;
let fetchMock: ReturnType<typeof vi.fn>;

function answer(counts: unknown): FakePendingApprovals {
  return () => ({ status: 200, body: { counts } });
}

function seed(pendingApprovals: FakePendingApprovals = answer({ [alpha.id]: 2, [beta.id]: 1 })) {
  backend = createFakeBackend({
    projects: [alpha, beta, gamma],
    currentProjectId: alpha.id,
    labels: [],
    tickets: [
      { id: 'AAA-00001', project_id: alpha.id, title: 'Alpha のチケット', status: 'TODO', priority: 'HIGH', labelIds: [] },
      { id: 'BBB-00001', project_id: beta.id, title: 'Beta のチケット', status: 'TODO', priority: 'MEDIUM', labelIds: [] }
    ],
    pendingApprovals
  });
  fetchMock = installFakeBackend(backend);
}

// A pending-approvals answer the test releases by hand.
function held() {
  let release!: (res: Response) => void;
  const promise = new Promise<Response>(resolve => {
    release = resolve;
  });
  return { promise, release };
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status });
}

function countRequests(): number {
  return fetchMock.mock.calls.filter(c => String(c[0]) === PATH).length;
}

// The header's switcher button: named by the current project alone.
function switcher() {
  return screen.getByRole('button', { name: 'Alpha' });
}

// A menu item: its name starts with the project's name and ends with its
// prefix, whatever badge sits in between.
function menuItem(p: Project) {
  return screen.getByRole('button', { name: new RegExp(`^${p.name}\\b.*${p.prefix}$`) });
}

function badgeIn(p: Project) {
  return within(menuItem(p)).queryByRole('img');
}

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('AAA-00001');
  return user;
}

describe('project switcher pending-approval badges', () => {
  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  describe('with counts for Alpha (2) and Beta (1) and none for Gamma', () => {
    beforeEach(() => seed());

    it('badges only the projects with tickets awaiting approval', async () => {
      const user = await renderApp();
      await user.click(switcher());

      expect(await within(menuItem(alpha)).findByRole('img')).toHaveTextContent('2');
      expect(badgeIn(beta)).toHaveTextContent('1');
      expect(badgeIn(gamma)).toBeNull();
    });

    it('never fetches or badges while the menu is closed', async () => {
      await renderApp();
      expect(countRequests()).toBe(0);
      expect(within(switcher()).queryByRole('img')).toBeNull();
    });

    it('names the badge with the count, in Japanese', async () => {
      const user = await renderApp();
      await user.click(switcher());

      const badge = await within(menuItem(alpha)).findByRole('img', { name: '承認待ち 2 件' });
      expect(badge).toHaveAttribute('title', '承認待ち 2 件');
      expect(i18n.t('projectSwitcher.pendingApprovals', { count: 2 })).toBe('承認待ち 2 件');
    });

    it('names the badge with the count, in English singular and plural', async () => {
      await i18n.changeLanguage('en');
      const user = await renderApp();
      await user.click(switcher());

      expect(await within(menuItem(alpha)).findByRole('img', { name: '2 tickets awaiting approval' })).toBeInTheDocument();
      expect(within(menuItem(beta)).getByRole('img', { name: '1 ticket awaiting approval' })).toBeInTheDocument();
    });

    it('folds the count into the menu item name between the project name and its prefix', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await within(menuItem(alpha)).findByRole('img');

      expect(screen.getByRole('button', { name: 'Alpha 承認待ち 2 件 AAA' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Gamma CCC' })).toBeInTheDocument();
      // The existing tests' way of finding an item still finds it.
      expect(screen.getAllByRole('button', { name: /Gamma/ })).toHaveLength(1);
    });

    it('puts the badge right before the prefix, both inside the right-aligned wrapper', async () => {
      const user = await renderApp();
      await user.click(switcher());
      const badge = await within(menuItem(alpha)).findByRole('img');

      const wrapper = badge.parentElement!;
      expect(wrapper).toHaveClass('ml-auto', 'shrink-0');
      expect(wrapper.children).toHaveLength(2);
      expect(wrapper.children[0]).toBe(badge);
      expect(wrapper.children[1]).toHaveTextContent('AAA');
      expect(wrapper.children[1]).not.toHaveClass('ml-auto');
      // The project name comes first, outside the wrapper.
      expect(wrapper.previousElementSibling).toHaveTextContent('Alpha');

      const gammaPrefix = within(menuItem(gamma)).getByText('CCC');
      expect(gammaPrefix.parentElement).toHaveClass('ml-auto', 'shrink-0');
      expect(gammaPrefix.parentElement!.children).toHaveLength(1);
    });

    it('colors the badge with opaque light/dark pairs and no blink', async () => {
      const user = await renderApp();
      await user.click(switcher());
      const badge = await within(menuItem(alpha)).findByRole('img');

      expect(badge).toHaveClass('bg-amber-100', 'text-amber-800', 'dark:bg-amber-900', 'dark:text-amber-100');
      expect(PENDING_APPROVAL_BADGE_CLASSES.split(' ').sort()).toEqual(
        ['bg-amber-100', 'dark:bg-amber-900', 'dark:text-amber-100', 'text-amber-800'].sort()
      );
      for (const cls of Array.from(badge.classList)) {
        expect(cls).not.toMatch(/\/|opacity|animate-/);
      }
    });

    it('refetches the counts every time the menu opens, not when it closes', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await within(menuItem(alpha)).findByRole('img', { name: '承認待ち 2 件' });
      expect(countRequests()).toBe(1);

      await user.click(switcher());
      expect(screen.queryByRole('button', { name: /^Gamma/ })).not.toBeInTheDocument();
      expect(countRequests()).toBe(1);

      backend.pendingApprovals = answer({ [alpha.id]: 5 });
      await user.click(switcher());
      expect(await within(menuItem(alpha)).findByRole('img', { name: '承認待ち 5 件' })).toHaveTextContent('5');
      expect(countRequests()).toBe(2);
      expect(badgeIn(beta)).toBeNull();
    });
  });

  it('does not render a badge for a zero count', async () => {
    seed(answer({ [alpha.id]: 2, [gamma.id]: 0 }));
    const user = await renderApp();
    await user.click(switcher());
    await within(menuItem(alpha)).findByRole('img');

    expect(badgeIn(gamma)).toBeNull();
  });

  it('ignores counts that are not positive integers', async () => {
    seed(answer({ [alpha.id]: 2, [beta.id]: 1, [gamma.id]: -3 }));
    const user = await renderApp();
    await user.click(switcher());
    await within(menuItem(alpha)).findByRole('img');
    expect(badgeIn(beta)).toHaveTextContent('1');
    expect(badgeIn(gamma)).toBeNull();

    await user.click(switcher());
    for (const bad of [1.5, '4', null, true]) {
      backend.pendingApprovals = answer({ [alpha.id]: 2, [gamma.id]: bad });
      await user.click(switcher());
      await within(menuItem(alpha)).findByRole('img');
      expect(badgeIn(gamma)).toBeNull();
      await user.click(switcher());
    }
  });

  it('clears the previous counts on reopen until the new answer arrives', async () => {
    seed();
    const user = await renderApp();
    await user.click(switcher());
    await within(menuItem(alpha)).findByRole('img', { name: '承認待ち 2 件' });
    await user.click(switcher());

    const second = held();
    backend.pendingApprovals = () => second.promise;
    await user.click(switcher());
    expect(badgeIn(alpha)).toBeNull();
    expect(badgeIn(beta)).toBeNull();

    await act(async () => second.release(jsonResponse({ counts: { [alpha.id]: 3 } })));
    expect(await within(menuItem(alpha)).findByRole('img', { name: '承認待ち 3 件' })).toBeInTheDocument();
  });

  it('keeps the menu usable while the counts are still loading', async () => {
    const pending = held();
    seed(() => pending.promise);
    const user = await renderApp();
    await user.click(switcher());

    expect(menuItem(alpha)).toBeInTheDocument();
    expect(menuItem(beta)).toBeInTheDocument();
    expect(menuItem(gamma)).toBeInTheDocument();
    expect(screen.queryAllByRole('img')).toHaveLength(0);

    await user.click(menuItem(beta));
    expect(await screen.findByText('BBB-00001')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Beta' })).toBeInTheDocument();
  });

  it('opens "New project..." while the counts are still loading', async () => {
    const pending = held();
    seed(() => pending.promise);
    const user = await renderApp();
    await user.click(switcher());

    await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.createNew') }));
    expect(await screen.findByRole('dialog', { name: i18n.t('createProjectModal.title') })).toBeInTheDocument();
  });

  it('drops an older answer that arrives after a newer one', async () => {
    const first = held();
    seed(() => first.promise);
    const user = await renderApp();
    await user.click(switcher());
    await user.click(switcher());

    backend.pendingApprovals = answer({ [alpha.id]: 7 });
    await user.click(switcher());
    expect(await within(menuItem(alpha)).findByRole('img', { name: '承認待ち 7 件' })).toBeInTheDocument();

    await act(async () => first.release(jsonResponse({ counts: { [alpha.id]: 2 } })));
    // Give the stale answer every chance to land.
    await act(async () => {
      await new Promise(r => setTimeout(r, 0));
    });
    expect(badgeIn(alpha)).toHaveTextContent('7');
  });

  // How each failure shows that the app has finished handling it, so the
  // "no badge" checks below look at the menu after the answer was processed,
  // not at the counts cleared when the menu opened: a failure that throws
  // (a non-2xx status, a network error, a body that is not JSON) is logged;
  // a malformed 200 is read to the end and parsed to no counts, silently.
  let bodyRead: ReturnType<typeof held>;

  function malformed200(body: unknown): FakePendingApprovals {
    return () => {
      const res = jsonResponse(body);
      const json = res.json.bind(res);
      res.json = async () => {
        try {
          return await json();
        } finally {
          bodyRead.release(res);
        }
      };
      return res;
    };
  }

  async function answerHandled(handled: 'logged' | 'parsed') {
    if (handled === 'logged') {
      await waitFor(() =>
        expect(console.error).toHaveBeenCalledWith('Failed to load pending approval counts', expect.anything())
      );
      return;
    }
    await act(async () => {
      await bodyRead.promise;
      // Let the parsed counts reach the state and the render.
      await new Promise(r => setTimeout(r, 0));
    });
    expect(console.error).not.toHaveBeenCalledWith('Failed to load pending approval counts', expect.anything());
  }

  describe.each<[string, FakePendingApprovals, 'logged' | 'parsed']>([
    ['a 500', () => ({ status: 500, body: { error: { code: 'INTERNAL', message: 'boom' } } }), 'logged'],
    [
      'a network error',
      () => {
        throw new TypeError('Failed to fetch');
      },
      'logged'
    ],
    ['a body that is not JSON', () => new Response('<html>oops</html>', { status: 200 }), 'logged'],
    ['a 200 without "counts"', malformed200({}), 'parsed'],
    ['a 200 with "counts": null', malformed200({ counts: null }), 'parsed']
  ])('when the counts request fails with %s', (_, failing, handled) => {
    beforeEach(() => {
      bodyRead = held();
      seed(failing);
      vi.spyOn(console, 'error').mockImplementation(() => {});
    });

    it('still lists every project, with no badge, and switches projects', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await waitFor(() => expect(countRequests()).toBe(1));
      await answerHandled(handled);

      expect(menuItem(alpha)).toBeInTheDocument();
      expect(menuItem(beta)).toBeInTheDocument();
      expect(menuItem(gamma)).toBeInTheDocument();
      expect(screen.queryAllByRole('img')).toHaveLength(0);

      await user.click(menuItem(beta));
      expect(await screen.findByText('BBB-00001')).toBeInTheDocument();
    });

    it('still opens "New project..."', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await waitFor(() => expect(countRequests()).toBe(1));
      await answerHandled(handled);

      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.createNew') }));
      expect(await screen.findByRole('dialog', { name: i18n.t('createProjectModal.title') })).toBeInTheDocument();
    });
  });
});

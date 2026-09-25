// DFLT-00155: the header's project switcher tells assistive technology
// whether its popup is open (aria-expanded), that it opens one
// (aria-haspopup="dialog", matching the popup's role="dialog"), and Escape
// closes the open popup and puts focus back on the button.
//
// fetch is served by test/fakeBackend.ts.
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'AAA', local_path: '/work/alpha', created_at: '', updated_at: '' };
const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BBB', local_path: '/work/beta', created_at: '', updated_at: '' };

const PENDING_PATH = '/api/projects/pending-approvals';

let backend: FakeBackend;
let fetchMock: ReturnType<typeof vi.fn>;

function seed() {
  backend = createFakeBackend({
    projects: [alpha, beta],
    currentProjectId: alpha.id,
    labels: [],
    tickets: [
      { id: 'AAA-00001', project_id: alpha.id, title: 'Alpha のチケット', status: 'TODO', priority: 'HIGH', labelIds: [] },
      { id: 'BBB-00001', project_id: beta.id, title: 'Beta のチケット', status: 'TODO', priority: 'MEDIUM', labelIds: [] }
    ],
    pendingApprovals: () => ({ status: 200, body: { counts: {} } })
  });
  fetchMock = installFakeBackend(backend);
}

function pendingRequests(): number {
  return fetchMock.mock.calls.filter(c => String(c[0]) === PENDING_PATH).length;
}

// The header's switcher button: named by the current project alone.
function switcher(name = alpha.name) {
  return screen.getByRole('button', { name });
}

function popup() {
  return screen.queryByRole('dialog', { name: i18n.t('projectSwitcher.menuLabel') });
}

function menuItem(p: Project) {
  return screen.getByRole('button', { name: new RegExp(`^${p.name}\\b.*${p.prefix}$`) });
}

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('AAA-00001');
  return user;
}

describe('project switcher accessibility', () => {
  beforeEach(() => seed());

  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  describe('aria-expanded', () => {
    it('is "false" while closed and "true" while open, following clicks on the button', async () => {
      const user = await renderApp();
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');

      await user.click(switcher());
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');

      await user.click(switcher());
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');
      expect(popup()).toBeNull();
    });

    it('goes back to "false" when the menu closes by a backdrop click', async () => {
      const user = await renderApp();
      await user.click(switcher());
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');

      await user.click(screen.getByTestId('project-switcher-overlay'));
      expect(popup()).toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');
    });

    it('goes back to "false" when a project is chosen from the menu', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await user.click(menuItem(beta));

      await screen.findByText('BBB-00001');
      expect(popup()).toBeNull();
      expect(switcher(beta.name)).toHaveAttribute('aria-expanded', 'false');
    });
  });

  describe('aria-haspopup and the popup it opens', () => {
    it('declares a dialog popup, and the open popup is a named dialog the button controls', async () => {
      const user = await renderApp();
      expect(switcher()).toHaveAttribute('aria-haspopup', 'dialog');
      // Nothing to point at while the popup is not rendered.
      expect(switcher()).not.toHaveAttribute('aria-controls');

      await user.click(switcher());
      const dialog = popup();
      expect(dialog).not.toBeNull();
      expect(dialog).not.toHaveAttribute('aria-modal');
      expect(switcher()).toHaveAttribute('aria-controls', dialog!.id);
      expect(dialog!.id).not.toBe('');

      await user.click(switcher());
      expect(switcher()).not.toHaveAttribute('aria-controls');
    });

    it('names the popup in English too', async () => {
      await i18n.changeLanguage('en');
      const user = await renderApp();
      await user.click(switcher());
      expect(screen.getByRole('dialog', { name: 'Switch project' })).toBeInTheDocument();
    });
  });

  describe('Escape', () => {
    it('closes the menu and keeps focus on the button when focus is on the button', async () => {
      const user = await renderApp();
      await user.click(switcher());
      switcher().focus();
      expect(popup()).not.toBeNull();

      await user.keyboard('{Escape}');
      expect(popup()).toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');
      expect(switcher()).toHaveFocus();
    });

    it('closes the menu and returns focus to the button when focus is on a menu item', async () => {
      const user = await renderApp();
      await user.click(switcher());
      switcher().focus();
      await user.tab();
      expect(menuItem(alpha)).toHaveFocus();

      await user.keyboard('{Escape}');
      expect(popup()).toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');
      expect(switcher()).toHaveFocus();
    });

    it('closes the menu and focuses the button when focus is on "New project..."', async () => {
      const user = await renderApp();
      await user.click(switcher());
      screen.getByRole('button', { name: i18n.t('projectSwitcher.createNew') }).focus();

      await user.keyboard('{Escape}');
      expect(popup()).toBeNull();
      expect(switcher()).toHaveFocus();
    });

    it('closes the menu and focuses the button when focus is on <body>', async () => {
      const user = await renderApp();
      await user.click(switcher());
      (document.activeElement as HTMLElement | null)?.blur();
      expect(document.body).toHaveFocus();

      fireEvent.keyDown(document.body, { key: 'Escape' });
      expect(popup()).toBeNull();
      expect(switcher()).toHaveFocus();
    });

    it('does nothing while the menu is closed: it neither opens it nor takes the event', async () => {
      await renderApp();
      switcher().focus();

      const notCancelled = fireEvent.keyDown(switcher(), { key: 'Escape' });
      expect(notCancelled).toBe(true);
      expect(popup()).toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');
    });

    it('leaves the menu open when Escape cancels an IME composition', async () => {
      const user = await renderApp();
      await user.click(switcher());

      const notCancelled = fireEvent.keyDown(switcher(), { key: 'Escape', isComposing: true });
      expect(notCancelled).toBe(true);
      expect(popup()).not.toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');
    });

    it('leaves the menu open for an Escape another handler already took', async () => {
      const user = await renderApp();
      await user.click(switcher());

      const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
      event.preventDefault();
      fireEvent(switcher(), event);
      expect(popup()).not.toBeNull();
    });

    it('does not close the menu for other keys', async () => {
      const user = await renderApp();
      await user.click(switcher());
      fireEvent.keyDown(switcher(), { key: 'Enter' });
      fireEvent.keyDown(switcher(), { key: 'a' });
      expect(popup()).not.toBeNull();
    });

    it('still refetches the pending-approval counts when the menu reopens after an Escape', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await waitFor(() => expect(pendingRequests()).toBe(1));

      await user.keyboard('{Escape}');
      expect(popup()).toBeNull();

      await user.click(switcher());
      expect(popup()).not.toBeNull();
      await waitFor(() => expect(pendingRequests()).toBe(2));
    });
  });
});

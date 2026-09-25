// DFLT-00155: the header's project switcher tells assistive technology
// whether its popup is open (aria-expanded), that it opens one
// (aria-haspopup="dialog", matching the popup's role="dialog"), and Escape
// closes the open popup and puts focus back on the button.
//
// DFLT-00159: the open menu closes when keyboard focus (Tab / Shift+Tab)
// leaves the button and the menu -- to another element on the page, or off
// the page from the first element. Moving between the button and the items
// keeps it open, and so does focus falling to <body> or off the page by a
// click, blur() or a window switch, so the click on an item or the backdrop
// is never lost. A Tab pressed with Ctrl, Meta or Alt, or one that confirms
// an IME composition, is not taken as keyboard focus leaving (DFLT-00160).
//
// DFLT-00158: the menu marks the current project's item with
// aria-current="true" (and on no other item), and hides the decorative check
// mark from assistive technology.
//
// fetch is served by test/fakeBackend.ts.
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

function seed({ currentProjectId = alpha.id }: { currentProjectId?: string } = {}) {
  backend = createFakeBackend({
    projects: [alpha, beta],
    currentProjectId,
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

    // Leaves the menu open with focus outside it: Tab past the menu would
    // close it (DFLT-00159), so drop focus to <body> first (a blur with no
    // next element, which leaves the menu open) and move on from there. The
    // move from <body> to the target is no focusout of the switcher, so the
    // menu stays open behind it. jsdom has no hit testing, which lets the
    // click reach the target despite the menu's backdrop.
    async function leaveOpenMenuFor(user: ReturnType<typeof userEvent.setup>, target: HTMLElement) {
      await user.click(switcher());
      act(() => (document.activeElement as HTMLElement | null)?.blur());
      expect(document.body).toHaveFocus();
      expect(popup()).not.toBeNull();
      await user.click(target);
    }

    // QA round 1: a modal opened from another header button while the menu is
    // still open behind it (focus fell to <body> first). Its Escape belongs
    // to the modal, not to the menu behind it.
    it('lets a modal opened over the open menu take Escape, keeping focus in the modal until it closes', async () => {
      const user = await renderApp();
      const launch = screen.getByRole('button', { name: i18n.t('header.launchClaude') });
      await leaveOpenMenuFor(user, launch);
      expect(popup()).not.toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');

      const modal = await screen.findByRole('dialog', { name: i18n.t('claudeRunnerModal.title') });
      expect(modal).toHaveAttribute('aria-modal', 'true');
      expect(modal).toContainElement(document.activeElement as HTMLElement);

      await user.keyboard('{Escape}');
      // The first Escape closes the modal and returns focus to its opener,
      // exactly as without the menu; the menu is not what it closes.
      expect(screen.queryByRole('dialog', { name: i18n.t('claudeRunnerModal.title') })).toBeNull();
      expect(launch).toHaveFocus();
      expect(switcher()).not.toHaveFocus();
      expect(popup()).not.toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');
    });

    // Accessibility review round 2: clicking the modal's backdrop drops focus
    // to <body>, which alone would count as "on the switcher". The modal
    // still owns that Escape.
    it('lets a modal opened over the open menu take Escape when focus has fallen to <body>', async () => {
      const user = await renderApp();
      const launch = screen.getByRole('button', { name: i18n.t('header.launchClaude') });
      await leaveOpenMenuFor(user, launch);

      await screen.findByRole('dialog', { name: i18n.t('claudeRunnerModal.title') });
      (document.activeElement as HTMLElement | null)?.blur();
      expect(document.body).toHaveFocus();

      await user.keyboard('{Escape}');
      // The modal closes and focus goes back to its opener; the menu behind
      // it is untouched and focus does not jump to the switcher button.
      expect(screen.queryByRole('dialog', { name: i18n.t('claudeRunnerModal.title') })).toBeNull();
      expect(popup()).not.toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');
      expect(launch).toHaveFocus();
      expect(switcher()).not.toHaveFocus();
    });

    it('leaves Escape alone while any aria-modal dialog is open, whatever has focus', async () => {
      const user = await renderApp();
      await user.click(switcher());
      // A stand-in for any modal (e.g. ConfirmDialog's alertdialog) that has
      // no Escape handler of its own, so the event reaches only the menu.
      const modal = document.createElement('div');
      modal.setAttribute('role', 'alertdialog');
      modal.setAttribute('aria-modal', 'true');
      document.body.appendChild(modal);
      try {
        for (const target of [document.body, switcher(), menuItem(beta)]) {
          const notCancelled = fireEvent.keyDown(target, { key: 'Escape' });
          expect(notCancelled).toBe(true);
          expect(popup()).not.toBeNull();
          expect(switcher()).toHaveAttribute('aria-expanded', 'true');
        }
      } finally {
        modal.remove();
      }
      // Once the modal is gone, Escape closes the menu again.
      fireEvent.keyDown(document.body, { key: 'Escape' });
      expect(popup()).toBeNull();
      expect(switcher()).toHaveFocus();
    });

    it('leaves the menu and the event alone when focus is outside the button and the menu', async () => {
      const user = await renderApp();
      await user.click(switcher());
      // Via <body>, so no focusout of the switcher closes the menu first.
      act(() => (document.activeElement as HTMLElement | null)?.blur());
      const launch = screen.getByRole('button', { name: i18n.t('header.launchClaude') });
      launch.focus();
      expect(popup()).not.toBeNull();

      const notCancelled = fireEvent.keyDown(launch, { key: 'Escape' });
      expect(notCancelled).toBe(true);
      expect(popup()).not.toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');
      expect(launch).toHaveFocus();
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

  describe('focus leaving the switcher', () => {
    function launch() {
      return screen.getByRole('button', { name: i18n.t('header.launchClaude') });
    }

    function createNewItem() {
      return within(popup()!).getByRole('button', { name: i18n.t('projectSwitcher.createNew') });
    }

    function expectClosed() {
      expect(popup()).toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'false');
      expect(switcher()).not.toHaveAttribute('aria-controls');
    }

    function expectOpen() {
      expect(popup()).not.toBeNull();
      expect(switcher()).toHaveAttribute('aria-expanded', 'true');
    }

    it('closes the menu when Tab moves focus from its last item to the next header button', async () => {
      const user = await renderApp();
      await user.click(switcher());
      switcher().focus();
      // Button -> Alpha -> Beta -> "New project..."
      await user.tab();
      await user.tab();
      await user.tab();
      expect(createNewItem()).toHaveFocus();
      expectOpen();

      await user.tab();
      expect(launch()).toHaveFocus();
      expectClosed();
    });

    it('closes the menu when Shift+Tab moves focus from the button to an element before it', async () => {
      const outside = document.createElement('button');
      outside.textContent = 'before the app';
      document.body.insertBefore(outside, document.body.firstChild);
      try {
        const user = await renderApp();
        await user.click(switcher());
        switcher().focus();

        await user.tab({ shift: true });
        expect(outside).toHaveFocus();
        expectClosed();
      } finally {
        outside.remove();
      }
    });

    it('closes the menu when Shift+Tab moves focus off the page from the button', async () => {
      const user = await renderApp();
      await user.click(switcher());
      switcher().focus();

      // The button is the page's first focusable element: jsdom moves focus
      // to <body> (a browser to its own UI), a focusout with no next element.
      await user.tab({ shift: true });
      expect(document.body).toHaveFocus();
      expectClosed();
    });

    it('keeps the menu open while focus moves between the button and its items', async () => {
      const user = await renderApp();
      await user.click(switcher());
      switcher().focus();

      for (const expected of [() => menuItem(alpha), () => menuItem(beta), createNewItem]) {
        await user.tab();
        expect(expected()).toHaveFocus();
        expectOpen();
      }
      for (const expected of [() => menuItem(beta), () => menuItem(alpha), () => switcher()]) {
        await user.tab({ shift: true });
        expect(expected()).toHaveFocus();
        expectOpen();
      }
    });

    it('keeps the menu open when focus falls to <body>, and the backdrop still closes it', async () => {
      const user = await renderApp();
      await user.click(switcher());
      act(() => (document.activeElement as HTMLElement | null)?.blur());
      expect(document.body).toHaveFocus();
      expectOpen();

      await user.click(screen.getByTestId('project-switcher-overlay'));
      expectClosed();
    });

    it('still switches projects on an item click after focus fell to <body>', async () => {
      const user = await renderApp();
      await user.click(switcher());
      menuItem(beta).focus();
      act(() => menuItem(beta).blur());
      expect(document.body).toHaveFocus();
      expectOpen();

      await user.click(menuItem(beta));
      await screen.findByText('BBB-00001');
      expect(popup()).toBeNull();
      expect(switcher(beta.name)).toHaveAttribute('aria-expanded', 'false');
    });

    it('returns focus to the button, with the menu closed, when the new-project modal opened from the menu closes', async () => {
      const user = await renderApp();
      await user.click(switcher());
      switcher().focus();
      await user.tab();
      await user.tab();
      await user.tab();
      expect(createNewItem()).toHaveFocus();

      await user.keyboard('{Enter}');
      const modal = await screen.findByRole('dialog', { name: i18n.t('createProjectModal.title') });
      expect(modal).toHaveAttribute('aria-modal', 'true');
      expectClosed();

      await user.keyboard('{Escape}');
      expect(screen.queryByRole('dialog', { name: i18n.t('createProjectModal.title') })).toBeNull();
      expect(switcher()).toHaveFocus();
      expectClosed();
    });

    it('still refetches the pending-approval counts when the menu reopens after Tab closed it', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await waitFor(() => expect(pendingRequests()).toBe(1));
      switcher().focus();
      await user.tab();
      await user.tab();
      await user.tab();
      await user.tab();
      expect(launch()).toHaveFocus();
      expectClosed();

      await user.click(switcher());
      expectOpen();
      await waitFor(() => expect(pendingRequests()).toBe(2));
    });

    it('does not take a Tab with Ctrl, Meta or Alt, or one followed by a pointerdown, as leaving by keyboard', async () => {
      const user = await renderApp();
      await user.click(switcher());

      for (const modifier of [{ ctrlKey: true }, { metaKey: true }, { altKey: true }]) {
        switcher().focus();
        fireEvent.keyDown(switcher(), { key: 'Tab', ...modifier });
        act(() => switcher().blur());
        expect(document.body).toHaveFocus();
        expectOpen();
      }

      // A plain Tab's record does not carry over to a later click.
      switcher().focus();
      fireEvent.keyDown(switcher(), { key: 'Tab' });
      fireEvent.pointerDown(switcher());
      act(() => switcher().blur());
      expectOpen();

      // Control: a plain Tab straight before the same focusout does close it.
      switcher().focus();
      fireEvent.keyDown(switcher(), { key: 'Tab' });
      act(() => switcher().blur());
      expectClosed();
    });

    it('does not take a Tab that confirms an IME composition as leaving by keyboard', async () => {
      const user = await renderApp();
      await user.click(switcher());

      switcher().focus();
      fireEvent.keyDown(switcher(), { key: 'Tab', isComposing: true });
      act(() => switcher().blur());
      expect(document.body).toHaveFocus();
      expectOpen();

      // Control: a plain Tab straight before the same focusout does close it.
      switcher().focus();
      fireEvent.keyDown(switcher(), { key: 'Tab' });
      act(() => switcher().blur());
      expectClosed();
    });
  });

  describe('aria-current', () => {
    // Looked up inside the popup: with no current project, the empty state
    // offers a create button of its own.
    function createNew() {
      return within(popup()!).getByRole('button', { name: i18n.t('projectSwitcher.createNew') });
    }

    it('marks only the current project\'s item, not the others or "New project..."', async () => {
      const user = await renderApp();
      await user.click(switcher());

      expect(menuItem(alpha)).toHaveAttribute('aria-current', 'true');
      expect(menuItem(beta)).not.toHaveAttribute('aria-current');
      expect(createNew()).not.toHaveAttribute('aria-current');
      // Only the item carries it: not the header button or the popup.
      expect(switcher()).not.toHaveAttribute('aria-current');
      expect(popup()).not.toHaveAttribute('aria-current');
    });

    it('moves to the new current project after a switch', async () => {
      const user = await renderApp();
      await user.click(switcher());
      await user.click(menuItem(beta));
      await screen.findByText('BBB-00001');

      await user.click(switcher(beta.name));
      expect(menuItem(beta)).toHaveAttribute('aria-current', 'true');
      expect(menuItem(alpha)).not.toHaveAttribute('aria-current');
      expect(createNew()).not.toHaveAttribute('aria-current');
    });

    it('marks no item when there is no current project', async () => {
      seed({ currentProjectId: '' });
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.noProjectYet'));

      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.noProject') }));
      await waitFor(() => expect(menuItem(beta)).toBeInTheDocument());
      expect(menuItem(alpha)).not.toHaveAttribute('aria-current');
      expect(menuItem(beta)).not.toHaveAttribute('aria-current');
      expect(createNew()).not.toHaveAttribute('aria-current');
      expect(popup()!.querySelector('[aria-current]')).toBeNull();
    });

    it('marks no item when the current project could not be read', async () => {
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === '/api/current-project' && (init?.method ?? 'GET') === 'GET') {
          return new Response(JSON.stringify({ error: { code: 'CONFIG_READ_FAILED', message: 'boom' } }), {
            status: 500
          });
        }
        return realFetch(input, init);
      };
      vi.spyOn(console, 'error').mockImplementation(() => {});
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));

      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.noProject') }));
      await waitFor(() => expect(menuItem(beta)).toBeInTheDocument());
      expect(menuItem(alpha)).not.toHaveAttribute('aria-current');
      expect(menuItem(beta)).not.toHaveAttribute('aria-current');
      expect(popup()!.querySelector('[aria-current]')).toBeNull();
    });

    it('hides the check mark of every item from assistive technology, keeping the item names', async () => {
      const user = await renderApp();
      await user.click(switcher());

      for (const p of [alpha, beta]) {
        const icon = menuItem(p).querySelector('svg');
        expect(icon).not.toBeNull();
        expect(icon).toHaveAttribute('aria-hidden', 'true');
      }
      // The check mark's color, the only visual cue, is unchanged.
      expect(menuItem(alpha).querySelector('svg')).toHaveClass('text-blue-600');
      expect(menuItem(beta).querySelector('svg')).toHaveClass('text-transparent');
    });
  });
});

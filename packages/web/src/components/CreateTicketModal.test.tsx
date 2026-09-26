// DFLT-00079: the "New Ticket" modal asks for a single free-form request
// instead of separate title/description fields, and the prompt handed to
// Claude asks the create-ticket skill to work out the title/description.
import { useState } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import ja from '../i18n/locales/ja/translation.json';
import en from '../i18n/locales/en/translation.json';
import { CreateTicketModal } from './CreateTicketModal';
import { submittingName } from '../test/submittingName';

const getRequestInput = () =>
  screen.getByLabelText(i18n.t('createModal.requestLabel')) as HTMLTextAreaElement;

const getSubmitButton = () => screen.getByRole('button', { name: i18n.t('createModal.submit') });
const getSubmittingButton = () =>
  screen.getByRole('button', { name: submittingName(i18n.t('createModal.submit')) });

const renderModal = (overrides: Partial<React.ComponentProps<typeof CreateTicketModal>> = {}) => {
  const props = {
    onSubmit: vi.fn<(request: string) => Promise<boolean>>(async () => true),
    onClose: vi.fn(),
    isCreating: false,
    status: '',
    ...overrides
  };
  const { container } = render(<CreateTicketModal {...props} />);
  return { ...props, container };
};

describe('CreateTicketModal', () => {
  it('renders exactly one required request textarea and no title/description fields', () => {
    const { container } = renderModal();

    const input = getRequestInput();
    expect(input.tagName).toBe('TEXTAREA');
    expect(input).toBeRequired();
    expect(input).toHaveAttribute('placeholder', i18n.t('createModal.requestPlaceholder'));
    expect(container.querySelectorAll('textarea')).toHaveLength(1);
    expect(container.querySelectorAll('input')).toHaveLength(0);
  });

  it('keeps the submit button disabled and does not submit while the request is empty or whitespace-only', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal();

    expect(getSubmitButton()).toBeDisabled();

    await user.type(getRequestInput(), '   {Enter}  ');
    expect(getSubmitButton()).toBeDisabled();
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('submits the request text once via the Create button and clears the input on success', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal();
    const input = getRequestInput();

    await user.type(input, 'ログイン画面を直したい{Enter}理由: 遅い');
    expect(getSubmitButton()).toBeEnabled();
    await user.click(getSubmitButton());

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    expect(onSubmit).toHaveBeenCalledWith('ログイン画面を直したい\n理由: 遅い');
    await waitFor(() => {
      expect(input.value).toBe('');
    });
  });

  it('keeps the request text when the launch fails so it can be retried', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn<(request: string) => Promise<boolean>>(async () => false);
    renderModal({ onSubmit });
    const input = getRequestInput();

    await user.type(input, '長い要望{Enter}2行目');
    await user.click(getSubmitButton());

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    expect(onSubmit).toHaveBeenCalledWith('長い要望\n2行目');
    expect(input.value).toBe('長い要望\n2行目');
    expect(getSubmitButton()).toBeEnabled();
  });

  it('inserts a newline on plain Enter without submitting', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal();
    const input = getRequestInput();

    await user.type(input, '1行目{Enter}2行目');
    expect(input.value).toBe('1行目\n2行目');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('submits on Ctrl+Enter', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal();

    await user.type(getRequestInput(), 'a{Enter}b');
    await user.keyboard('{Control>}{Enter}{/Control}');

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith('a\nb');
    });
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('submits on Cmd(Meta)+Enter', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal();

    await user.type(getRequestInput(), 'x');
    await user.keyboard('{Meta>}{Enter}{/Meta}');

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith('x');
    });
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('calls onClose from the cancel button', async () => {
    const user = userEvent.setup();
    const { onClose } = renderModal();

    await user.click(screen.getByRole('button', { name: i18n.t('createModal.cancel') }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('disables the input while creating and relabels cancel as close', () => {
    renderModal({ isCreating: true });

    expect(getRequestInput()).toBeDisabled();
    expect(getSubmittingButton()).toBeDisabled();
    expect(screen.getByRole('button', { name: i18n.t('createModal.close') })).toBeInTheDocument();
  });

  // DFLT-00206: while the ticket is being created the submit button is
  // aria-busy and its accessible name says "(submitting)"; both go away once
  // the request settles, whether it succeeded or failed.
  describe('submitting state of the Create button', () => {
    // Owns isCreating the way App does: true while onSubmit is pending.
    const renderWithPendingSubmit = () => {
      let settle: (ok: boolean) => void = () => {};
      const Harness = () => {
        const [isCreating, setIsCreating] = useState(false);
        return (
          <CreateTicketModal
            isCreating={isCreating}
            status=""
            onClose={vi.fn()}
            onSubmit={async () => {
              setIsCreating(true);
              try {
                return await new Promise<boolean>(resolve => {
                  settle = resolve;
                });
              } finally {
                setIsCreating(false);
              }
            }}
          />
        );
      };
      render(<Harness />);
      return { settle: (ok: boolean) => settle(ok) };
    };

    for (const ok of [true, false]) {
      it(`is busy only while creating and clears after ${ok ? 'success' : 'failure'}`, async () => {
        const user = userEvent.setup();
        const { settle } = renderWithPendingSubmit();

        expect(getSubmitButton()).not.toHaveAttribute('aria-busy');
        await user.type(getRequestInput(), 'request');
        await user.click(getSubmitButton());

        const busy = getSubmittingButton();
        expect(busy).toHaveAttribute('aria-busy', 'true');
        expect(busy).toHaveAccessibleName(submittingName(i18n.t('createModal.submit')));

        await act(async () => {
          settle(ok);
        });

        const done = getSubmitButton();
        expect(done).toBe(busy);
        expect(done).not.toHaveAttribute('aria-busy');
        expect(done).toHaveAccessibleName(i18n.t('createModal.submit'));
      });
    }
  });

  // DFLT-00074: dialog semantics and keyboard focus management.
  describe('as a modal dialog', () => {
    it('is a dialog named by its title, focuses the request textarea and describes it with the shortcut hint', () => {
      renderModal();

      const dialog = screen.getByRole('dialog', { name: i18n.t('createModal.title') });
      expect(dialog).toHaveAttribute('aria-modal', 'true');
      expect(getRequestInput()).toHaveFocus();
      expect(getRequestInput()).toHaveAccessibleDescription(i18n.t('createModal.shortcutHint'));
    });

    it('wraps Tab and Shift+Tab inside the dialog', async () => {
      const user = userEvent.setup();
      renderModal();
      const cancel = screen.getByRole('button', { name: i18n.t('createModal.cancel') });

      await user.type(getRequestInput(), 'x');
      // textarea -> cancel -> submit -> (wrap) textarea
      await user.tab();
      expect(cancel).toHaveFocus();
      await user.tab();
      expect(getSubmitButton()).toHaveFocus();
      await user.tab();
      expect(getRequestInput()).toHaveFocus();
      await user.tab({ shift: true });
      expect(getSubmitButton()).toHaveFocus();
    });

    it('calls onClose on Escape', async () => {
      const user = userEvent.setup();
      const { onClose } = renderModal();

      await user.keyboard('{Escape}');
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('returns focus to the button that opened it when it unmounts', async () => {
      const user = userEvent.setup();
      const Harness = () => {
        const [isOpen, setIsOpen] = useState(false);
        return (
          <>
            <button type="button" onClick={() => setIsOpen(true)}>
              new-ticket
            </button>
            {isOpen && (
              <CreateTicketModal onSubmit={async () => true} onClose={() => setIsOpen(false)} isCreating={false} status="" />
            )}
          </>
        );
      };
      render(<Harness />);

      await user.click(screen.getByRole('button', { name: 'new-ticket' }));
      expect(getRequestInput()).toHaveFocus();
      await user.keyboard('{Escape}');

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'new-ticket' })).toHaveFocus();
    });
  });

  it('shows the launch status message', () => {
    renderModal({ status: i18n.t('claudeLaunch.started') });

    expect(screen.getByRole('button', { name: i18n.t('createModal.close') })).toBeInTheDocument();
    expect(screen.getAllByText(i18n.t('claudeLaunch.started')).length).toBeGreaterThan(0);
  });
});

describe('create-ticket translations', () => {
  it.each([
    ['ja', ja],
    ['en', en]
  ])('%s has the single request field keys and no old title/description keys', (_lang, locale) => {
    const modal = locale.createModal as Record<string, string>;
    expect(modal.requestLabel).toBeTruthy();
    expect(modal.requestPlaceholder).toBeTruthy();
    for (const oldKey of ['titleLabel', 'titlePlaceholder', 'descriptionLabel', 'descriptionPlaceholder']) {
      expect(modal).not.toHaveProperty(oldKey);
    }

    const prompt = locale.claudePrompts.createTicket;
    expect(prompt).toContain('{{request}}');
    expect(prompt).toMatch(/^\/graph-ops:create-ticket\s/);
    expect(prompt).not.toContain('{{title}}');
    expect(prompt).not.toContain('{{description}}');
  });

  it.each(['ja', 'en'])('%s createTicket prompt embeds a multi-line request verbatim', lng => {
    const request = '1行目 "quoted"\n2行目 <tag> & more';
    const prompt = i18n.t('claudePrompts.createTicket', { request, lng });

    expect(prompt.endsWith(request)).toBe(true);
  });
});

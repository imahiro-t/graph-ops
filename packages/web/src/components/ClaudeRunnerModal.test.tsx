// DFLT-00072: the launcher modal's prompt input is a multi-line textarea.
// Plain Enter must insert a newline, Cmd/Ctrl+Enter must launch, and the
// prompt must reach the launch API with its newlines intact.
import React, { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { ClaudeRunnerModal } from './ClaudeRunnerModal';

const fetchMock = () => fetch as unknown as ReturnType<typeof vi.fn>;

const getPromptInput = () =>
  screen.getByPlaceholderText(i18n.t('claudeRunnerModal.placeholder')) as HTMLTextAreaElement;

const getLaunchButton = () => screen.getByRole('button', { name: i18n.t('claudeRunnerModal.launch') });

const sentBody = () => {
  const [, init] = fetchMock().mock.calls[0];
  return JSON.parse((init as RequestInit).body as string);
};

describe('ClaudeRunnerModal', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders the prompt input as a textarea', () => {
    render(<ClaudeRunnerModal isOpen onClose={() => {}} projectId="proj-x" />);

    expect(getPromptInput().tagName).toBe('TEXTAREA');
  });

  it('inserts a newline on plain Enter without launching, and keeps pasted newlines', async () => {
    const user = userEvent.setup();
    render(<ClaudeRunnerModal isOpen onClose={() => {}} projectId="proj-x" />);
    const textarea = getPromptInput();

    await user.type(textarea, '1行目{Enter}2行目');
    expect(textarea.value).toBe('1行目\n2行目');
    expect(fetch).not.toHaveBeenCalled();

    await user.clear(textarea);
    await user.click(textarea);
    await user.paste('A\nB\nC');
    expect(textarea.value).toBe('A\nB\nC');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('launches on Ctrl+Enter with the multi-line prompt intact, then closes and resets the input', async () => {
    fetchMock().mockResolvedValueOnce({ ok: true });
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<ClaudeRunnerModal isOpen onClose={onClose} projectId="proj-x" />);
    const textarea = getPromptInput();

    await user.type(textarea, '- 手順1{Enter}- 手順2{Enter}- 手順3');
    await user.keyboard('{Control>}{Enter}{/Control}');

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    const body = sentBody();
    expect(body.prompt).toBe('- 手順1\n- 手順2\n- 手順3');
    expect(body.project_id).toBe('proj-x');
    // No defaultPrompt/ticketId was given, so the default prompt is empty.
    expect(textarea.value).toBe('');
  });

  it('launches on Cmd(Meta)+Enter as well', async () => {
    fetchMock().mockResolvedValueOnce({ ok: true });
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<ClaudeRunnerModal isOpen onClose={onClose} projectId="proj-x" />);

    await user.type(getPromptInput(), 'a{Enter}b');
    await user.keyboard('{Meta>}{Enter}{/Meta}');

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(sentBody().prompt).toBe('a\nb');
  });

  it('keeps the launch button disabled when the prompt is only whitespace and newlines', async () => {
    const user = userEvent.setup();
    render(<ClaudeRunnerModal isOpen onClose={() => {}} projectId="proj-x" />);

    await user.type(getPromptInput(), '  {Enter}  ');

    expect(getLaunchButton()).toBeDisabled();

    // Once there is actual content, the button becomes enabled.
    await user.type(getPromptInput(), 'a');
    expect(getLaunchButton()).toBeEnabled();
  });

  // DFLT-00074: dialog semantics and keyboard focus management.
  describe('as a modal dialog', () => {
    const Harness: React.FC<{ onClose?: () => void }> = ({ onClose }) => {
      const [isOpen, setIsOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setIsOpen(true)}>
            open-launcher
          </button>
          <ClaudeRunnerModal
            isOpen={isOpen}
            onClose={() => {
              onClose?.();
              setIsOpen(false);
            }}
            projectId="proj-x"
            defaultPrompt="hello"
          />
        </>
      );
    };

    it('is a dialog named by its title with a labelled close button', () => {
      render(<ClaudeRunnerModal isOpen onClose={() => {}} projectId="proj-x" />);

      const dialog = screen.getByRole('dialog', { name: i18n.t('claudeRunnerModal.title') });
      expect(dialog).toHaveAttribute('aria-modal', 'true');
      expect(screen.getByRole('button', { name: i18n.t('common.closeDialog') })).toBeInTheDocument();
    });

    it('labels the prompt textarea and describes it with the shortcut hint', () => {
      render(<ClaudeRunnerModal isOpen onClose={() => {}} projectId="proj-x" />);

      const textarea = screen.getByLabelText(i18n.t('claudeRunnerModal.promptLabel'));
      expect(textarea).toBe(getPromptInput());
      expect(textarea).toHaveAccessibleDescription(i18n.t('claudeRunnerModal.shortcutHint'));
    });

    it('moves focus to the textarea on open, wraps Tab/Shift+Tab, closes on Escape and returns focus to the opener', async () => {
      const onClose = vi.fn();
      const user = userEvent.setup();
      render(<Harness onClose={onClose} />);

      await user.click(screen.getByRole('button', { name: 'open-launcher' }));
      expect(getPromptInput()).toHaveFocus();

      await user.tab();
      expect(getLaunchButton()).toHaveFocus();
      await user.tab();
      expect(screen.getByRole('button', { name: i18n.t('common.closeDialog') })).toHaveFocus();
      await user.tab({ shift: true });
      expect(getLaunchButton()).toHaveFocus();

      await user.keyboard('{Escape}');
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'open-launcher' })).toHaveFocus();
    });

    it('returns focus to the opener when closed with the close button', async () => {
      const user = userEvent.setup();
      render(<Harness />);

      await user.click(screen.getByRole('button', { name: 'open-launcher' }));
      await user.click(screen.getByRole('button', { name: i18n.t('common.closeDialog') }));
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'open-launcher' })).toHaveFocus();
    });
  });

  it('does not launch on Ctrl/Cmd+Enter when the prompt is only whitespace and newlines', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<ClaudeRunnerModal isOpen onClose={onClose} projectId="proj-x" />);

    await user.type(getPromptInput(), '  {Enter}  ');
    await user.keyboard('{Control>}{Enter}{/Control}');
    await user.keyboard('{Meta>}{Enter}{/Meta}');

    expect(fetch).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });
});

// DFLT-00074: the "Create Project" modal, moved out of App.tsx, as a
// keyboard-operable dialog with labelled inputs.
import React, { useRef, useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { CreateProjectModal } from './CreateProjectModal';

const nameInput = () => screen.getByLabelText(i18n.t('createProjectModal.nameLabel')) as HTMLInputElement;
const prefixInput = () => screen.getByLabelText(i18n.t('createProjectModal.prefixLabel')) as HTMLInputElement;
const workDirInput = () => screen.getByLabelText(i18n.t('createProjectModal.workDirLabel')) as HTMLInputElement;
const submitButton = () => screen.getByRole('button', { name: i18n.t('createModal.submit') });
const cancelButton = () => screen.getByRole('button', { name: i18n.t('createModal.cancel') });

// Holds the form state like App.tsx does.
const Controlled: React.FC<{
  onSubmit?: (e: React.FormEvent) => void;
  onClose?: () => void;
  initial?: { name?: string; prefix?: string; workDir?: string };
}> = ({ onSubmit = e => e.preventDefault(), onClose = () => {}, initial = {} }) => {
  const [name, setName] = useState(initial.name ?? '');
  const [prefix, setPrefix] = useState(initial.prefix ?? '');
  const [workDir, setWorkDir] = useState(initial.workDir ?? '');
  return (
    <CreateProjectModal
      name={name}
      prefix={prefix}
      workDir={workDir}
      onNameChange={setName}
      onPrefixChange={setPrefix}
      onWorkDirChange={setWorkDir}
      onSubmit={onSubmit}
      onClose={onClose}
      isSaving={false}
      error=""
    />
  );
};

describe('CreateProjectModal', () => {
  it('is a dialog named by its title with the three inputs labelled', () => {
    render(<Controlled />);

    const dialog = screen.getByRole('dialog', { name: i18n.t('createProjectModal.title') });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(nameInput()).toHaveAttribute('placeholder', i18n.t('createProjectModal.namePlaceholder'));
    expect(prefixInput()).toHaveAttribute('maxLength', '5');
    expect(workDirInput()).toHaveAttribute('placeholder', i18n.t('createProjectModal.workDirPlaceholder'));
  });

  it('focuses the name input on open', () => {
    render(<Controlled />);
    expect(nameInput()).toHaveFocus();
  });

  it('wraps Tab and Shift+Tab inside the dialog', async () => {
    const user = userEvent.setup();
    render(<Controlled initial={{ name: 'P', workDir: '/w' }} />);

    // name -> prefix -> workDir -> cancel -> submit -> (wrap) name
    await user.tab();
    expect(prefixInput()).toHaveFocus();
    await user.tab();
    expect(workDirInput()).toHaveFocus();
    await user.tab();
    expect(cancelButton()).toHaveFocus();
    await user.tab();
    expect(submitButton()).toHaveFocus();
    await user.tab();
    expect(nameInput()).toHaveFocus();
    await user.tab({ shift: true });
    expect(submitButton()).toHaveFocus();
  });

  it('calls onClose on Escape and from the cancel button', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<Controlled onClose={onClose} />);

    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);

    await user.click(cancelButton());
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('submits the form with the typed values', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn((e: React.FormEvent) => e.preventDefault());
    render(<Controlled onSubmit={onSubmit} />);

    await user.type(nameInput(), 'My Project');
    await user.type(prefixInput(), 'abc');
    await user.type(workDirInput(), '/tmp/work');
    expect(prefixInput()).toHaveValue('ABC');
    await user.click(submitButton());

    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('keeps the submit button disabled while the name or working directory is only whitespace', async () => {
    const user = userEvent.setup();
    render(<Controlled />);

    expect(submitButton()).toBeDisabled();
    await user.type(nameInput(), '   ');
    await user.type(workDirInput(), '  ');
    expect(submitButton()).toBeDisabled();

    await user.type(nameInput(), 'x');
    expect(submitButton()).toBeDisabled();
    await user.type(workDirInput(), '/w');
    expect(submitButton()).toBeEnabled();
  });

  describe('focus on close', () => {
    // Mirrors App.tsx: an opener that may disappear when the modal closes
    // (the empty state's button after a successful create) and the project
    // switcher button as the fallback.
    const Harness: React.FC = () => {
      const [isOpen, setIsOpen] = useState(false);
      const [hasProject, setHasProject] = useState(false);
      const fallbackRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button type="button" ref={fallbackRef}>
            project-switcher
          </button>
          {!hasProject && (
            <button type="button" onClick={() => setIsOpen(true)}>
              empty-state-create
            </button>
          )}
          {isOpen && (
            <CreateProjectModal
              name="P"
              prefix=""
              workDir="/w"
              onNameChange={() => {}}
              onPrefixChange={() => {}}
              onWorkDirChange={() => {}}
              onSubmit={e => {
                e.preventDefault();
                setIsOpen(false);
                setHasProject(true);
              }}
              onClose={() => setIsOpen(false)}
              isSaving={false}
              error=""
              returnFocusFallbackRef={fallbackRef}
            />
          )}
        </>
      );
    };

    it('returns focus to the opener when it still exists (cancel)', async () => {
      const user = userEvent.setup();
      render(<Harness />);

      await user.click(screen.getByRole('button', { name: 'empty-state-create' }));
      expect(nameInput()).toHaveFocus();
      await user.keyboard('{Escape}');

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'empty-state-create' })).toHaveFocus();
    });

    it('returns focus to the fallback when the opener disappears on close (successful create)', async () => {
      const user = userEvent.setup();
      render(<Harness />);

      await user.click(screen.getByRole('button', { name: 'empty-state-create' }));
      await user.click(submitButton());

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'empty-state-create' })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'project-switcher' })).toHaveFocus();
    });
  });
});

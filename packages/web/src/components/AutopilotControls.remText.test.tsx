// DFLT-00225: the lines under the autopilot button (what a person is waited
// on for, the disabled reasons, the result of a start, the untrusted-folder
// notice) and the mode reasons in the confirmation dialog were 11px, a size
// that ignores the browser's default font size (WCAG 1.4.4). They are
// 0.6875rem now: 11px at the default 16px, so the default size looks as
// before, and larger with a larger default. jsdom does no layout, so this
// checks the classes; the sizes and the action row's layout at 100-200% and
// 320px-sm+ were measured in a real browser, and the build output was checked
// for the generated rule (see the ticket's implementation notes).
// At 22px (a 200% default) a run ID such as "(run-20260927-012345-" is wider
// than the row at 320px and has no break opportunity Chrome takes, so the
// lines also break anywhere (wrap-anywhere). Not together with
// wrap-break-word: on Tailwind v4 that one is emitted after wrap-anywhere
// and would win, bringing back the overflow (DFLT-00270).
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { NO_AUTOPILOT } from '../lib/autopilotApi';
import { AutopilotControls } from './AutopilotControls';

const REM_TEXT = 'text-[0.6875rem]';
const PX_TEXT = 'text-[11px]';

const expectRemText = (el: HTMLElement) => {
  expect(el).toHaveClass(REM_TEXT);
  expect(el).not.toHaveClass(PX_TEXT);
};

const expectBreaksAnywhere = (el: HTMLElement) => {
  expect(el).toHaveClass('wrap-anywhere');
  expect(el).not.toHaveClass('wrap-break-word');
};

afterEach(async () => {
  cleanup();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('AutopilotControls sizes its small lines in rem (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('sizes the awaited line and the disabled reasons in rem and breaks them anywhere', () => {
    render(
      <AutopilotControls
        ticketId="T"
        status="IN PROGRESS"
        view={{
          ...NO_AUTOPILOT,
          badges: ['awaitingHuman'],
          awaiting: 'plan approval',
          blockedBy: { ticket: 'P', tree: 'P' }
        }}
      />
    );
    const awaiting = screen.getByTestId('autopilot-awaiting');
    expectRemText(awaiting);
    expectBreaksAnywhere(awaiting);
    const reasons = screen.getAllByTestId('autopilot-disabled-reason');
    expect(reasons.length).toBeGreaterThan(0);
    for (const r of reasons) {
      expectRemText(r);
      expectBreaksAnywhere(r);
    }
  });

  it('sizes the result of a start and the untrusted-folder notice in rem and breaks them anywhere', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          run_id: 'run-20260927-012345-a1b2c3d4',
          mode: 'tree',
          root: 'T',
          state: 'starting',
          created: true,
          resumed: false,
          untrusted_folder: '/work/t'
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      )
    );
    const user = userEvent.setup();
    render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
    await user.click(screen.getByTestId('autopilot-start'));
    await user.click(screen.getByRole('button', { name: i18n.t('autopilot.confirm.start') }));
    const message = await screen.findByTestId('autopilot-message');
    expectRemText(message);
    expectBreaksAnywhere(message);
    const notice = screen.getByTestId('autopilot-untrusted');
    expectRemText(notice);
    expectBreaksAnywhere(notice.querySelector('p')!);
  });

  // DFLT-00252: the notice's dismiss button was about 22.5px tall at 100%
  // text, under the 24x24 CSS px of WCAG 2.5.8. min-h-6 / min-w-6 (1.5rem,
  // 24px at 100%, larger with larger text) with its label centred; its focus
  // ring shows in dark mode too. jsdom does no layout, so this checks the
  // classes; the size was measured in a real browser.
  it('gives the untrusted-folder dismiss button a 24px target and a focus ring, and it still dismisses', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          run_id: 'run-1',
          mode: 'tree',
          root: 'T',
          state: 'starting',
          created: true,
          resumed: false,
          untrusted_folder: '/work/t'
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      )
    );
    const user = userEvent.setup();
    render(<AutopilotControls ticketId="T" status="TODO" view={NO_AUTOPILOT} />);
    await user.click(screen.getByTestId('autopilot-start'));
    await user.click(screen.getByRole('button', { name: i18n.t('autopilot.confirm.start') }));
    const dismiss = await screen.findByTestId('autopilot-untrusted-dismiss');
    expect(dismiss).toHaveAccessibleName(i18n.t('autopilot.untrustedDismiss'));
    expect(dismiss).toHaveClass('min-h-6', 'min-w-6', 'inline-flex', 'items-center', 'justify-center');
    expect(dismiss).toHaveClass(
      'focus:outline-hidden',
      'focus-visible:ring-2',
      'focus-visible:ring-violet-500',
      'dark:focus-visible:ring-violet-400'
    );
    // The existing wrapping stays (DFLT-00224 / DFLT-00225).
    expect(dismiss).toHaveClass('shrink-0', 'max-w-full', 'max-sm:wrap-anywhere');
    await user.click(dismiss);
    expect(screen.queryByTestId('autopilot-untrusted')).not.toBeInTheDocument();
  });

  it('sizes the reason under a disabled mode in the dialog in rem and breaks it anywhere', async () => {
    const user = userEvent.setup();
    render(
      <AutopilotControls
        ticketId="T"
        status="IN PROGRESS"
        view={{ ...NO_AUTOPILOT, blockedBy: { ticket: '', tree: 'P' } }}
      />
    );
    await user.click(screen.getByTestId('autopilot-start'));
    const dialog = screen.getByRole('dialog');
    const modeReason = within(dialog).getByTestId('autopilot-mode-reason-tree');
    expectRemText(modeReason);
    expectBreaksAnywhere(modeReason);
  });

  it('leaves no px-sized text in the controls', async () => {
    const user = userEvent.setup();
    render(
      <AutopilotControls
        ticketId="T"
        status="IN PROGRESS"
        view={{ ...NO_AUTOPILOT, badges: ['awaitingHuman'], awaiting: 'plan approval', blockedBy: { ticket: '', tree: 'P' } }}
      />
    );
    await user.click(screen.getByTestId('autopilot-start'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    // The whole document: the dialog is rendered in a portal.
    expect(document.body.querySelectorAll('[class*="text-[11px]"]')).toHaveLength(0);
  });
});

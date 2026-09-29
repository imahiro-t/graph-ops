// DFLT-00259: with large text in a narrow window the ticket list page could
// be wider than the window. The top header's "GraphOps" name, subtitle,
// project switcher, Launch Claude, language and New Ticket buttons and the
// "updated" time each kept the width of their longest word, and the
// pagination group (previous, page number, next) could be wider than its row.
//
// Now:
// - each header item is min-w-0 and no wider than its row, and its text may
//   break inside a word ([overflow-wrap:anywhere], a last resort that only
//   applies when the word does not fit). That alone removes the sideways
//   scroll without a media query and changes nothing wherever the header
//   fitted before. The button labels are
//   spans of their own so the icons keep their size; the "updated" row does
//   not wrap, so its time keeps shrinking next to the refresh button as it
//   already did in English at 320-328px with a 32px root;
// - under 15rem (a 320px window at 200%) the pagination group may wrap (DOM
//   order kept), right-aligned so "next" stays at the right end, is no wider
//   than the row, and the page number may break inside its digits. It only
//   wraps once it is as wide as the row.
//
// DFLT-00319: the supported range is a 320px window with up to 200% text.
// The header's padding, labels and summary figures no longer change in a
// window of 200 CSS px or less (the icon-only header and the breaks inside
// the node progress there were removed), so these tests also check that the
// labels stay visible and the node progress unbreakable at every width.
// jsdom computes no layout, so these tests pin the classes that produce that
// behaviour and the names; the real-browser measurements are in the
// implementation notes.
//
// fetch is served by test/fakeBackend.ts, like App.largeTextReflow.test.tsx.
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { findPreviousPage } from './test/waitForAnswers';
import { allIconButtonTooltips, openIconButtonTooltips, setupHoverUser, startHoverFakeTimers, waitForHoverOpenDelay } from './test/iconButtonTooltip';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

const BREAKS_ANYWHERE = 'wrap-anywhere';
const NARROW = 'upto-15rem:';

// Six tickets on pages of five, so the pagination row is drawn. Without a
// project there are no tickets.
function seed({ withProject = true }: { withProject?: boolean } = {}) {
  installFakeBackend(
    createFakeBackend({
      projects: [alpha],
      currentProjectId: withProject ? alpha.id : '',
      paginationPageSize: 5,
      labels: [],
      tickets: (withProject ? Array.from({ length: 6 }) : []).map((_, i) => ({
        id: `ALP-0000${i + 1}`,
        project_id: alpha.id,
        title: `チケット ${i + 1}`,
        status: 'TODO' as const,
        priority: 'MEDIUM' as const,
        labelIds: []
      }))
    })
  );
}

async function renderApp() {
  render(<App />);
  await screen.findByText('ALP-00001');
}

// Without a project there is no ticket to wait for: wait for the switcher's
// "Select a project" label instead.
async function renderAppWithoutProject() {
  render(<App />);
  const header = screen.getByRole('banner');
  await within(header).findByRole('button', { name: i18n.t('projectSwitcher.noProject') });
  return header;
}

function languageName(lng: 'ja' | 'en') {
  return i18n.t('header.language.toggleTitle', { lang: i18n.t(`header.language.${lng}`) });
}

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('top header in a 320px window at 200%% (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('pads the header with px-4 below lg at every width', async () => {
    await renderApp();
    const header = screen.getByRole('banner');
    expect(header).toHaveClass('px-4', 'lg:px-6');
    expect(header).not.toHaveClass(`${NARROW}px-2`);
    expect(Array.from(header.classList).filter(c => c.startsWith('px-') || c.includes(':px-'))).toEqual(['px-4', 'lg:px-6']);
  });

  it('lets the app name and the subtitle break inside a word', async () => {
    await renderApp();
    const header = screen.getByRole('banner');
    const name = within(header).getByText('GraphOps');
    expect(name).toHaveClass('min-w-0', BREAKS_ANYWHERE);
    const subtitle = name.nextElementSibling as HTMLElement;
    expect(subtitle).toHaveTextContent(i18n.t('header.subtitle'));
    expect(subtitle).toHaveClass('min-w-0', BREAKS_ANYWHERE);
  });

  it('keeps the project switcher no wider than its row, still truncating the name', async () => {
    await renderApp();
    const header = screen.getByRole('banner');
    const switcher = within(header).getByRole('button', { name: /Alpha/ });
    expect(switcher).toHaveAttribute('aria-haspopup', 'dialog');
    // DFLT-00268: the 14rem cap sits on the wrapper; the button follows it.
    expect(switcher).toHaveClass('min-w-0', 'max-w-full');
    expect(switcher).not.toHaveClass('max-w-[14rem]');
    expect(switcher).toHaveClass('gap-1.5', 'px-3');
    expect(Array.from(switcher.classList).filter(c => c.includes(':gap-') || c.includes(':px-'))).toEqual([]);
    // DFLT-00285: the button is an IconButton, so its wrapper span comes in
    // between; it shrinks with the outer div, which is flex so the span is a
    // flex item rather than inline content.
    const span = switcher.parentElement as HTMLElement;
    expect(span.tagName).toBe('SPAN');
    expect(span).toHaveClass('min-w-0', 'max-w-full');
    expect(span.parentElement).toHaveClass('relative', 'flex', 'min-w-0', 'max-w-56');
    const name = within(switcher).getByText('Alpha');
    expect(name).toHaveClass('truncate');
    expect(name.className).not.toMatch(/sr-only/);
  });

  it.each([
    ['header.launchClaude'],
    ['header.newTicket']
  ])('keeps the %s label in a span of its own, visible and naming the button', async key => {
    await renderApp();
    const header = screen.getByRole('banner');
    const button = within(header).getByRole('button', { name: i18n.t(key) });
    expect(button).toHaveClass('min-w-0', 'max-w-full', 'flex', 'items-center', 'gap-1.5');
    expect(button).not.toHaveClass('flex-wrap');
    expect(button).not.toHaveClass(`${NARROW}flex-wrap`);
    expect(Array.from(button.classList).filter(c => c.includes(':px-'))).toEqual([]);
    // DFLT-00319: a plain <button> again (as before DFLT-00293), so it is the
    // header row's flex item itself, with no hover wrapper around it.
    expect(button.parentElement?.tagName).toBe('DIV');
    expect(button.parentElement).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    const label = within(button).getByText(i18n.t(key));
    expect(label.tagName).toBe('SPAN');
    expect(label).toHaveClass('min-w-0', BREAKS_ANYWHERE);
    expect(label.className).not.toMatch(/sr-only/);
    const icon = button.querySelector('svg') as SVGElement;
    expect(icon).toHaveClass('shrink-0');
    expect(icon).toHaveAttribute('aria-hidden', 'true');
  });

  it('lets the language button and its wrapper shrink and its text break', async () => {
    await renderApp();
    const header = screen.getByRole('banner');
    const current = lng === 'ja' ? 'ja' : 'en';
    const button = within(header).getByRole('button', {
      name: i18n.t('header.language.toggleTitle', { lang: i18n.t(`header.language.${current}`) })
    });
    expect(button).toHaveClass('min-w-0', 'max-w-full');
    expect(button).not.toHaveClass('flex-wrap');
    // IconButton's hover wrapper is the flex item of the header row.
    expect(button.parentElement).toHaveClass('inline-flex', 'min-w-0', 'max-w-full');
    const text = within(button).getByText(i18n.t(`header.language.${current}`));
    expect(text).toHaveClass('min-w-0', BREAKS_ANYWHERE);
    expect(text.className).not.toMatch(/sr-only/);
    expect(button.querySelector('svg')).toHaveClass('shrink-0');
  });

  it('lets the updated time shrink and break next to the refresh button without wrapping the row', async () => {
    await renderApp();
    const header = screen.getByRole('banner');
    const refresh = within(header).getByRole('button', { name: i18n.t('toolbar.refreshTitle') });
    const row = refresh.parentElement!.parentElement as HTMLElement;
    expect(row).toHaveClass('flex', 'min-w-0', 'max-w-full');
    // A wrapping row moved the time under the button at 320-328px in English.
    expect(row).not.toHaveClass('flex-wrap');
    const time = row.lastElementChild as HTMLElement;
    expect(time.tagName).toBe('SPAN');
    expect(time).toHaveClass('min-w-0', BREAKS_ANYWHERE);
  });
});

describe.each(['ja', 'en'] as const)('pagination in a 320px window at 200%% (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('lets the button group wrap right-aligned under 15rem, in reading order', async () => {
    await renderApp();
    const previous = await findPreviousPage();
    const next = screen.getByRole('button', { name: i18n.t('pagination.next') });
    const group = previous.parentElement as HTMLElement;
    expect(next.parentElement).toBe(group);
    expect(group).toHaveClass(
      `${NARROW}flex-wrap`,
      `${NARROW}justify-end`,
      'max-w-full',
      // Kept from DFLT-00251 / DFLT-00258.
      `${NARROW}gap-1`,
      'shrink-0'
    );
    // Unconditional wrapping or centring would change the look elsewhere.
    expect(group).not.toHaveClass('flex-wrap');
    expect(group).not.toHaveClass('justify-center');
    expect(group).not.toHaveClass(`${NARROW}justify-center`);
    const children = Array.from(group.children);
    expect(children).toHaveLength(3);
    expect(children[0]).toBe(previous);
    expect(children[2]).toBe(next);
  });

  it('lets the page number break inside its digits', async () => {
    await renderApp();
    const previous = await findPreviousPage();
    const page = previous.nextElementSibling as HTMLElement;
    expect(page).toHaveTextContent(i18n.t('pagination.pageOf', { page: 1, total: 2 }));
    expect(page).toHaveClass('min-w-0', BREAKS_ANYWHERE);
  });
});

describe.each(['ja', 'en'] as const)('summary card figures (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('breaks the node progress after the slash only, never inside a number', async () => {
    await renderApp();
    const progress = screen.getByTestId('summary-node-progress');
    const sides = Array.from(progress.querySelectorAll('span'));
    expect(sides).toHaveLength(2);
    for (const side of sides) {
      // Unbreakable at every width (DFLT-00258).
      expect(side).toHaveClass('whitespace-nowrap');
      expect(Array.from(side.classList).filter(c => c.includes('whitespace-normal'))).toEqual([]);
    }
  });

  it('keeps the five figure labels in rem text, breaking between words only', async () => {
    await renderApp();
    const items = Array.from(screen.getByTestId('summary-metrics').children) as HTMLElement[];
    expect(items).toHaveLength(5);
    for (const item of items) {
      const label = item.children[1] as HTMLElement;
      expect(label).toHaveClass('text-[0.6875rem]');
      expect(Array.from(label.classList).filter(c => c.includes(BREAKS_ANYWHERE))).toEqual([]);
    }
  });
});

// DFLT-00319: moved from App.headerIconOnly.test.tsx (removed with the
// icon-only header of a window of 200px or less), keeping the cases that
// hold at every width. Without a project New Ticket is natively disabled and
// described by the reason alone (a hidden span referenced by
// aria-describedby), with the reason as its title and no visible tooltip;
// with a project it has neither. The header's Tab order is unchanged.
//
// DFLT-00333: the no-tooltip case used to hover button.parentElement, which
// was IconButton's wrapper span before DFLT-00319 but is now the header's
// whole button group, so it no longer checked New Ticket itself. It now
// checks the button on its own: no hover wrapper around it, a description
// that is the hidden reason span and no tooltip element, and no tooltip
// after hovering the button itself and sending pointer and mouse events to
// it directly.
describe.each(['ja', 'en'] as const)('New Ticket and the Tab order in a 320px window at 200%% (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('is disabled without a project and described by the reason alone, through a hidden element', async () => {
    seed({ withProject: false });
    const header = await renderAppWithoutProject();
    const button = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    expect(button).toBeDisabled();
    const reason = i18n.t('projectSwitcher.selectFirst');
    expect(button).toHaveAccessibleDescription(reason);
    expect(button).not.toHaveAccessibleDescription(new RegExp(i18n.t('header.newTicket')));
    const describedBy = button.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    const description = document.getElementById(describedBy as string) as HTMLElement;
    expect(description).toHaveAttribute('hidden');
    expect(description.textContent).toBe(reason);
  });

  it('keeps the title with the reason without a project and gives the button itself no tooltip', async () => {
    startHoverFakeTimers();
    seed({ withProject: false });
    const user = setupHoverUser();
    const header = await renderAppWithoutProject();
    const button = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    const reason = i18n.t('projectSwitcher.selectFirst');
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', reason);

    // No tooltip trigger: IconButton would wrap the button in its hover span,
    // so the parent would be that span rather than the button group's div.
    const group = button.parentElement as HTMLElement;
    expect(group.tagName).toBe('DIV');

    // The only description is the hidden reason span, not a tooltip element.
    const describedBy = (button.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean);
    expect(describedBy).toHaveLength(1);
    const description = document.getElementById(describedBy[0]) as HTMLElement;
    expect(description).toHaveAttribute('hidden');
    expect(description.textContent).toBe(reason);
    expect(description.closest('[data-icon-button-tooltip]')).toBeNull();
    expect(button).toHaveAccessibleDescription(reason);
    // No header button uses describeWithTooltip, so no tooltip element is
    // rendered while none is open.
    expect(allIconButtonTooltips()).toHaveLength(0);

    // Hover the button itself, never the whole group.
    await user.hover(button);
    // user-event may hold back mouse events from a disabled element, while
    // some browsers deliver them. Send them to the button directly too: React
    // then calls no onMouseEnter of the disabled button itself, but does call
    // those of the elements around it, where IconButton tracks hover.
    fireEvent.pointerOver(button);
    fireEvent.mouseOver(button);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltips()).toHaveLength(0);
    expect(allIconButtonTooltips()).toHaveLength(0);
  });

  it('has no description and no title with a project', async () => {
    seed();
    await renderApp();
    const header = screen.getByRole('banner');
    const button = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    await waitFor(() => expect(button).toBeEnabled());
    expect(button).not.toHaveAttribute('aria-describedby');
    expect(button).not.toHaveAttribute('title');
    expect(button).toHaveAccessibleDescription('');
  });

  it('keeps the Tab order of the header controls', async () => {
    seed();
    const user = userEvent.setup();
    await renderApp();
    const header = screen.getByRole('banner');
    const expected = [
      'Alpha',
      i18n.t('header.launchClaude'),
      languageName(lng),
      new RegExp(`^${i18n.t('header.theme.toggleTitle', { mode: '' }).split(':')[0]}`),
      i18n.t('header.settings'),
      i18n.t('header.newTicket')
    ];
    const buttons = expected.map(name => within(header).getByRole('button', { name }));
    await user.tab();
    for (const button of buttons) {
      expect(button).toHaveFocus();
      await user.tab();
    }
  });
});

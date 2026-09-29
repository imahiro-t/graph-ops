// DFLT-00259: in a 160px window at a 200% text size (a 32px root font on the
// page, or a 32px default font) the ticket list page was 283px wide. The top
// header's "GraphOps" name, subtitle, project switcher, Launch Claude,
// language and New Ticket buttons and the "updated" time each kept the width
// of their longest word, and the pagination group (previous, page number,
// next: 54px buttons at 200%) was 211-254px wide in a 112px row. Once those
// fitted, the summary card's node progress ("2394/", whitespace-nowrap since
// DFLT-00258) and the English figure labels ("Progress") still ran 5-25px
// past the window: in a 160px window the card's items have no content width
// left at 200%.
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
// - on top of that, only for looks, in a window of 200 CSS px or less (at any
//   text size, so the header also changes there at 100%) it pads with px-2, the Launch
//   Claude, language and New Ticket buttons may put their label on a line
//   under the icon (flex-wrap, centred), and the project switcher and those
//   buttons pad less, so a label breaks between words instead of letter by
//   letter and the switcher's arrow stays inside its frame. A px query, not
//   the 15rem one, which also matches 320-336px with a 32px default font;
// - under 15rem the pagination group may wrap (DOM order kept), right-aligned
//   so "next" stays at the right end, is no wider than the row, and the page
//   number may break inside its digits. It only wraps once it is as wide as
//   the row, so the DFLT-00258 case (a 160px window at 200% browser zoom,
//   "100 / 123", 129px in a 136px row) stays on one line;
// - in a window of 200 CSS px or less the node progress may break inside a
//   number and the figure labels inside a word. A px query, not the 15rem
//   one: that one also matches 320-336px with a 32px default font.
//
// Measured in a real browser (vite + Playwright Chromium, see the ticket's
// implementation notes): at 160px with a 32px root and with a 32px default
// font, Japanese and English, with and without the detail panel, scrollWidth
// is 160 and nothing ends past the window; every header button and the
// pagination buttons can be hit at their centre after scrolling to them; with
// "1 / 26", "10 / 26" and "100 / 129" the wrapped group's lines share their
// right edge. At 320/328/336px (both 200% methods) and at 100% from 320 to
// 1280px every header item, the pagination group, the detail padding and the
// untrusted-folder notice measured the same as before (within 0.5px), and
// the header stays on one line at 1024/1280px. jsdom computes no layout, so
// these tests pin the classes that produce that behaviour and the names.
//
// fetch is served by test/fakeBackend.ts, like App.largeTextReflow.test.tsx.
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { findPreviousPage } from './test/waitForAnswers';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

const BREAKS_ANYWHERE = 'wrap-anywhere';
const NARROW = 'upto-15rem:';
const TINY = '[@media(max-width:200px)]:';

// Six tickets on pages of five, so the pagination row is drawn.
function seed() {
  installFakeBackend(
    createFakeBackend({
      projects: [alpha],
      currentProjectId: alpha.id,
      paginationPageSize: 5,
      labels: [],
      tickets: Array.from({ length: 6 }, (_, i) => ({
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

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('top header in a 160px window at 200%% (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('pads the header less only in a window of 200px or less', async () => {
    await renderApp();
    const header = screen.getByRole('banner');
    expect(header).toHaveClass('px-4', `${TINY}px-2`, 'lg:px-6');
    expect(header).not.toHaveClass(`${NARROW}px-2`);
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
    expect(switcher).toHaveClass('gap-1.5', `${TINY}gap-1`, 'px-3', `${TINY}px-2`);
    // DFLT-00285: the button is an IconButton, so its wrapper span comes in
    // between; it shrinks with the outer div, which is flex so the span is a
    // flex item rather than inline content.
    const span = switcher.parentElement as HTMLElement;
    expect(span.tagName).toBe('SPAN');
    expect(span).toHaveClass('min-w-0', 'max-w-full');
    expect(span.parentElement).toHaveClass('relative', 'flex', 'min-w-0', 'max-w-56');
    expect(within(switcher).getByText('Alpha')).toHaveClass('truncate');
  });

  it.each([
    ['header.launchClaude'],
    ['header.newTicket']
  ])('lets the %s button wrap its label in a span of its own, keeping its name', async key => {
    await renderApp();
    const header = screen.getByRole('banner');
    const button = within(header).getByRole('button', { name: i18n.t(key) });
    expect(button).toHaveClass('min-w-0', 'max-w-full');
    expect(button).toHaveClass(`${TINY}flex-wrap`, `${TINY}justify-center`, `${TINY}px-2`);
    expect(button).not.toHaveClass('flex-wrap');
    expect(button).not.toHaveClass(`${NARROW}flex-wrap`);
    const label = within(button).getByText(i18n.t(key));
    expect(label.tagName).toBe('SPAN');
    expect(label).toHaveClass('min-w-0', BREAKS_ANYWHERE);
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
    expect(button).toHaveClass(`${TINY}flex-wrap`, `${TINY}justify-center`);
    expect(button).not.toHaveClass('flex-wrap');
    // IconButton's hover wrapper is the flex item of the header row.
    expect(button.parentElement).toHaveClass('inline-flex', 'min-w-0', 'max-w-full');
    const text = within(button).getByText(i18n.t(`header.language.${current}`));
    expect(text).toHaveClass('min-w-0', BREAKS_ANYWHERE);
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

describe.each(['ja', 'en'] as const)('pagination in a 160px window at 200%% (%s)', lng => {
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

describe.each(['ja', 'en'] as const)('summary card in a window of 200px or less (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('lets the node progress break inside a number only there', async () => {
    await renderApp();
    const progress = screen.getByTestId('summary-node-progress');
    const sides = Array.from(progress.querySelectorAll('span'));
    expect(sides).toHaveLength(2);
    for (const side of sides) {
      // Still unbreakable elsewhere (DFLT-00258), breakable in a tiny window.
      expect(side).toHaveClass('whitespace-nowrap', `${TINY}whitespace-normal`);
      expect(side).not.toHaveClass('whitespace-normal');
      expect(side).not.toHaveClass(`${NARROW}whitespace-normal`);
    }
  });

  it('lets the five figure labels break inside a word only there', async () => {
    await renderApp();
    const items = Array.from(screen.getByTestId('summary-metrics').children) as HTMLElement[];
    expect(items).toHaveLength(5);
    for (const item of items) {
      const label = item.children[1] as HTMLElement;
      expect(label).toHaveClass('text-[0.6875rem]', `${TINY}${BREAKS_ANYWHERE}`);
      expect(label).not.toHaveClass(BREAKS_ANYWHERE);
    }
  });
});

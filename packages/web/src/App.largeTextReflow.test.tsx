// DFLT-00239: with a 200% default font on a 320-336px screen the ticket list
// page used to be 3-18px wider than the window (WCAG 1.4.10, Reflow). The
// culprits were the toolbar's filter triggers, which were whitespace-nowrap
// -- "Assignee: 1 selected" ended 18px past a 320px window once one value
// was picked -- and the pagination row under the list, whose "next" button
// ran past the window while the count was crushed into a narrow column.
// Now a trigger's text wraps inside the trigger (it is never truncated, so
// the count stays visible and the text stays the accessible name), and the
// pagination row wraps, moving the button group -- previous, page number,
// next, kept together -- under the count. The description card's header row
// is covered by components/TicketItem.descriptionHeaderWrap.test.tsx.
//
// jsdom computes no layout, so these tests pin the classes that produce that
// behaviour and the triggers' accessible names. The behaviour itself --
// document.documentElement.scrollWidth no wider than the window at 320,
// 328 and 336px with a 32px root font (Japanese and English, with and
// without the detail panel, with each filter at one value), elementFromPoint
// on each wrapped item, and unchanged measurements at 100% -- was measured
// in a real browser (see the ticket's implementation notes).
//
// DFLT-00251 continues this for a 160px window (320px at 200% zoom). There
// the English summary card heading ("All Tickets Overview (Click to
// expand...)") kept the min-content width of its longest words and ran 5px
// past the window, and the pagination buttons with a two-digit page number
// ("10 / 25") ended past <main>'s padding. Under 15rem (the rem query of
// DFLT-00227) <main> now pads with px-3, the heading wraps and may break
// inside a word, and the pagination buttons close up to gap-2; at every
// width the summary card's children are no wider than the card and the
// numbers wrap between items (the original report, DFLT-00234, predates
// DFLT-00220, and the numbers were already found to wrap: 100-200% at
// 320-1024px measured no sideways scroll before this change). With the
// default font at 320-1440px the page measures the same as before.
//
// fetch is served by test/fakeBackend.ts, like App.headerLayout.test.tsx.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

// Six tickets on pages of five, so the pagination row is drawn.
const PAGE_SIZE = 5;
const TICKET_COUNT = 6;

function seed() {
  const backend = createFakeBackend({
    projects: [alpha],
    currentProjectId: alpha.id,
    paginationPageSize: PAGE_SIZE,
    labels: [{ id: 'label-ui', project_id: alpha.id, name: 'UI改善', color: 'green' }],
    tickets: Array.from({ length: TICKET_COUNT }, (_, i) => ({
      id: `ALP-0000${i + 1}`,
      project_id: alpha.id,
      title: `チケット ${i + 1}`,
      status: 'TODO' as const,
      priority: 'MEDIUM' as const,
      assignee: '佐藤',
      labelIds: ['label-ui']
    }))
  });
  installFakeBackend(backend);
}

// Each filter's name builds both its trigger's test ID and its i18n keys.
const FILTERS = ['status', 'assignee', 'priority', 'label'] as const;

const trigger = (name: string) => screen.getByTestId(`toolbar-${name}-filter-panel-trigger`);

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('ALP-00001');
  return user;
}

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('toolbar filter triggers with large text (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('wrap their text inside the trigger instead of widening the toolbar', async () => {
    await renderApp();
    for (const name of FILTERS) {
      const button = trigger(name);
      expect(button).not.toHaveClass('whitespace-nowrap');
      expect(button).toHaveClass('max-w-full', 'min-w-0');
      // The wrapper the panel is positioned against may not outgrow the
      // toolbar either.
      expect(button.parentElement).toHaveClass('relative', 'max-w-full', 'min-w-0');

      // The text sits in its own span that may break anywhere as a last
      // resort; the arrow stays full size and hidden from assistive tech.
      const text = button.querySelector('span') as HTMLElement;
      expect(text).toHaveClass('min-w-0', '[overflow-wrap:anywhere]', 'break-keep');
      expect(text).toHaveTextContent(i18n.t(`toolbar.${name}All`));
      const arrow = button.querySelector('svg') as SVGElement;
      expect(arrow).toHaveClass('shrink-0');
      expect(arrow).toHaveAttribute('aria-hidden', 'true');
    }
  });

  it.each(FILTERS)('keep the selection in the %s trigger\'s accessible name after picking one value', async name => {
    const user = await renderApp();
    await user.click(trigger(name));
    const panel = screen.getByRole('group', { name: i18n.t(`toolbar.${name}GroupLabel`) });
    // The label filter's options arrive with the labels request.
    const boxes = await waitFor(() => {
      const found = within(panel).getAllByRole('checkbox');
      expect(found.length).toBeGreaterThan(0);
      return found;
    });
    await user.click(boxes[0]);
    await user.keyboard('{Escape}');

    const expected = i18n.t(`toolbar.${name}Selected`, { count: 1 });
    expect(screen.getByRole('button', { name: expected })).toBe(trigger(name));
    // The visible text is the name: no aria-label that could disagree with
    // it (WCAG 2.5.3).
    expect(trigger(name)).not.toHaveAttribute('aria-label');
    expect(trigger(name)).toHaveFocus();
  });
});

describe.each(['ja', 'en'] as const)('pagination row with large text (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('wraps, keeping previous / page number / next together in one group', async () => {
    await renderApp();
    const previous = screen.getByRole('button', { name: i18n.t('pagination.previous') });
    const next = screen.getByRole('button', { name: i18n.t('pagination.next') });
    const pageOf = screen.getByText(i18n.t('pagination.pageOf', { page: 1, total: 2 }));

    const group = previous.parentElement as HTMLElement;
    expect(group).toContainElement(next);
    expect(group).toContainElement(pageOf);
    expect(group).toHaveClass('flex', 'items-center', 'gap-3', 'shrink-0');

    const row = group.parentElement as HTMLElement;
    expect(row).toHaveClass('flex', 'flex-wrap', 'items-center', 'justify-between', 'gap-x-3', 'gap-y-2');

    const range = screen.getByText(i18n.t('pagination.range', { from: 1, to: PAGE_SIZE, total: TICKET_COUNT }));
    expect(range.parentElement).toBe(row);
    expect(range).toHaveClass('min-w-0');
  });
});

describe.each(['ja', 'en'] as const)('summary card, <main> and pagination in a 160px window (%s)', lng => {
  const NARROW = '[@media(max-width:15rem)]:';

  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  // DFLT-00252: also below sm (a px query), so a 200% root font size set on
  // the page (which the rem query does not follow) still pads less at
  // 320/360px; the 15rem query stays for a very large default font.
  it('pads <main> less only below sm or under 15rem', async () => {
    await renderApp();
    const main = screen.getByRole('main');
    expect(main).toHaveClass('px-6', 'max-sm:px-3', `${NARROW}px-3`);
    expect(main).not.toHaveClass('px-3');
    expect(main).not.toHaveClass('sm:px-6');
    expect(main).not.toHaveClass('sm:px-3');
  });

  it('keeps the summary card\'s children inside the card and the numbers wrapping between items', async () => {
    await renderApp();
    const metrics = screen.getByTestId('summary-metrics');
    expect(metrics).toHaveClass('flex', 'flex-wrap', 'min-w-0', 'max-w-full');
    expect(metrics).toHaveTextContent(i18n.t('summary.total'));
    const heading = screen.getByTestId('summary-heading');
    expect(heading).toHaveClass('min-w-0', 'max-w-full');
    expect(heading.parentElement).toBe(metrics.parentElement);
    expect(heading.parentElement).toHaveClass('flex', 'flex-wrap');
  });

  // Only under 15rem: at 320-414px with the default font the title and the
  // note sit side by side, each wrapping its own text; letting them wrap
  // onto lines of their own (or break inside a word) there would change
  // that look.
  it('lets the summary heading wrap and break inside a word only under 15rem', async () => {
    await renderApp();
    const heading = screen.getByTestId('summary-heading');
    expect(heading).toHaveClass('flex', `${NARROW}flex-wrap`, 'items-center', 'gap-x-2', 'gap-y-0.5');
    expect(heading).not.toHaveClass('flex-wrap');
    const title = screen.getByText(i18n.t('summary.title'));
    const note = screen.getByText(i18n.t('summary.subtitle'));
    for (const el of [title, note]) {
      expect(el.parentElement).toBe(heading);
      expect(el).toHaveClass(`${NARROW}min-w-0`, `${NARROW}[overflow-wrap:anywhere]`);
      expect(el).not.toHaveClass('[overflow-wrap:anywhere]');
    }
  });

  it('closes up the pagination buttons under 15rem and lets the count break', async () => {
    await renderApp();
    const previous = screen.getByRole('button', { name: i18n.t('pagination.previous') });
    const group = previous.parentElement as HTMLElement;
    expect(group).toHaveClass('gap-3', `${NARROW}gap-2`, 'shrink-0');
    const range = screen.getByText(i18n.t('pagination.range', { from: 1, to: PAGE_SIZE, total: TICKET_COUNT }));
    expect(range).toHaveClass('min-w-0', '[overflow-wrap:anywhere]');
  });
});

// DFLT-00251 (after the release gate): at 320px with a 32px root font the
// node progress number ("2204/2228" with a few thousand nodes, text-lg bold)
// was one unbreakable word wider than its item and ran 2px past the window.
// Every item is now no wider than the row and its number may break -- after
// the slash first, anywhere as a last resort. An item only narrows when it
// is wider than a whole line of the row, so with the default font nothing
// moves (measured in a real browser at 320-1024px x 16/24/32px with
// four-digit node counts; see the ticket's implementation notes).
describe.each(['ja', 'en'] as const)('summary card numbers with large text (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('lets every number break inside an item that stays within the row', async () => {
    await renderApp();
    const metrics = screen.getByTestId('summary-metrics');
    const items = Array.from(metrics.children) as HTMLElement[];
    expect(items).toHaveLength(5);
    for (const item of items) {
      expect(item).toHaveClass('text-center', 'px-3', 'min-w-0', 'max-w-full');
      const number = item.firstElementChild as HTMLElement;
      expect(number).toHaveClass('text-lg', 'font-bold', '[overflow-wrap:anywhere]');
    }
    expect(items[4]).toHaveTextContent(i18n.t('summary.nodeProgress'));
  });

  it('offers a line break right after the slash of the node progress', async () => {
    await renderApp();
    const progress = screen.getByTestId('summary-node-progress');
    expect(progress.parentElement?.parentElement).toBe(screen.getByTestId('summary-metrics'));
    // The seeded tickets have no nodes. The text reads the same as before;
    // the <wbr> adds no character.
    expect(progress).toHaveTextContent(/^0\/0$/);
    const wbr = progress.querySelector('wbr');
    expect(wbr).not.toBeNull();
    expect(wbr?.previousSibling?.textContent).toMatch(/\/$/);
    expect(wbr?.nextSibling?.textContent).toBe('0');
  });
});

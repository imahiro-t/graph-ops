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

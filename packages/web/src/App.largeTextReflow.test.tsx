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
// DFLT-00251 continues this for large text in a narrow window. There the
// English summary card heading ("All Tickets Overview (Click to
// expand...)") kept the min-content width of its longest words and ran
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
import { findPreviousPage } from './test/waitForAnswers';
import { allIconButtonTooltips, openIconButtonTooltip, waitForHoverOpenDelay } from './test/iconButtonTooltip';
import { PROJECT_MENU_WIDTH_REM } from './lib/popupPlacement';

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

// The header's switcher: the only header button with aria-haspopup="dialog".
function switcherButton(): HTMLElement {
  const found = within(screen.getByRole('banner'))
    .getAllByRole('button')
    .filter(b => b.getAttribute('aria-haspopup') === 'dialog');
  expect(found).toHaveLength(1);
  return found[0];
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
      expect(text).toHaveClass('min-w-0', 'wrap-anywhere', 'break-keep');
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
    const previous = await findPreviousPage();
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

describe.each(['ja', 'en'] as const)('summary card, <main> and pagination in a 320px window at 200%% (%s)', lng => {
  const NARROW = 'upto-15rem:';

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

  // DFLT-00319: nothing narrower than that is padded less any more (px-3
  // is the least at every width).
  it('has no side padding for <main> other than px-6, max-sm:px-3 and under-15rem px-3', async () => {
    await renderApp();
    const main = screen.getByRole('main');
    expect(Array.from(main.classList).filter(c => /(^|:)px-/.test(c))).toEqual(['px-6', 'max-sm:px-3', `${NARROW}px-3`]);
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
      expect(el).toHaveClass(`${NARROW}min-w-0`, `${NARROW}wrap-anywhere`);
      expect(el).not.toHaveClass('wrap-anywhere');
      expect(el).not.toHaveClass('wrap-anywhere');
    }
  });

  // DFLT-00258: gap-1 (was gap-2) and no side padding on the row under
  // 15rem, so a three-digit page number ("100 / 123") needs less room.
  // From 15rem up nothing changes.
  it('closes up the pagination buttons under 15rem and lets the count break', async () => {
    await renderApp();
    const previous = await findPreviousPage();
    const group = previous.parentElement as HTMLElement;
    expect(group).toHaveClass('gap-3', `${NARROW}gap-1`, 'shrink-0');
    expect(group).not.toHaveClass(`${NARROW}gap-2`);
    const row = group.parentElement as HTMLElement;
    expect(row).toHaveClass('px-1', `${NARROW}px-0`);
    expect(row).not.toHaveClass('px-0');
    const range = screen.getByText(i18n.t('pagination.range', { from: 1, to: PAGE_SIZE, total: TICKET_COUNT }));
    expect(range).toHaveClass('min-w-0', 'wrap-anywhere');
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
      expect(number).toHaveClass('text-lg', 'font-bold', 'wrap-anywhere');
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

// DFLT-00258: the rest of DFLT-00251's backlog on the summary card.
describe.each(['ja', 'en'] as const)('summary card with a root font size set on the page (%s)', lng => {
  const NARROW = 'upto-15rem:';
  const CARD_NARROW = 'cq-upto-12rem:';

  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  // The 15rem media query does not follow a root font size set on the page;
  // a container query's rem does. In English at 320-336px with a 32px root
  // the note ran 26px past the card; with the default font the card is
  // 262px wide at 320px, well over 12rem (192px), so nothing changes there.
  it('lets the heading wrap when the card is narrower than 12rem, keeping the 15rem query', async () => {
    await renderApp();
    const heading = screen.getByTestId('summary-heading');
    const card = heading.parentElement as HTMLElement;
    expect(card).toHaveClass('@container');
    expect(card).toBe(screen.getByTestId('summary-metrics').parentElement);
    // The heading's width comes from its content, so it is not a container.
    expect(heading.className).not.toMatch(/container-type/);
    expect(heading).toHaveClass(`${NARROW}flex-wrap`, `${CARD_NARROW}flex-wrap`);
    expect(heading).not.toHaveClass('flex-wrap');
    const spans = Array.from(heading.children) as HTMLElement[];
    expect(spans).toHaveLength(2);
    expect(spans[0]).toHaveTextContent(i18n.t('summary.title'));
    expect(spans[1]).toHaveTextContent(i18n.t('summary.subtitle'));
    for (const el of spans) {
      expect(el).toHaveClass(
        `${NARROW}min-w-0`,
        `${NARROW}wrap-anywhere`,
        `${CARD_NARROW}min-w-0`,
        `${CARD_NARROW}wrap-anywhere`
      );
      expect(el).not.toHaveClass('min-w-0');
      expect(el).not.toHaveClass('wrap-anywhere');
      expect(el).not.toHaveClass('wrap-anywhere');
    }
  });

  it('sizes the five figure labels in rem (11px at the default font), not px', async () => {
    await renderApp();
    const metrics = screen.getByTestId('summary-metrics');
    const labels = (Array.from(metrics.children) as HTMLElement[]).map(item => item.children[1] as HTMLElement);
    expect(labels.map(l => l.textContent)).toEqual(
      ['total', 'inProgress', 'inReview', 'done', 'nodeProgress'].map(key => i18n.t(`summary.${key}`))
    );
    for (const label of labels) expect(label).toHaveClass('text-[0.6875rem]');
    const card = metrics.parentElement as HTMLElement;
    expect(card.querySelector('[class*="text-[11px]"]')).toBeNull();
  });

  // Each side of the slash is whitespace-nowrap, so the only break left is
  // the <wbr> after the slash: never "9997" / "2/", at any width (see also
  // App.headerNarrowReflow.test.tsx).
  it('breaks the node progress only after the slash, never inside a number', async () => {
    await renderApp();
    const progress = screen.getByTestId('summary-node-progress');
    expect(progress).toHaveTextContent(/^0\/0$/);
    const wbr = progress.querySelector('wbr') as HTMLElement;
    const before = wbr.previousSibling as HTMLElement;
    const after = wbr.nextSibling as HTMLElement;
    expect(before.tagName).toBe('SPAN');
    expect(before).toHaveClass('whitespace-nowrap');
    expect(before.textContent).toBe('0/');
    expect(after.tagName).toBe('SPAN');
    expect(after).toHaveClass('whitespace-nowrap');
    expect(after.textContent).toBe('0');
    expect(Array.from(progress.childNodes)).toEqual([before, wbr, after]);

    // The other four numbers keep breaking anywhere as a last resort.
    const items = Array.from(screen.getByTestId('summary-metrics').children) as HTMLElement[];
    for (const item of items.slice(0, 4)) {
      const number = item.firstElementChild as HTMLElement;
      expect(number).toHaveClass('wrap-anywhere');
      expect(number.querySelector('.whitespace-nowrap')).toBeNull();
    }
  });
});

// DFLT-00268: the project switcher's button was capped at 14rem on the button
// itself, and its min-content width was that cap: with a 32px root font the
// button stayed 14rem (448px) wide however narrow the header got, so a long
// project name ("Selection Manipulator") made the page 404px wide in a 320px
// window. The 14rem cap now sits on the wrapper, which is a
// flex item that may shrink (min-w-0), and the button is never wider than
// the wrapper (max-w-full), so the name truncates inside the header's width.
// Where the button already fitted (the default font at 320-1440px) the
// header measures the same as before: the wrapper's size contribution is
// still capped at 14rem.
//
// DFLT-00277 then gave the button a tooltip (the name on the first line, the
// local path or "not set" on the second; see the describe below), but its
// accessible name is still the button's text, the project name alone. The
// test here checks that too -- the button it finds by that name is the
// header's switcher, and the name is exactly "Alpha" -- so the button's name
// and layout are checked in this one place. DFLT-00285 made the button an
// IconButton (no title attribute), whose wrapper span sits between the button
// and the 14rem wrapper and shrinks with it.
describe.each(['ja', 'en'] as const)('project switcher in a narrow header (%s)', lng => {
  beforeEach(async () => {
    seed();
    await i18n.changeLanguage(lng);
  });

  it('caps the wrapper at 14rem and lets the button shrink with it, truncating the name and keeping the accessible name', async () => {
    await renderApp();
    const button = within(screen.getByRole('banner')).getByRole('button', { name: /Alpha/ });
    expect(button).toBe(switcherButton());
    expect(button).toHaveAttribute('aria-haspopup', 'dialog');
    expect(button).not.toHaveAttribute('title');
    expect(button).toHaveAccessibleName('Alpha');
    expect(button).toHaveClass('max-w-full');
    expect(button).not.toHaveClass('max-w-56');
    const span = button.parentElement as HTMLElement;
    expect(span).toHaveClass('min-w-0', 'max-w-full');
    const wrapper = span.parentElement as HTMLElement;
    expect(wrapper).toHaveClass('relative', 'flex', 'min-w-0', 'max-w-56');
    expect(within(button).getByText('Alpha')).toHaveClass('truncate');
  });
});

// DFLT-00277: with the name truncated in the button, sighted users read the
// full name in the button's tooltip -- the name on the first line, the local
// path (or "not set") on the second -- and in the popup, whose items now
// wrap the name instead of truncating it. The prefix and the
// pending-approval badge stay in the group on the item's right. The
// accessible name is unchanged (checked with the button's layout in
// "project switcher in a narrow header" above).
// DFLT-00285: the tooltip is IconButton's visible one (hover and keyboard
// focus, aria-hidden) instead of a title attribute, so a screen reader no
// longer reads the name a second time as the description: the description
// is the local path (or "not set") alone. The popup's width and position are
// set from the window (App.projectMenuPlacement.test.tsx). DFLT-00319: at
// 320px with 200% text the popup is 9rem wide, so its items keep one layout
// at every supported width (the second-line layout below 8rem was removed).
// jsdom computes no layout, so the wrapping itself, the unchanged look at
// the default font and the absence of sideways scroll at 320px / 32px were
// measured in a real browser (see the ticket's implementation notes).
describe.each(['ja', 'en'] as const)('full project name in the switcher\'s tooltip and popup (%s)', lng => {
  const LONG = 'Long project name for checking that the full name wraps inside the popup item';
  const NO_SPACE = 'A'.repeat(80);
  const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '', created_at: '', updated_at: '' };
  const long: Project = { id: 'p-long', name: LONG, prefix: 'LONG', local_path: '/work/long', created_at: '', updated_at: '' };
  const noSpace: Project = { id: 'p-nospace', name: NO_SPACE, prefix: 'NOSP', local_path: '/work/nospace', created_at: '', updated_at: '' };

  function seedProjects(currentProjectId: string) {
    const projects = [alpha, beta, long, noSpace];
    const current = projects.find(p => p.id === currentProjectId) as Project;
    installFakeBackend(
      createFakeBackend({
        projects,
        currentProjectId,
        labels: [],
        tickets: [
          { id: `${current.prefix}-00001`, project_id: current.id, title: 'チケット', status: 'TODO', priority: 'MEDIUM', labelIds: [] }
        ],
        pendingApprovals: () => ({ status: 200, body: { counts: { [beta.id]: 2, [long.id]: 1 } } })
      })
    );
    return current;
  }

  async function renderWith(currentProjectId: string) {
    const current = seedProjects(currentProjectId);
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(`${current.prefix}-00001`);
    return user;
  }

  // The popup's items are the project list (GET /api/projects), a request of
  // its own that seeing a ticket does not imply has been answered: wait for
  // an item before looking at them (DFLT-00296).
  async function openPopup(user: ReturnType<typeof userEvent.setup>) {
    await user.click(switcherButton());
    const popup = screen.getByRole('dialog', { name: i18n.t('projectSwitcher.menuLabel') });
    await within(popup).findByText(noSpace.name, { exact: true });
    return popup;
  }

  // An item's name span: the text node's own element, found by exact text.
  function itemName(popup: HTMLElement, name: string): HTMLElement {
    return within(popup).getByText(name, { exact: true });
  }

  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  async function hoverTooltipLines(user: ReturnType<typeof userEvent.setup>): Promise<string[]> {
    await user.hover(switcherButton());
    await waitForHoverOpenDelay();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toHaveAttribute('aria-hidden', 'true');
    return Array.from(tooltip.children).map(line => line.textContent ?? '');
  }

  it('puts the name on the tooltip\'s first line and the local path on the second, describing the button by the path alone', async () => {
    const user = await renderWith(alpha.id);
    const button = switcherButton();
    expect(button).not.toHaveAttribute('title');
    expect(button).toHaveAccessibleName('Alpha');
    expect(button).toHaveAccessibleDescription('/work/alpha');
    expect(await hoverTooltipLines(user)).toEqual(['Alpha', '/work/alpha']);
  });

  // DFLT-00321: "Local path: Not set", not a bare "Not set".
  it('writes "local path: not set" on the second line and in the description when the project has no local path', async () => {
    const user = await renderWith(beta.id);
    const notSet = i18n.t('projectSwitcher.localPathNotSet');
    expect(notSet).toBe(i18n.language === 'en' ? 'Local path: Not set' : 'ローカルパス: 未設定');
    const button = switcherButton();
    expect(button).not.toHaveAttribute('title');
    expect(button).toHaveAccessibleName('Beta');
    expect(button).toHaveAccessibleDescription(notSet);
    expect(await hoverTooltipLines(user)).toEqual(['Beta', notSet]);
  });

  it('puts a long name in the tooltip in full, keeping it out of the description', async () => {
    const user = await renderWith(long.id);
    expect(switcherButton()).toHaveAccessibleDescription('/work/long');
    expect(await hoverTooltipLines(user)).toEqual([LONG, '/work/long']);
  });

  it('names the button by the whole of a name with no spaces, and describes it without the name', async () => {
    await renderWith(noSpace.id);
    const button = switcherButton();
    expect(button).toHaveAccessibleName(NO_SPACE);
    expect(button).toHaveAccessibleDescription('/work/nospace');
    expect(button).not.toHaveAccessibleDescription(expect.stringContaining(NO_SPACE));
  });

  it('gives the button no tooltip and no description when there is no project', async () => {
    installFakeBackend(createFakeBackend({ projects: [], currentProjectId: '', labels: [], tickets: [] }));
    const user = userEvent.setup();
    render(<App />);
    await screen.findByText(i18n.t('projectSwitcher.noProjectYet'));
    const button = switcherButton();
    expect(button).toHaveTextContent(i18n.t('projectSwitcher.noProject'));
    expect(button).not.toHaveAttribute('title');
    expect(button).not.toHaveAttribute('aria-describedby');
    expect(button).toHaveAccessibleDescription('');
    await user.hover(button);
    await waitForHoverOpenDelay();
    await user.tab();
    expect(allIconButtonTooltips().filter(t => !t.hidden && t.textContent?.includes(i18n.t('projectSwitcher.noProject')))).toHaveLength(0);
  });

  it('wraps every item\'s name in the popup instead of truncating it', async () => {
    const user = await renderWith(alpha.id);
    const popup = await openPopup(user);
    for (const p of [alpha, beta, long, noSpace]) {
      const name = itemName(popup, p.name);
      expect(name).not.toHaveClass('truncate');
      expect(name).toHaveClass('min-w-0', 'wrap-anywhere');
      // The whole name is the text: nothing cut off.
      expect(name.textContent).toBe(p.name);
      expect(name.closest('button')?.parentElement).toBe(popup);
    }
  });

  it('keeps w-64 (PROJECT_MENU_WIDTH_REM) and puts the popup under the button, with no cap from the window\'s width alone', async () => {
    const user = await renderWith(alpha.id);
    const popup = await openPopup(user);
    expect(popup).toHaveClass('absolute', 'left-0', 'top-full', 'mt-1.5', 'w-64');
    expect(popup).not.toHaveClass('@container');
    expect(popup).not.toHaveClass('max-w-[max(calc(100vw-2rem),8rem)]');
    expect(popup.className).not.toMatch(/(^|\s)max-w-/);
    // w-64 is 16rem: the width the placement is worked out from.
    expect(PROJECT_MENU_WIDTH_REM).toBe(16);
  });

  it('keeps an item\'s badge and prefix on the name\'s line at every width', async () => {
    const user = await renderWith(alpha.id);
    const popup = await openPopup(user);
    for (const p of [alpha, beta, long, noSpace]) {
      const name = itemName(popup, p.name);
      const item = name.closest('button') as HTMLElement;
      const right = name.nextElementSibling as HTMLElement;
      expect(item).toHaveClass('flex', 'items-center', 'px-3');
      for (const el of [item, name, right]) expect(Array.from(el.classList).filter(c => c.startsWith('cq-'))).toEqual([]);
      expect(item.className).not.toMatch(/flex-wrap/);
      expect(right).toHaveClass('ml-auto', 'shrink-0');
    }
  });

  it('lets the "new project" text wrap inside the popup, keeping the icon\'s size', async () => {
    const user = await renderWith(alpha.id);
    const popup = await openPopup(user);
    const text = within(popup).getByText(i18n.t('projectSwitcher.createNew'));
    expect(text.tagName).toBe('SPAN');
    expect(text).toHaveClass('min-w-0', 'wrap-anywhere');
    const item = text.closest('button') as HTMLElement;
    expect(item).toHaveAccessibleName(i18n.t('projectSwitcher.createNew'));
    expect(item.querySelector('svg')).toHaveClass('shrink-0');
  });

  it('keeps the prefix and the pending-approval badge in the group on the item\'s right', async () => {
    const user = await renderWith(alpha.id);
    const popup = await openPopup(user);
    // The counts are fetched when the popup opens (GET
    // /api/projects/pending-approvals) and arrive after it: wait for both
    // badges (DFLT-00296).
    await waitFor(() => expect(within(popup).getAllByRole('img')).toHaveLength(2));
    const cases: Array<[Project, number | null]> = [
      [alpha, null],
      [beta, 2],
      [long, 1],
      [noSpace, null]
    ];
    for (const [p, count] of cases) {
      const name = itemName(popup, p.name);
      const right = name.nextElementSibling as HTMLElement;
      expect(right).toHaveClass('ml-auto', 'shrink-0');
      expect(within(right).getByText(p.prefix)).toBeInTheDocument();
      if (count === null) {
        expect(right.children).toHaveLength(1);
      } else {
        expect(right.children).toHaveLength(2);
        expect(right.firstElementChild).toHaveTextContent(String(count));
      }
    }
  });
});

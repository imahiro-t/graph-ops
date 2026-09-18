import React from 'react';

interface Props {
  content: string;
  // DFLT-00085: when true this bordered box itself becomes a 16rem-capped
  // scroll container, so inline artifact previews stay skimmable in a list.
  // The box that owns the border is the scroller on purpose (rather than an
  // outer wrapper) so the border stays put and only the content moves --
  // that fixed frame is what makes it read as "there is more inside".
  // Default false keeps the full-height render used by the ticket
  // description and by the open-in-a-new-tab preview page.
  scrollable?: boolean;
  // Accessible name for that scroll region. A keyboard user has to be able
  // to reach and scroll the box (WCAG 2.1.1), which means it takes focus --
  // and a focusable region needs a name saying which artifact it holds.
  label?: string;
}

// Text color for each of the three "real" step keywords And/But continue.
// Kept as a lookup (rather than each block below picking its own shade) so
// And/But can share it exactly instead of drifting to their own arbitrary
// color -- see lastStepKeyword below.
const stepKeywordColor: Record<'Given' | 'When' | 'Then', string> = {
  Given: 'text-blue-600',
  When: 'text-amber-600',
  Then: 'text-emerald-600'
};

export const GherkinViewer: React.FC<Props> = ({ content, scrollable = false, label }) => {
  const lines = content.split('\n');

  // And/But are continuations of whichever of Given/When/Then most recently
  // appeared (in the current scenario) -- they have no step type of their
  // own, so they must not get an independent color and instead take on
  // whatever that last step's color was. Mutated in place as renderLine
  // walks the lines in order below; reset on a new Scenario since each
  // scenario's Given/When/Then sequence starts over.
  let lastStepKeyword: 'Given' | 'When' | 'Then' | null = null;

  const renderLine = (line: string, index: number) => {
    const trimmed = line.trim();

    // Feature / Requirement
    if (trimmed.startsWith('Feature:')) {
      return (
        <div key={index} className="font-bold text-indigo-700 dark:text-indigo-400 py-0.5">
          <span className="bg-indigo-100 dark:bg-indigo-950 text-indigo-800 dark:text-indigo-300 px-1.5 py-0.5 rounded text-[11px] mr-2">Feature</span>
          {line.replace(/^(\s*)Feature:\s*/, '')}
        </div>
      );
    }

    // Scenario -- a new scenario restarts the Given/When/Then sequence, so
    // And/But right after it (before any real step keyword) fall back to a
    // neutral color rather than carrying over the previous scenario's last
    // step type.
    if (trimmed.startsWith('Scenario:')) {
      lastStepKeyword = null;
      return (
        <div key={index} className="font-semibold text-slate-800 dark:text-slate-200 mt-2 py-0.5">
          <span className="bg-slate-200 dark:bg-slate-700 text-slate-800 dark:text-slate-200 px-1.5 py-0.5 rounded text-[11px] mr-2">Scenario</span>
          {line.replace(/^(\s*)Scenario:\s*/, '')}
        </div>
      );
    }

    // Given (blue)
    if (trimmed.startsWith('Given ')) {
      lastStepKeyword = 'Given';
      const match = line.match(/^(\s*)Given (.*)$/);
      const indent = match ? match[1] : '';
      const rest = match ? match[2] : trimmed.substring(6);
      return (
        <div key={index} className="text-slate-700 dark:text-slate-300 py-0.5 flex items-start">
          <span className="font-mono text-slate-300 dark:text-slate-700 select-none mr-2 whitespace-pre">{indent}</span>
          <span className={`font-bold ${stepKeywordColor.Given} w-16 shrink-0`}>Given</span>
          <span className="text-slate-800 dark:text-slate-200">{rest}</span>
        </div>
      );
    }

    // When (orange/amber)
    if (trimmed.startsWith('When ')) {
      lastStepKeyword = 'When';
      const match = line.match(/^(\s*)When (.*)$/);
      const indent = match ? match[1] : '';
      const rest = match ? match[2] : trimmed.substring(5);
      return (
        <div key={index} className="text-slate-700 dark:text-slate-300 py-0.5 flex items-start">
          <span className="font-mono text-slate-300 dark:text-slate-700 select-none mr-2 whitespace-pre">{indent}</span>
          <span className={`font-bold ${stepKeywordColor.When} w-16 shrink-0`}>When</span>
          <span className="text-slate-800 dark:text-slate-200">{rest}</span>
        </div>
      );
    }

    // Then (green)
    if (trimmed.startsWith('Then ')) {
      lastStepKeyword = 'Then';
      const match = line.match(/^(\s*)Then (.*)$/);
      const indent = match ? match[1] : '';
      const rest = match ? match[2] : trimmed.substring(5);
      return (
        <div key={index} className="text-slate-700 dark:text-slate-300 py-0.5 flex items-start">
          <span className="font-mono text-slate-300 dark:text-slate-700 select-none mr-2 whitespace-pre">{indent}</span>
          <span className={`font-bold ${stepKeywordColor.Then} w-16 shrink-0`}>Then</span>
          <span className="text-slate-800 dark:text-slate-200 font-medium">{rest}</span>
        </div>
      );
    }

    // And/But: continuations of whichever step (Given/When/Then) came
    // before them, not steps of their own -- colored to match that step
    // (see lastStepKeyword) rather than an independent color. Falls back to
    // a neutral slate if one somehow appears before any real step keyword
    // in its scenario.
    if (trimmed.startsWith('And ') || trimmed.startsWith('But ')) {
      const keyword = trimmed.startsWith('And ') ? 'And' : 'But';
      const match = line.match(new RegExp(`^(\\s*)${keyword} (.*)$`));
      const indent = match ? match[1] : '';
      const rest = match ? match[2] : trimmed.substring(keyword.length + 1);
      const color = lastStepKeyword ? stepKeywordColor[lastStepKeyword] : 'text-slate-500 dark:text-slate-400';
      return (
        <div key={index} className="text-slate-700 dark:text-slate-300 py-0.5 flex items-start">
          <span className="font-mono text-slate-300 dark:text-slate-700 select-none mr-2 whitespace-pre">{indent}</span>
          <span className={`font-bold ${color} w-16 shrink-0`}>{keyword}</span>
          <span className="text-slate-800 dark:text-slate-200">{rest}</span>
        </div>
      );
    }

    // Default line
    return (
      <div key={index} className="text-slate-500 dark:text-slate-400 py-0.5 whitespace-pre">
        {line || '\u00A0'}
      </div>
    );
  };

  return (
    <div
      data-testid="gherkin-viewer"
      // max-h-64 (not h-64) so a short artifact still renders at its own
      // height instead of being stretched to 16rem with dead space below.
      // The focus ring is blue-500 (not indigo-400): outline-none removes the
      // UA's own focus indicator, so the ring alone has to clear WCAG 1.4.11 /
      // 2.4.11's 3:1 against what it sits on. indigo-400 (#818cf8) only
      // reached 2.85-2.98:1 on the light theme's white/slate-50 backgrounds;
      // blue-500 (#3b82f6) is 3.68:1 / 3.52:1 there and 3.98:1 / 4.85:1 on
      // dark slate-800/900 -- and it matches settings/TemplateTextEditor,
      // the other scroll region in this app.
      className={`bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-3 font-mono text-xs overflow-x-auto shadow-inner${
        scrollable ? ' max-h-64 overflow-y-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500' : ''
      }`}
      tabIndex={scrollable ? 0 : undefined}
      role={scrollable && label ? 'region' : undefined}
      aria-label={scrollable ? label : undefined}
    >
      {lines.map((l, i) => renderLine(l, i))}
    </div>
  );
};

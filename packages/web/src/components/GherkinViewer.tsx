import React from 'react';

interface Props {
  content: string;
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

export const GherkinViewer: React.FC<Props> = ({ content }) => {
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
    <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg p-3 font-mono text-xs overflow-x-auto shadow-inner">
      {lines.map((l, i) => renderLine(l, i))}
    </div>
  );
};

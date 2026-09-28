// DFLT-00270: guards for the Tailwind v4 migration's v3-compatibility layer.
//
// index.css restores v3's space-y-* / divide-* behaviour (gap and border on
// every child but the first, specificity 0,3,0) value by value. A value it
// does not list silently gets v4's behaviour instead (a child's own mt-*
// adds to the gap, a divider moves by a pixel), and nothing else -- build,
// lint or the component tests -- would notice. So every space-* / divide-*
// class the sources use, variants included, has to have both its
// zero-specificity reset (`:where(...) > *`) and its v3 rule
// (`.<class> > :not([hidden]) ~ :not([hidden])`) in index.css.
//
// It also checks that no element carries both wrap-break-word and
// wrap-anywhere as plain classes: on v4 wrap-break-word is emitted after
// wrap-anywhere and wins, undoing the anywhere break the reflow fixes rely
// on (on v3 the arbitrary [overflow-wrap:anywhere] came last and won).
//
// DFLT-00274 adds two guards that work on class expressions found through
// the TypeScript syntax tree rather than by regular expressions:
//
// - No class name is glued to a template's `${` (`inset-shadow-sm${...}`).
//   v4's Oxide scanner does not take such a token as a class candidate, so
//   the class is silently missing from the CSS (DFLT-00270 lost the
//   Gherkin / Markdown viewers' inner shadow this way). Separate them with a
//   space: `inset-shadow-sm ${...}`.
// - wrap-break-word and wrap-anywhere are not combined anywhere in one class
//   expression, even when they come from different literals (a template and
//   a ternary inside it, a `+` concatenation, a constant it refers to). The
//   union of the expression's strings is checked, so two branches of a
//   ternary that never apply together are still reported (conservatively):
//   split such a case into separate class expressions.
//
// A class expression is:
// (a) a className / class / *ClassName JSX attribute;
// (b) the initializer of a variable, property or parameter default (a
//     destructured one included) whose name ends in class / classes /
//     classname / classnames, any case (inputClass, ERROR_BOX_CLASS,
//     PENDING_APPROVAL_BADGE_CLASSES, symbolClass);
// (c) the initializer of any same-file `const` a class expression refers to
//     by name, whatever it is called (LabelsEditor's `dim`, GherkinViewer's
//     block-scoped `color`), followed transitively. The object of a property
//     or element access is resolved too (`STYLE[b]` takes in AutopilotBadges'
//     `STYLE` map, `stepKeywordColor.Given` GherkinViewer's), the accessed
//     key is not.
// Conditions (a ternary's test, a comparison, `!x`, the left side of `&&`)
// only choose between class lists and are skipped. Function calls are
// opaque: their arguments (an i18n key built for t(), say) are neither
// collected nor resolved, and a called function's body is not followed -- a
// function whose own name ends in class (artifactTabClass) is a class
// expression by (b) instead. Imports, `let` variables and parameters are not
// followed either, so a class string that only arrives through them
// (statusMeta.chip.bg and the other *Meta files, which list complete class
// names statically) is outside these checks.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

// process.cwd() is the Vitest root, i.e. packages/web.
const WEB_ROOT = process.cwd();
const SRC = join(WEB_ROOT, 'src');

const sourceFiles = (dir: string): string[] =>
  readdirSync(dir).flatMap(name => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return sourceFiles(path);
    return /\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name) ? [path] : [];
  });

// Blanks out comments, keeping line numbers. A `//` right after a colon or a
// quote (a URL, or a string that starts with one) is left alone.
const stripComments = (text: string) =>
  text
    .replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:'"`])\/\/[^\n]*/g, (m, lead: string) => lead + ' '.repeat(m.length - lead.length));

const SOURCES = [
  ...sourceFiles(SRC).map(path => ({ path, text: stripComments(readFileSync(path, 'utf8')) })),
  { path: join(WEB_ROOT, 'index.html'), text: readFileSync(join(WEB_ROOT, 'index.html'), 'utf8') }
];

// space-x-*, space-y-*, divide-* (widths, colors, styles, reverse), with any
// variants in front and an optional negative sign.
const SPACE_DIVIDE =
  /(?<![\w\-:[\].\\])((?:[\w\-[\]@()&.:]+:)?-?(?:space-[xy]|divide)-[\w.[\]/-]*[\w\]])/g;

const usedSpaceDivideClasses = () => {
  const found = new Map<string, string>();
  for (const { path, text } of SOURCES) {
    for (const m of text.matchAll(SPACE_DIVIDE)) {
      if (!found.has(m[1])) found.set(m[1], relative(WEB_ROOT, path));
    }
  }
  return found;
};

const cssEscape = (cls: string) => cls.replace(/[^\w-]/g, c => `\\${c}`);
const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const INDEX_CSS = readFileSync(join(SRC, 'index.css'), 'utf8');

// The selector lists of the `:where(...) > *` resets (allowing one level of
// nested parentheses, for `:is(.dark *)`).
const RESET_LISTS = [...INDEX_CSS.matchAll(/:where\(((?:[^()]|\([^()]*\))*)\)\s*>\s*\*/g)].map(m => m[1]);

// `.<class>` not followed by more of a class name (so space-y-1 does not
// match .space-y-1\.5).
const classSelector = (cls: string) => new RegExp(`\\.${escapeRegExp(cssEscape(cls))}(?![\\w\\\\-])`);

describe('Tailwind v3 compatibility in index.css', () => {
  const used = usedSpaceDivideClasses();

  it('finds the space-y / divide classes the app uses (the scan is not vacuous)', () => {
    for (const cls of ['space-y-2', 'space-y-0.5', 'divide-y', 'sm:divide-x', 'dark:divide-slate-800']) {
      expect([...used.keys()]).toContain(cls);
    }
  });

  it('has a reset and a v3 rule for every space-* / divide-* class used in src/ and index.html', () => {
    const missing: string[] = [];
    for (const [cls, file] of used) {
      const selector = classSelector(cls);
      const hasReset = RESET_LISTS.some(list => selector.test(list));
      const hasV3Rule = INDEX_CSS.includes(`.${cssEscape(cls)} > :not([hidden]) ~ :not([hidden])`);
      if (!hasReset || !hasV3Rule) {
        missing.push(`${cls} (${file}): ${hasReset ? '' : 'no :where(...) > * reset; '}${hasV3Rule ? '' : 'no v3 rule'}`);
      }
    }
    expect(missing).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// Class expressions, found through the TypeScript syntax tree (DFLT-00274).

// Names whose initializer is a class list by convention.
const CLASS_NAMED = /class(es|name|names)?$/i;
const isClassAttribute = (name: string) => name === 'className' || name === 'class' || name.endsWith('ClassName');

type ClassExpression = {
  // Path relative to packages/web, and the 1-based line of the expression.
  file: string;
  line: number;
  // The attribute / variable / property name that made it a class expression.
  origin: string;
  // Every string piece collected from it (literals, template heads and
  // spans), constants it refers to included.
  pieces: string[];
  // Template literals in it, constants it refers to included.
  templates: ts.TemplateExpression[];
  // Same-file constants it took in, by name.
  resolved: string[];
  sourceFile: ts.SourceFile;
};

const parseSource = (fileName: string, text: string) =>
  ts.createSourceFile(
    fileName,
    text,
    ts.ScriptTarget.Latest,
    true,
    fileName.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS
  );

const lineOf = (sourceFile: ts.SourceFile, node: ts.Node) =>
  sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;

const isFunctionLike = (node: ts.Node): node is ts.SignatureDeclaration & { parameters: ts.NodeArray<ts.ParameterDeclaration> } =>
  ts.isFunctionDeclaration(node) ||
  ts.isFunctionExpression(node) ||
  ts.isArrowFunction(node) ||
  ts.isMethodDeclaration(node);

// The same-file `const` an identifier refers to: the innermost block (or the
// file) that declares it directly, walking outwards from the identifier. A
// function parameter of the same name on the way shadows it (not resolved).
// TDZ and other finer scoping rules are not modelled.
const resolveConst = (id: ts.Identifier): ts.VariableDeclaration | undefined => {
  for (let scope: ts.Node | undefined = id.parent; scope; scope = scope.parent) {
    if (isFunctionLike(scope) && scope.parameters.some(p => ts.isIdentifier(p.name) && p.name.text === id.text)) {
      return undefined;
    }
    if (ts.isBlock(scope) || ts.isSourceFile(scope) || ts.isModuleBlock(scope)) {
      for (const statement of scope.statements) {
        if (!ts.isVariableStatement(statement)) continue;
        if (!(statement.declarationList.flags & ts.NodeFlags.Const)) continue;
        for (const declaration of statement.declarationList.declarations) {
          if (ts.isIdentifier(declaration.name) && declaration.name.text === id.text) return declaration;
        }
      }
    }
  }
  return undefined;
};

// Whether an identifier stands for a value that could be resolved, as
// opposed to a name being declared or a key being accessed.
const isValueReference = (id: ts.Identifier) => {
  const parent = id.parent;
  if (ts.isPropertyAccessExpression(parent) && parent.name === id) return false;
  if (ts.isElementAccessExpression(parent) && parent.argumentExpression === id) return false;
  if (ts.isPropertyAssignment(parent) && parent.name === id) return false;
  if (
    (ts.isVariableDeclaration(parent) || ts.isParameter(parent) || ts.isBindingElement(parent) || ts.isFunctionDeclaration(parent)) &&
    parent.name === id
  ) {
    return false;
  }
  return true;
};

// Collects the string pieces and templates under a class expression,
// following same-file constants (each declaration at most once).
const collectClassParts = (root: ts.Node) => {
  const pieces: string[] = [];
  const templates: ts.TemplateExpression[] = [];
  const resolved: string[] = [];
  const seen = new Set<ts.Node>();
  const walk = (node: ts.Node): void => {
    if (ts.isTypeNode(node)) return;
    if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
      // The callee is only walked for a property access's object
      // (`obj.method()`); the arguments not at all.
      if (ts.isPropertyAccessExpression(node.expression)) walk(node.expression.expression);
      return;
    }
    if (ts.isElementAccessExpression(node)) {
      walk(node.expression);
      return;
    }
    // Conditions only choose between class lists: a ternary's test, a
    // comparison, a negation and the left side of && contribute no classes.
    if (ts.isConditionalExpression(node)) {
      walk(node.whenTrue);
      walk(node.whenFalse);
      return;
    }
    if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) return;
    if (ts.isBinaryExpression(node)) {
      const op = node.operatorToken.kind;
      if (op === ts.SyntaxKind.AmpersandAmpersandToken) {
        walk(node.right);
        return;
      }
      if (op !== ts.SyntaxKind.PlusToken && op !== ts.SyntaxKind.BarBarToken && op !== ts.SyntaxKind.QuestionQuestionToken) return;
    }
    if (ts.isPropertyAssignment(node)) {
      walk(node.initializer);
      return;
    }
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
      pieces.push(node.text);
      return;
    }
    if (ts.isTemplateExpression(node)) {
      templates.push(node);
      pieces.push(node.head.text);
      for (const span of node.templateSpans) {
        walk(span.expression);
        pieces.push(span.literal.text);
      }
      return;
    }
    if (ts.isIdentifier(node)) {
      if (!isValueReference(node)) return;
      const declaration = resolveConst(node);
      if (declaration?.initializer && !seen.has(declaration)) {
        seen.add(declaration);
        resolved.push(node.text);
        walk(declaration.initializer);
      }
      return;
    }
    ts.forEachChild(node, walk);
  };
  walk(root);
  return { pieces, templates, resolved };
};

const collectClassExpressions = (fileName: string, text: string): ClassExpression[] => {
  const sourceFile = parseSource(fileName, text);
  const found: ClassExpression[] = [];
  const add = (origin: string, node: ts.Node) =>
    found.push({ file: fileName, line: lineOf(sourceFile, node), origin, sourceFile, ...collectClassParts(node) });
  const visit = (node: ts.Node): void => {
    if (ts.isJsxAttribute(node) && node.initializer && isClassAttribute(node.name.getText(sourceFile))) {
      const value = ts.isJsxExpression(node.initializer) ? node.initializer.expression : node.initializer;
      if (value) add(node.name.getText(sourceFile), value);
    } else if (
      (ts.isVariableDeclaration(node) ||
        ts.isPropertyAssignment(node) ||
        ts.isParameter(node) ||
        ts.isBindingElement(node)) &&
      node.initializer &&
      (ts.isIdentifier(node.name) || ts.isStringLiteral(node.name)) &&
      CLASS_NAMED.test(node.name.text)
    ) {
      add(node.name.text, node.initializer);
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return found;
};

// index.html has no syntax tree here: its class="..." values are the class
// expressions.
const collectHtmlClassExpressions = (fileName: string, text: string) =>
  [...text.matchAll(/\bclass\s*=\s*(["'])([\s\S]*?)\1/g)].map(m => ({
    file: fileName,
    line: text.slice(0, m.index).split('\n').length,
    value: m[2]
  }));

// A class-name character right before `${`: a letter, a digit, or the `]`,
// `)` or `-` an arbitrary value / variant / name can end with.
const GLUED_BEFORE = /[A-Za-z0-9\])-]$/;
const GLUED_IN_TEXT = /[A-Za-z0-9\])-]\$\{/g;

const gluedSnippet = (staticText: string) => `${staticText.split(/\s+/).pop()}\${`;

// Every place in a file's class expressions where a class name runs straight
// into `${`, as "file:line: <class>${". A template reached from several
// class expressions is reported once.
const findGluedTemplateClasses = (fileName: string, text: string): string[] => {
  if (fileName.endsWith('.html')) {
    return collectHtmlClassExpressions(fileName, text).flatMap(({ file, line, value }) =>
      [...value.matchAll(GLUED_IN_TEXT)].map(m => `${file}:${line}: ${gluedSnippet(value.slice(0, m.index + 1))}`)
    );
  }
  const offenders: string[] = [];
  const reported = new Set<ts.Node>();
  for (const { sourceFile, templates } of collectClassExpressions(fileName, text)) {
    for (const template of templates) {
      if (reported.has(template)) continue;
      // The static text before each `${`: the head, and every span's literal
      // but the last (which closes the template).
      const beforeSubstitutions = [template.head, ...template.templateSpans.slice(0, -1).map(s => s.literal)];
      const glued = beforeSubstitutions.filter(part => GLUED_BEFORE.test(part.text));
      if (glued.length === 0) continue;
      reported.add(template);
      for (const part of glued) offenders.push(`${fileName}:${lineOf(sourceFile, part)}: ${gluedSnippet(part.text)}`);
    }
  }
  return offenders;
};

const classTokens = (pieces: string[]) => new Set(pieces.flatMap(piece => piece.split(/\s+/)).filter(Boolean));

const hasWrapConflict = (tokens: Set<string>) => tokens.has('wrap-break-word') && tokens.has('wrap-anywhere');

// Every class expression in a file that has both plain wrap-break-word and
// wrap-anywhere among all of its pieces, as "file:line (origin)".
const findWrapConflicts = (fileName: string, text: string): string[] => {
  if (fileName.endsWith('.html')) {
    return collectHtmlClassExpressions(fileName, text)
      .filter(({ value }) => hasWrapConflict(classTokens([value])))
      .map(({ file, line }) => `${file}:${line} (class)`);
  }
  return collectClassExpressions(fileName, text)
    .filter(({ pieces }) => hasWrapConflict(classTokens(pieces)))
    .map(({ file, line, origin }) => `${file}:${line} (${origin})`);
};

// The real sources, parsed from the original text (comments are not in the
// syntax tree, so stripComments is not needed), with paths relative to
// packages/web.
const RAW_SOURCES = [...sourceFiles(SRC), join(WEB_ROOT, 'index.html')].map(path => ({
  file: relative(WEB_ROOT, path),
  text: readFileSync(path, 'utf8')
}));

const REAL_CLASS_EXPRESSIONS = RAW_SOURCES.filter(({ file }) => !file.endsWith('.html')).flatMap(({ file, text }) =>
  collectClassExpressions(file, text)
);

const INDEX_HTML_CLASSES = collectHtmlClassExpressions('index.html', readFileSync(join(WEB_ROOT, 'index.html'), 'utf8'));

// The class expressions of one real file whose tokens include all of `tokens`.
const realExpressionsWith = (file: string, tokens: string[]) =>
  REAL_CLASS_EXPRESSIONS.filter(e => e.file === file && tokens.every(t => classTokens(e.pieces).has(t)));

describe('Tailwind v4 scanner: class names glued to ${', () => {
  it('finds no class name glued to ${ in src/ and index.html', () => {
    const offenders = RAW_SOURCES.flatMap(({ file, text }) => findGluedTemplateClasses(file, text));
    expect(offenders).toEqual([]);
  });

  it.each([
    ['className template', "<div className={`p-2 relative${x ? ' a' : ''}`} />", 'relative${'],
    ['*ClassName attribute', '<span wrapperClassName={`inline-flex${c}`} />', 'inline-flex${'],
    ['*Class variable', 'const inputClass = `border rounded-sm${x}`;', 'rounded-sm${'],
    ['*Class arrow function', "const artifactTabClass = (a: boolean) => `px-3 py-2${a ? ' x' : ''}`;", 'py-2${'],
    ['*_CLASSES constant', 'export const BADGE_CLASSES = `bg-amber-100${x}`;', 'bg-amber-100${'],
    [
      'constant referred to by a className, whatever its name',
      "const dim = size === 'md' ? `w-6 h-6${s}` : 'w-4 h-4'; const E = () => <span className={`${dim} rounded-full`} />;",
      'h-6${'
    ],
    [
      'block-scoped constant in a function',
      "function F() { const color = c ? 'a' : `text-slate-500${d}`; return <span className={`font-bold ${color}`} />; }",
      'text-slate-500${'
    ],
    ['constants followed transitively', 'const A = `px-2${x}`; const B = `${A} py-1`; const E = () => <div className={B} />;', 'px-2${'],
    ['nested template', "const E = () => <div className={`a ${c ? `b-1${d}` : ''}`} />;", 'b-1${'],
    ['arbitrary variant ending in ]', 'const E = () => <div className={`[@media(max-width:15rem)]:px-2 m-[3px]${x}`} />;', 'm-[3px]${'],
    ['property of a class-named key', "const meta = { symbolClass: `font-bold${x}` };", 'font-bold${'],
    ['class-named parameter default', 'function F({ className = `p-1${x}` }) { return className; }', 'p-1${']
  ])('reports a class name glued to ${ (%s)', (_label, source, snippet) => {
    const offenders = findGluedTemplateClasses('sample.tsx', source);
    expect(offenders).toHaveLength(1);
    expect(offenders[0]).toContain(snippet);
  });

  it('reports a class name glued to ${ in an HTML class attribute', () => {
    expect(findGluedTemplateClasses('index.html', '<body class="min-h-screen${x}">')).toEqual([
      'index.html:1: min-h-screen${'
    ]);
    expect(findGluedTemplateClasses('index.html', '<body class="min-h-screen" data-x="a${b}">')).toEqual([]);
  });

  it.each([
    ['id', '<div id={`ticket-${id}`} />'],
    ['data-testid', '<div data-testid={`row-${id}`} />'],
    ['aria-describedby', "<input aria-describedby={`${fieldId}-hint${p ? ' x' : ''}`} />"],
    ['htmlFor', '<label htmlFor={`${p}-name`} />'],
    ['i18n key', 'const label = t(`settings.mysqlTls${mode}`);'],
    ['URL', 'fetch(`/api/tickets/${id}`);'],
    ['key', '<li key={`${a}-${b}`} />'],
    ['constant only used outside class expressions', 'const rowId = `row-${id}`; const E = () => <div id={rowId} className="p-2" />;'],
    ['function-call argument inside a class expression', 'const E = () => <div className={`p-2 ${cls(t(`k.a${m}`))}`} />;'],
    ['element-access key inside a class expression', 'const K = `k${m}`; const E = () => <div className={`p-2 ${STYLE[K]}`} />;'],
    ['class names separated from ${ by spaces', "const E = () => <header className={`relative ${c ? 'lg:sticky' : ''} z-30`} />;"],
    ['condition choosing between class lists', "const E = () => <div className={mode === `m${x}` ? 'p-1' : 'p-2'} />;"],
    ['parameter shadowing a constant', 'const x = `a-${b}`; const E = (x: string) => <div className={`p-2 ${x}`} />;']
  ])('does not report non-class template strings (%s)', (_label, source) => {
    expect(findGluedTemplateClasses('sample.tsx', source)).toEqual([]);
  });

  it('actually collects the class expressions of the real sources (the scan is not vacuous)', () => {
    const withTemplates = REAL_CLASS_EXPRESSIONS.filter(e => e.templates.length > 0);
    // 49 when this was written; the bound leaves room for refactoring.
    expect(withTemplates.length).toBeGreaterThanOrEqual(40);

    // Known template class expressions.
    expect(
      REAL_CLASS_EXPRESSIONS.some(e => e.file === 'src/components/GherkinViewer.tsx' && e.templates.length > 0 && classTokens(e.pieces).has('inset-shadow-sm'))
    ).toBe(true);
    expect(realExpressionsWith('src/App.tsx', ['relative', 'lg:sticky', 'z-30'])).not.toEqual([]);
    expect(realExpressionsWith('src/components/IconButton.tsx', ['inline-flex'])).not.toEqual([]);
    expect(INDEX_HTML_CLASSES.some(({ value }) => classTokens([value]).has('min-h-screen'))).toBe(true);

    // Constants taken in by name, and by the naming convention.
    const resolvedIn = (file: string, name: string) =>
      REAL_CLASS_EXPRESSIONS.filter(e => e.file === file && e.resolved.includes(name));
    expect(resolvedIn('src/components/settings/ReviewGatesEditor.tsx', 'SMALL_LABEL_CLASS')).not.toEqual([]);
    expect(REAL_CLASS_EXPRESSIONS.some(e => e.file === 'src/components/TicketItem.tsx' && e.origin === 'artifactTabClass')).toBe(true);

    // The constants plan review 1 found outside a name-only convention.
    const badge = resolvedIn('src/components/PendingApprovalBadge.tsx', 'PENDING_APPROVAL_BADGE_CLASSES');
    expect(badge.some(e => e.origin === 'className' && classTokens(e.pieces).has('bg-amber-100'))).toBe(true);
    expect(REAL_CLASS_EXPRESSIONS.some(e => e.origin === 'PENDING_APPROVAL_BADGE_CLASSES')).toBe(true);
    const dim = resolvedIn('src/components/settings/LabelsEditor.tsx', 'dim');
    expect(dim.some(e => classTokens(e.pieces).has('w-6') && classTokens(e.pieces).has('rounded-full'))).toBe(true);
    const color = resolvedIn('src/components/GherkinViewer.tsx', 'color');
    expect(color.some(e => classTokens(e.pieces).has('text-slate-500') && classTokens(e.pieces).has('font-bold'))).toBe(true);
  });
});

describe('overflow-wrap classes', () => {
  it('never puts plain wrap-break-word and wrap-anywhere in the same string literal', () => {
    // Kept alongside the class-expression check below: it also covers
    // strings outside any class expression (a map of class lists, say).
    const offenders: string[] = [];
    for (const { path, text } of SOURCES) {
      // Each string / template literal is one class list (or part of one).
      for (const m of text.matchAll(/(['"`])((?:(?!\1)[^\\]|\\.)*)\1/g)) {
        const classes = new Set(m[2].split(/\s+/));
        if (classes.has('wrap-break-word') && classes.has('wrap-anywhere')) {
          const line = text.slice(0, m.index).split('\n').length;
          offenders.push(`${relative(WEB_ROOT, path)}:${line}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it('never puts plain wrap-break-word and wrap-anywhere in one class expression', () => {
    const offenders = RAW_SOURCES.flatMap(({ file, text }) => findWrapConflicts(file, text));
    expect(offenders).toEqual([]);
  });

  it.each([
    ['a template and a literal inside it', "const E = () => <div className={`wrap-anywhere ${c ? 'wrap-break-word' : ''}`} />;"],
    ['a + concatenation', "const E = () => <div className={'wrap-anywhere ' + (c ? 'wrap-break-word' : '')} />;"],
    ['a class-named constant', "const BASE_CLASS = 'wrap-break-word'; const E = () => <div className={`${BASE_CLASS} wrap-anywhere`} />;"],
    ['a constant of any name', "const w = 'wrap-anywhere'; const E = () => <div className={`wrap-break-word ${w}`} />;"],
    ['the object of an element access', "const S = { a: 'wrap-anywhere' }; const E = () => <div className={`wrap-break-word ${S[k]}`} />;"],
    ['exclusive ternary branches (reported conservatively)', "const E = () => <div className={c ? 'wrap-anywhere' : 'wrap-break-word'} />;"]
  ])('reports wrap-break-word combined with wrap-anywhere across %s', (_label, source) => {
    expect(findWrapConflicts('sample.tsx', source)).toHaveLength(1);
  });

  it('reports the combination in an HTML class attribute', () => {
    expect(findWrapConflicts('index.html', '<p class="wrap-anywhere wrap-break-word">')).toEqual(['index.html:1 (class)']);
  });

  it.each([
    ['a variant on one of them', "const E = () => <div className={`wrap-break-word ${c ? 'max-sm:wrap-anywhere' : ''}`} />;"],
    ['separate elements', "const E = () => <><div className=\"wrap-break-word\" /><div className=\"wrap-anywhere\" /></>;"]
  ])('does not report %s', (_label, source) => {
    expect(findWrapConflicts('sample.tsx', source)).toEqual([]);
  });

  it('checks whole class expressions in the real sources (the scan is not vacuous)', () => {
    // ConfirmDialog's confirm button: wrap-break-word in the template and
    // bg-red-600 in the ternary inside it end up in one token set.
    expect(realExpressionsWith('src/components/ConfirmDialog.tsx', ['wrap-break-word', 'bg-red-600'])).not.toEqual([]);
    // ErrorBox's className takes in ERROR_BOX_CLASS by resolving it.
    expect(
      REAL_CLASS_EXPRESSIONS.some(
        e => e.file === 'src/components/settings/ErrorBox.tsx' && e.origin === 'className' && e.resolved.includes('ERROR_BOX_CLASS')
      )
    ).toBe(true);
    // AutopilotBadges' badge: wrap-anywhere in the template, and the STYLE
    // map's class lists through STYLE[b].
    expect(realExpressionsWith('src/components/AutopilotBadges.tsx', ['wrap-anywhere', 'bg-violet-50'])).not.toEqual([]);
  });
});

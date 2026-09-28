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
// (b) the initializer of a variable, property (object or class) or parameter
//     default (a destructured one included), or the body of a function
//     declaration, method or getter, whose name ends in class / classes /
//     classname / classnames, any case (inputClass, ERROR_BOX_CLASS,
//     PENDING_APPROVAL_BADGE_CLASSES, symbolClass, artifactTabClass whether
//     it is a const arrow function or `function artifactTabClass()`);
// (c) the initializer of any same-file `const` a class expression refers to
//     by name, whatever it is called (LabelsEditor's `dim`, GherkinViewer's
//     block-scoped `color`), followed transitively. A name is looked up in
//     the innermost enclosing scope that binds it; when that binding is not
//     a plain `const` -- a parameter (destructured ones included), a `let` /
//     `var`, a destructuring `const`, a function / class / enum declaration,
//     a loop or catch variable -- it shadows any outer constant and nothing
//     is resolved. A property access with a static key (`M.bg`, `M['bg']`,
//     `stepKeywordColor.Given`) follows only that property's value through
//     object literals; a dynamic key (`STYLE[b]`) takes in the whole object
//     (AutopilotBadges' `STYLE` map), as does a static key the object
//     literal does not plainly list (a spread, say). The key itself is never
//     collected.
// Conditions (a ternary's test, a comparison, `!x`, the left side of `&&`)
// only choose between class lists and are skipped. Function calls are
// opaque: their arguments (an i18n key built for t(), the callback of
// `useMemo(() => ...)`) are neither collected nor resolved, and a called
// function's body is not followed -- a function whose own name ends in class
// is a class expression by (b) instead. Imports, `let` variables and
// parameters are not followed either, so a class string that only arrives
// through them (statusMeta.chip.bg and the other *Meta files, which list
// complete class names statically) is outside these checks.
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

// Every non-test source under src/ plus index.html, read once. `file` is
// relative to packages/web; `text` is the original text (the syntax-tree
// checks below parse it, and comments are not in the tree).
const RAW_SOURCES = [...sourceFiles(SRC), join(WEB_ROOT, 'index.html')].map(path => ({
  path,
  file: relative(WEB_ROOT, path),
  text: readFileSync(path, 'utf8')
}));

// The same sources with comments blanked out of the TypeScript files, for the
// regular-expression scans.
const SOURCES = RAW_SOURCES.map(({ path, text }) => ({
  path,
  text: path.endsWith('.html') ? text : stripComments(text)
}));

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

// Whether a binding name (a plain identifier or a destructuring pattern,
// nested to any depth) binds `name`.
const bindsName = (binding: ts.BindingName, name: string): boolean =>
  ts.isIdentifier(binding)
    ? binding.text === name
    : binding.elements.some(element => !ts.isOmittedExpression(element) && bindsName(element.name, name));

const SHADOWED = 'shadowed';

// What `scope` itself binds under `name`: a plain `const` declaration (which
// can be resolved), SHADOWED for any other kind of binding (a parameter, a
// `let` / `var`, a destructuring `const`, a function / class / enum
// declaration, a loop or catch variable), or undefined when it binds nothing
// by that name.
const bindingIn = (scope: ts.Node, name: string): ts.VariableDeclaration | typeof SHADOWED | undefined => {
  if (ts.isFunctionLike(scope) && scope.parameters.some(p => bindsName(p.name, name))) return SHADOWED;
  if (ts.isCatchClause(scope) && scope.variableDeclaration && bindsName(scope.variableDeclaration.name, name)) {
    return SHADOWED;
  }
  if (
    (ts.isForStatement(scope) || ts.isForOfStatement(scope) || ts.isForInStatement(scope)) &&
    scope.initializer &&
    ts.isVariableDeclarationList(scope.initializer) &&
    scope.initializer.declarations.some(d => bindsName(d.name, name))
  ) {
    return SHADOWED;
  }
  if (!(ts.isBlock(scope) || ts.isSourceFile(scope) || ts.isModuleBlock(scope))) return undefined;
  for (const statement of scope.statements) {
    if (ts.isVariableStatement(statement)) {
      const isConst = (statement.declarationList.flags & ts.NodeFlags.Const) !== 0;
      for (const declaration of statement.declarationList.declarations) {
        if (!bindsName(declaration.name, name)) continue;
        return isConst && ts.isIdentifier(declaration.name) ? declaration : SHADOWED;
      }
    } else if (
      (ts.isFunctionDeclaration(statement) || ts.isClassDeclaration(statement) || ts.isEnumDeclaration(statement)) &&
      statement.name?.text === name
    ) {
      return SHADOWED;
    }
  }
  return undefined;
};

// The same-file `const` an identifier refers to: the innermost enclosing
// scope that binds the name, walking outwards from the identifier. When that
// binding is anything but a plain `const`, it shadows outer constants and
// nothing is resolved. TDZ, `var` hoisting out of nested blocks and other
// finer scoping rules are not modelled.
const resolveConst = (id: ts.Identifier): ts.VariableDeclaration | undefined => {
  for (let scope: ts.Node | undefined = id.parent; scope; scope = scope.parent) {
    const binding = bindingIn(scope, id.text);
    if (binding === SHADOWED) return undefined;
    if (binding) return binding;
  }
  return undefined;
};

// Strips parentheses and type-only wrappers (`as const`, `satisfies`, `!`).
const unwrap = (node: ts.Expression): ts.Expression => {
  let current = node;
  while (
    ts.isParenthesizedExpression(current) ||
    ts.isAsExpression(current) ||
    ts.isSatisfiesExpression(current) ||
    ts.isNonNullExpression(current) ||
    ts.isTypeAssertionExpression(current)
  ) {
    current = current.expression;
  }
  return current;
};

// The text of a name that is statically known (an identifier, a private
// name, a string or numeric literal, a computed name holding a literal).
const staticName = (name: ts.Node): string | undefined => {
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name) || ts.isStringLiteralLike(name) || ts.isNumericLiteral(name)) {
    return name.text;
  }
  if (ts.isComputedPropertyName(name)) return staticName(name.expression);
  return undefined;
};

// The value an object literal gives `key`, or undefined when that is not
// plainly known (the key is missing, or a spread or method may supply it).
// Later properties win, as at run time.
const propertyValue = (object: ts.ObjectLiteralExpression, key: string): ts.Expression | undefined => {
  for (const property of [...object.properties].reverse()) {
    if (ts.isSpreadAssignment(property)) return undefined;
    if (staticName(property.name) !== key) continue;
    if (ts.isPropertyAssignment(property)) return property.initializer;
    if (ts.isShorthandPropertyAssignment(property)) return property.name;
    return undefined;
  }
  return undefined;
};

// Whether an identifier stands for a value that could be resolved, as
// opposed to a name being declared. (Accessed keys never get here: property
// and element accesses are handled as a whole.)
const isValueReference = (id: ts.Identifier) => {
  const parent = id.parent;
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
// following same-file constants (each value reached through them at most
// once).
const collectClassParts = (root: ts.Node) => {
  const pieces: string[] = [];
  const templates: ts.TemplateExpression[] = [];
  const resolved: string[] = [];
  const seen = new Set<ts.Node>();
  // Follows `expression` down the static property `keys` (outermost first),
  // resolving constants on the way, and walks what it reaches. Where the
  // keys cannot be followed (the value is not an object literal, or does not
  // plainly list the key), the whole value there is walked instead.
  const follow = (expression: ts.Expression, keys: string[], via = new Set<ts.Node>()): void => {
    const value = unwrap(expression);
    if (ts.isIdentifier(value)) {
      const declaration = resolveConst(value);
      if (!declaration?.initializer || via.has(declaration)) return;
      via.add(declaration);
      resolved.push(value.text);
      follow(declaration.initializer, keys, via);
      return;
    }
    if (keys.length > 0 && ts.isObjectLiteralExpression(value)) {
      const property = propertyValue(value, keys[0]);
      if (property) {
        follow(property, keys.slice(1), via);
        return;
      }
    }
    if (seen.has(value)) return;
    seen.add(value);
    walk(value);
  };
  const walk = (node: ts.Node): void => {
    if (ts.isTypeNode(node)) return;
    if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
      // The callee is only walked for a property access's object
      // (`obj.method()`); the arguments not at all.
      if (ts.isPropertyAccessExpression(node.expression)) walk(node.expression.expression);
      return;
    }
    if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
      // Gather the static keys of an access chain (`M.a['b']`); a dynamic
      // key (`STYLE[b]`) stops the chain and takes in its whole object.
      const keys: string[] = [];
      let base: ts.Expression = node;
      for (;;) {
        if (ts.isPropertyAccessExpression(base)) {
          keys.unshift(base.name.text);
        } else if (ts.isElementAccessExpression(base) && staticName(unwrap(base.argumentExpression)) !== undefined) {
          keys.unshift(staticName(unwrap(base.argumentExpression))!);
        } else {
          break;
        }
        base = unwrap(base.expression);
      }
      if (ts.isElementAccessExpression(base)) {
        follow(base.expression, []);
        return;
      }
      follow(base, keys);
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
      if (isValueReference(node)) follow(node, []);
      return;
    }
    ts.forEachChild(node, walk);
  };
  seen.add(root);
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
        ts.isPropertyDeclaration(node) ||
        ts.isParameter(node) ||
        ts.isBindingElement(node)) &&
      node.initializer
    ) {
      const name = staticName(node.name);
      if (name !== undefined && CLASS_NAMED.test(name)) add(name, node.initializer);
    } else if (
      (ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node) || ts.isGetAccessorDeclaration(node)) &&
      node.name &&
      node.body
    ) {
      // A class-named function declaration, method or getter: its body
      // (every string it returns, or builds on the way) is the class list.
      const name = staticName(node.name);
      if (name !== undefined && CLASS_NAMED.test(name)) add(name, node.body);
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

const REAL_CLASS_EXPRESSIONS = RAW_SOURCES.filter(({ file }) => !file.endsWith('.html')).flatMap(({ file, text }) =>
  collectClassExpressions(file, text)
);

const INDEX_HTML_CLASSES = RAW_SOURCES.filter(({ file }) => file.endsWith('.html')).flatMap(({ file, text }) =>
  collectHtmlClassExpressions(file, text)
);

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
    ['class-named parameter default', 'function F({ className = `p-1${x}` }) { return className; }', 'p-1${'],
    [
      'class-named function declaration',
      'function tabClass(a: boolean) { return `px-3${a ? " x" : ""}`; } const E = () => <div className={tabClass(true)} />;',
      'px-3${'
    ],
    ['class-named method', 'class K { rowClass() { return `p-2${x}`; } }', 'p-2${'],
    ['class-named getter', 'const o = { get chipClass() { return `ml-1${x}`; } };', 'ml-1${'],
    ['class-named class property', 'class K { cellClass = `pl-4${x}`; }', 'pl-4${'],
    [
      'property followed through a static key',
      "const M = { label: 'Step', bg: `bg-red-100${x}` }; const E = () => <div className={M.bg} />;",
      'bg-red-100${'
    ],
    [
      'nested properties followed through static keys',
      "const M = { a: { bg: `mt-1${x}` } as const }; const E = () => <div className={`p-2 ${M.a['bg']}`} />;",
      'mt-1${'
    ],
    [
      'whole object when the static key is not plainly listed (a spread)',
      "const M = { ...B, label: `mb-1${x}` }; const E = () => <div className={M.bg} />;",
      'mb-1${'
    ],
    [
      'inner constant shadowing an outer one',
      "const x = 'p-1'; function F() { const x = `pr-2${y}`; return <div className={`p-2 ${x}`} />; }",
      'pr-2${'
    ]
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
    ['parameter shadowing a constant', 'const x = `a-${b}`; const E = (x: string) => <div className={`p-2 ${x}`} />;'],
    ['destructured parameter shadowing a constant', 'const x = `a-${b}`; const E = ({ x }: any) => <div className={`p-2 ${x}`} />;'],
    [
      'nested destructured parameter shadowing a constant',
      'const x = `a-${b}`; const E = ({ p: [, { x }] }: any) => <div className={`p-2 ${x}`} />;'
    ],
    ['inner let shadowing a constant', 'const x = `a-${b}`; function F() { let x = "q"; return <div className={`p-2 ${x}`} />; }'],
    ['inner var shadowing a constant', 'const x = `a-${b}`; function F() { var x = "q"; return <div className={`p-2 ${x}`} />; }'],
    [
      'destructuring const shadowing a constant',
      'const x = `a-${b}`; function F(o: any) { const { x } = o; return <div className={`p-2 ${x}`} />; }'
    ],
    [
      'inner function declaration shadowing a constant',
      'const x = `a-${b}`; function F() { function x() { return "q"; } return <div className={`p-2 ${x}`} />; }'
    ],
    ['loop variable shadowing a constant', 'const x = `a-${b}`; for (const x of xs) out.push(<div className={`p-2 ${x}`} />);'],
    [
      'catch variable shadowing a constant',
      'const x = `a-${b}`; try { f(); } catch (x) { out.push(<div className={`p-2 ${x}`} />); }'
    ],
    [
      'other properties of an object accessed by a static key',
      'const M = { label: `Step${n}`, bg: "bg-red-100" }; const E = () => <div className={`${M.bg}`} />;'
    ],
    [
      'other properties of an object accessed by a static element key',
      "const M = { label: `Step${n}`, bg: 'bg-red-100' }; const E = () => <div className={M['bg']} />;"
    ]
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
    [
      'a property read through a static key',
      "const S = { a: 'wrap-anywhere', b: 'p-1' }; const E = () => <div className={`wrap-break-word ${S.a}`} />;"
    ],
    ['exclusive ternary branches (reported conservatively)', "const E = () => <div className={c ? 'wrap-anywhere' : 'wrap-break-word'} />;"]
  ])('reports wrap-break-word combined with wrap-anywhere across %s', (_label, source) => {
    expect(findWrapConflicts('sample.tsx', source)).toHaveLength(1);
  });

  it('reports the combination in an HTML class attribute', () => {
    expect(findWrapConflicts('index.html', '<p class="wrap-anywhere wrap-break-word">')).toEqual(['index.html:1 (class)']);
  });

  it.each([
    ['a variant on one of them', "const E = () => <div className={`wrap-break-word ${c ? 'max-sm:wrap-anywhere' : ''}`} />;"],
    ['separate elements', "const E = () => <><div className=\"wrap-break-word\" /><div className=\"wrap-anywhere\" /></>;"],
    [
      'another property of an object read through a static key',
      "const S = { a: 'wrap-anywhere', b: 'p-1' }; const E = () => <div className={`wrap-break-word ${S.b}`} />;"
    ]
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

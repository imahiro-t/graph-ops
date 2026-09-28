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
//   The reverse, a class name right after the `}` (`${x}p-2`), is not
//   checked: DFLT-00279 ran the installed scanner (tailwindcss,
//   @tailwindcss/oxide and @tailwindcss/oxide-darwin-arm64 4.3.3) on
//   `${x}p-2`, `${x}inset-shadow-sm`, `${x}bg-red-100 p-1`, `a ${b}p-2 c`,
//   and `${x}` followed by -mt-1, [mask-type:alpha], hover:bg-red-100,
//   @md:p-2, *:p-2, 2xl:p-2, !p-2 or p-2!; every one of those classes came
//   out as a candidate (and p-2 / inset-shadow-sm got their CSS rules).
//   The only one missing was `${x}p-2${y}`, whose p-2 is also glued to the
//   next `${` and is reported by this check. A later Tailwind version could
//   change this, so re-check it when upgrading.
// - wrap-break-word and wrap-anywhere are never applied together by one
//   class expression, even when they come from different literals (a
//   template and a ternary inside it, a `+` concatenation, a constant it
//   refers to, a class helper's arguments). Only combinations that can apply
//   at the same time are reported: the branches of a ternary, and the two
//   sides of `||` / `??`, exclusive; `c && x` is x or nothing. Everything
//   else counts as applied together, so these are still reported
//   (conservatively) even when only one part is used at run time: the
//   properties of an object taken in whole (`STYLE[b]`, say), the strings of
//   a class-named function's body (every return included), and the keys of
//   a class helper's object argument (whose conditions are not analysed:
//   `{ 'wrap-anywhere': c, 'wrap-break-word': !c }`). Split such a case into
//   separate class expressions.
//
// A class expression is:
// (a) a className / class / *ClassName JSX attribute;
// (b) the initializer of a variable, property (object or class) or parameter
//     default (a destructured one included), or the body of a function
//     declaration, method or getter, whose name ends in class / classes /
//     classname / classnames, any case (inputClass, ERROR_BOX_CLASS,
//     PENDING_APPROVAL_BADGE_CLASSES, symbolClass, artifactTabClass whether
//     it is a const arrow function or `function artifactTabClass()`);
// (c) the initializer of any `const` a class expression refers to by name,
//     whatever it is called (LabelsEditor's `dim`, GherkinViewer's
//     block-scoped `color`), followed transitively. A name is looked up in
//     the innermost enclosing scope that binds it; when that binding is not
//     a plain `const` -- a parameter (destructured ones included), a `let` /
//     `var`, a destructuring `const`, a function / class / enum declaration,
//     a loop or catch variable, an import that is not resolved (below) -- it
//     shadows any outer constant and nothing is resolved. A named import
//     (`import { X } from './x'`, `import { X as Y } from '../x/index'`) is
//     resolved when its path is relative and stays inside src/ (tried as
//     given, then with .ts, .tsx, /index.ts, /index.tsx), and the imported
//     file's top level has `export const X = ...` with X a plain identifier,
//     or `export { local as X }` / `export { X }` of such a plain `const`;
//     the constant is then followed in its own file, as in that file.
//     Nothing else is: a type-only, default or namespace import, a package
//     or out-of-src/ path, an exported `let` / function / class, a
//     re-export (`export { X } from`, `export *`) or an import re-exported
//     as it is. A template reached from several class expressions (of one
//     file or of files importing it) is reported once, at its own file and
//     line. A property access with a static key (`M.bg`, or a string
//     / numeric literal such as `M['bg']` or `M[0]`; also
//     `stepKeywordColor.Given`) follows only that property's value through
//     object literals; a dynamic key (`STYLE[b]` -- an identifier or any
//     other non-literal) takes in the whole object (AutopilotBadges' `STYLE`
//     map), as does a static key the object literal does not plainly list
//     (a spread or a dynamic computed key such as `[k]`, say). The key
//     itself is never collected.
// (d) a call of a class helper -- clsx, cn, cx, classnames, classNames,
//     twMerge or twJoin, called by that name (a helper imported under
//     another name is not recognised) -- that no class expression of (a) -
//     (c) takes in (nor an enclosing helper call): one outside every class
//     expression, or one inside a class expression but behind an opaque
//     call (`const buttonClass = useMemo(() => clsx(...))`,
//     `className={items.map(i => cn(...))}`, `className={foo(cn(...))}`),
//     whose arguments that expression does not walk. A helper call a class
//     expression of (a) - (c) does take in (`className={cn(...)}`, or a
//     constant it refers to) is part of it and is not a class expression of
//     its own. A helper call that a (d) helper call takes in through a
//     constant can still be one, if it comes earlier in the file: in
//     `const w = clsx(...); clsx(w, 'a');` both calls are class expressions,
//     and `clsx(...)` is checked on its own and again as part of
//     `clsx(w, 'a')`.
//     This raises no false report -- a template is reported once, and wrap
//     classes applied together are judged within each class expression. Its
//     arguments are class lists wherever the call is (in a className, a
//     constant, ...): strings, templates, arrays, the branches of ternaries /
//     `&&` / `||` / `??`, and for an object argument
//     (`{ 'p-2': c, [`m-${x}`]: d }`) the keys -- a dynamic computed key is
//     walked as an expression, a spread is walked as another argument -- but
//     not the values, which are conditions.
// Conditions (a ternary's test, a comparison, `!x`, the left side of `&&`)
// only choose between class lists and are skipped. Any other function call
// is opaque: its arguments (an i18n key built for t(), the callback of
// `useMemo(() => ...)`) are neither collected nor resolved (a class helper
// call among them is a class expression of its own, by (d)), and a called
// function's body is not followed -- a function whose own name ends in class
// is a class expression by (b) instead. `let` variables and parameters
// (function arguments, callback parameters) are not followed either: their
// values are only known at run time. So a class string that only arrives
// through them or through a non-helper call's result (statusMeta.chip.bg
// and the other *Meta files, which list complete class names statically)
// is outside these checks.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, posix, relative, sep } from 'node:path';
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
  file: relative(WEB_ROOT, path).split(sep).join('/'),
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

// Class helpers: every argument of a call to one of these is a class list
// (an object argument's keys, not its values).
const CLASS_HELPERS = new Set(['clsx', 'cn', 'cx', 'classnames', 'classNames', 'twMerge', 'twJoin']);

const helperName = (node: ts.Node) =>
  ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CLASS_HELPERS.has(node.expression.text)
    ? node.expression.text
    : undefined;

// Which class strings a class expression applies together. A leaf is a
// literal's text; `all` applies all of its children at once, `oneOf` one of
// them (a ternary's branches, the sides of `||` / `??`, `c && x` as x or
// nothing). The same subtree object can appear in several places (a
// constant referred to twice).
type ClassTree = string | { oneOf: boolean; children: ClassTree[] };

const all = (children: ClassTree[]): ClassTree => ({ oneOf: false, children });
const oneOf = (children: ClassTree[]): ClassTree => ({ oneOf: true, children });
const EMPTY: ClassTree = all([]);

type ClassExpression = {
  // Path relative to packages/web, and the 1-based line of the expression.
  file: string;
  line: number;
  // The attribute / variable / property name that made it a class
  // expression, or `clsx()` etc. for a class helper's call.
  origin: string;
  // Every string piece collected from it (literals, template heads and
  // spans, helper object keys), constants it refers to included.
  pieces: string[];
  // Template literals in it, constants it refers to included.
  templates: ts.TemplateExpression[];
  // Constants it took in, by the name it referred to them (each name once).
  resolved: string[];
  // Those of them that came from another file through an import.
  imported: string[];
  tree: ClassTree;
};

const parseSource = (fileName: string, text: string) =>
  ts.createSourceFile(
    fileName,
    text,
    ts.ScriptTarget.Latest,
    true,
    fileName.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS
  );

// The files the checks look at, by path relative to packages/web
// (`src/...`, with `/`), each TypeScript file parsed at most once so that a
// template reached from several files is the same node everywhere.
type SourceSet = {
  files: string[];
  text: (file: string) => string | undefined;
  parse: (file: string) => ts.SourceFile | undefined;
};

const createSourceSet = (sources: { file: string; text: string }[]): SourceSet => {
  const texts = new Map(sources.map(({ file, text }) => [file, text]));
  const parsed = new Map<string, ts.SourceFile>();
  return {
    files: [...texts.keys()],
    text: file => texts.get(file),
    parse: file => {
      const text = texts.get(file);
      if (text === undefined || !/\.(ts|tsx)$/.test(file)) return undefined;
      let sourceFile = parsed.get(file);
      if (!sourceFile) {
        sourceFile = parseSource(file, text);
        parsed.set(file, sourceFile);
      }
      return sourceFile;
    }
  };
};

const lineOf = (sourceFile: ts.SourceFile, node: ts.Node) =>
  sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;

// Whether a binding name (a plain identifier or a destructuring pattern,
// nested to any depth) binds `name`.
const bindsName = (binding: ts.BindingName, name: string): boolean =>
  ts.isIdentifier(binding)
    ? binding.text === name
    : binding.elements.some(element => !ts.isOmittedExpression(element) && bindsName(element.name, name));

const isExported = (statement: ts.Statement) =>
  ts.canHaveModifiers(statement) && (ts.getModifiers(statement) ?? []).some(m => m.kind === ts.SyntaxKind.ExportKeyword);

// The top-level plain `const` a file declares under `name` (only an
// exported one when `exportedOnly`), or undefined when the name is not
// declared that way.
const topLevelConst = (sourceFile: ts.SourceFile, name: string, exportedOnly: boolean) => {
  for (const statement of sourceFile.statements) {
    if (!ts.isVariableStatement(statement) || (exportedOnly && !isExported(statement))) continue;
    for (const declaration of statement.declarationList.declarations) {
      if (!bindsName(declaration.name, name)) continue;
      const isConst = (statement.declarationList.flags & ts.NodeFlags.Const) !== 0;
      return isConst && ts.isIdentifier(declaration.name) ? declaration : undefined;
    }
  }
  return undefined;
};

// The plain `const` a file exports as `name`: `export const name = ...`, or
// `export { local as name }` / `export { name }` of a top-level plain
// `const`. Re-exports (`export { name } from`, `export *`) are not followed.
const exportedConst = (sourceFile: ts.SourceFile, name: string) => {
  const direct = topLevelConst(sourceFile, name, true);
  if (direct) return direct;
  for (const statement of sourceFile.statements) {
    if (
      !ts.isExportDeclaration(statement) ||
      statement.moduleSpecifier ||
      statement.isTypeOnly ||
      !statement.exportClause ||
      !ts.isNamedExports(statement.exportClause)
    ) {
      continue;
    }
    const element = statement.exportClause.elements.find(e => e.name.text === name && !e.isTypeOnly);
    if (element) return topLevelConst(sourceFile, (element.propertyName ?? element.name).text, false);
  }
  return undefined;
};

// The file a relative import specifier names, when it is in the source set
// and inside src/.
const IMPORT_SUFFIXES = ['', '.ts', '.tsx', '/index.ts', '/index.tsx'];
const importedFile = (fromFile: string, specifier: string, sources: SourceSet) => {
  if (!specifier.startsWith('.')) return undefined;
  const target = posix.normalize(posix.join(posix.dirname(fromFile), specifier));
  if (!target.startsWith('src/')) return undefined;
  return IMPORT_SUFFIXES.map(suffix => sources.parse(target + suffix)).find(Boolean);
};

const SHADOWED = 'shadowed';

// What an import declaration binds under `name`: the imported file's
// exported plain `const` for a resolvable named import, SHADOWED for any
// other import of that name, or undefined when it does not bind the name.
const importBinding = (
  statement: ts.ImportDeclaration,
  name: string,
  sources: SourceSet | undefined
): ts.VariableDeclaration | typeof SHADOWED | undefined => {
  const clause = statement.importClause;
  if (!clause) return undefined;
  if (clause.name?.text === name) return SHADOWED;
  const bindings = clause.namedBindings;
  if (!bindings) return undefined;
  if (ts.isNamespaceImport(bindings)) return bindings.name.text === name ? SHADOWED : undefined;
  const element = bindings.elements.find(e => e.name.text === name);
  if (!element) return undefined;
  if (clause.isTypeOnly || element.isTypeOnly || !sources || !ts.isStringLiteral(statement.moduleSpecifier)) return SHADOWED;
  const target = importedFile(statement.getSourceFile().fileName, statement.moduleSpecifier.text, sources);
  return (target && exportedConst(target, (element.propertyName ?? element.name).text)) || SHADOWED;
};

// What `scope` itself binds under `name`: a plain `const` declaration (which
// can be resolved; for an import, the imported file's), SHADOWED for any
// other kind of binding (a parameter, a `let` / `var`, a destructuring
// `const`, a function / class / enum declaration, a loop or catch variable,
// an import that is not resolved), or undefined when it binds nothing by
// that name.
const bindingIn = (
  scope: ts.Node,
  name: string,
  sources: SourceSet | undefined
): ts.VariableDeclaration | typeof SHADOWED | undefined => {
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
    } else if (ts.isImportDeclaration(statement)) {
      const binding = importBinding(statement, name, sources);
      if (binding) return binding;
    }
  }
  return undefined;
};

// The `const` an identifier refers to: the innermost enclosing scope that
// binds the name, walking outwards from the identifier (and, for a resolved
// import, into the imported file). When that binding is anything but a
// plain `const`, it shadows outer constants and nothing is resolved. TDZ,
// `var` hoisting out of nested blocks and other finer scoping rules are not
// modelled.
const resolveConst = (id: ts.Identifier, sources: SourceSet | undefined): ts.VariableDeclaration | undefined => {
  for (let scope: ts.Node | undefined = id.parent; scope; scope = scope.parent) {
    const binding = bindingIn(scope, id.text, sources);
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

// The text of a key expression that is statically known: a string or
// numeric literal (a no-substitution template included). An identifier or
// any other expression (`STYLE[b]`, `{ [k]: ... }`) is dynamic.
const literalKey = (expression: ts.Expression): string | undefined => {
  const key = unwrap(expression);
  return ts.isStringLiteralLike(key) || ts.isNumericLiteral(key) ? key.text : undefined;
};

// The text of a declared name that is statically known (an identifier, a
// private name, a string or numeric literal, a computed name holding a
// literal).
const staticName = (name: ts.Node): string | undefined => {
  if (ts.isIdentifier(name) || ts.isPrivateIdentifier(name) || ts.isStringLiteralLike(name) || ts.isNumericLiteral(name)) {
    return name.text;
  }
  if (ts.isComputedPropertyName(name)) return literalKey(name.expression);
  return undefined;
};

// The value an object literal gives `key`, or undefined when that is not
// plainly known (the key is missing, or a spread, a dynamic computed key or
// a method may supply it). Later properties win, as at run time.
const propertyValue = (object: ts.ObjectLiteralExpression, key: string): ts.Expression | undefined => {
  for (const property of [...object.properties].reverse()) {
    if (ts.isSpreadAssignment(property)) return undefined;
    const name = staticName(property.name);
    if (name === undefined) return undefined;
    if (name !== key) continue;
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

// Collects the string pieces, templates and class tree under a class
// expression, following constants (same-file ones, and imported ones when
// `sources` is given). Each value reached through a constant is walked once
// (per context: inside a class helper's arguments or not) and its subtree
// reused wherever it is reached again; only a cycle back into a value still
// being walked yields nothing. Every class helper call the walk takes in is
// added to `reachedHelpers`, when given.
const collectClassParts = (root: ts.Node, sources?: SourceSet, reachedHelpers?: Set<ts.Node>) => {
  const pieces: string[] = [];
  const templates: ts.TemplateExpression[] = [];
  const resolved: string[] = [];
  const imported: string[] = [];
  const built = [new Map<ts.Node, ClassTree>(), new Map<ts.Node, ClassTree>()];
  // Follows `expression` down the static property `keys` (outermost first),
  // resolving constants on the way, and walks what it reaches. Where the
  // keys cannot be followed (the value is not an object literal, or does not
  // plainly list the key), the whole value there is walked instead -- for
  // its values, even inside a helper's arguments, since what is used is one
  // of them and not the object itself.
  const follow = (expression: ts.Expression, keys: string[], inHelper: boolean, via = new Set<ts.Node>()): ClassTree => {
    const value = unwrap(expression);
    if (ts.isIdentifier(value)) {
      const declaration = resolveConst(value, sources);
      if (!declaration?.initializer || via.has(declaration)) return EMPTY;
      via.add(declaration);
      if (!resolved.includes(value.text)) resolved.push(value.text);
      if (declaration.getSourceFile() !== value.getSourceFile() && !imported.includes(value.text)) imported.push(value.text);
      return follow(declaration.initializer, keys, inHelper, via);
    }
    if (keys.length > 0 && ts.isObjectLiteralExpression(value)) {
      const property = propertyValue(value, keys[0]);
      if (property) return follow(property, keys.slice(1), inHelper, via);
    }
    const context = inHelper && keys.length === 0;
    const memo = built[context ? 1 : 0];
    const known = memo.get(value);
    if (known) return known;
    memo.set(value, EMPTY);
    const tree = walk(value, context);
    memo.set(value, tree);
    return tree;
  };
  const walk = (node: ts.Node, inHelper: boolean): ClassTree => {
    if (ts.isTypeNode(node)) return EMPTY;
    if (helperName(node)) {
      reachedHelpers?.add(node);
      return all((node as ts.CallExpression).arguments.map(arg => walk(arg, true)));
    }
    if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
      // Any other call: the callee is only walked for a property access's
      // object (`obj.method()`); the arguments not at all.
      return ts.isPropertyAccessExpression(node.expression) ? walk(node.expression.expression, false) : EMPTY;
    }
    if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
      // Gather the static keys of an access chain (`M.a['b']`, `M[0]`); a
      // dynamic key (`STYLE[b]`, any non-literal) stops the chain and takes
      // in its whole object.
      const keys: string[] = [];
      let base: ts.Expression = node;
      for (;;) {
        if (ts.isPropertyAccessExpression(base)) {
          keys.unshift(base.name.text);
        } else if (ts.isElementAccessExpression(base) && literalKey(base.argumentExpression) !== undefined) {
          keys.unshift(literalKey(base.argumentExpression)!);
        } else {
          break;
        }
        base = unwrap(base.expression);
      }
      if (ts.isElementAccessExpression(base)) return follow(base.expression, [], false);
      return follow(base, keys, inHelper);
    }
    // Conditions only choose between class lists: a ternary's test, a
    // comparison, a negation and the left side of && contribute no classes.
    if (ts.isConditionalExpression(node)) return oneOf([walk(node.whenTrue, inHelper), walk(node.whenFalse, inHelper)]);
    if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) return EMPTY;
    if (ts.isBinaryExpression(node)) {
      switch (node.operatorToken.kind) {
        case ts.SyntaxKind.AmpersandAmpersandToken:
          return oneOf([walk(node.right, inHelper), EMPTY]);
        case ts.SyntaxKind.BarBarToken:
        case ts.SyntaxKind.QuestionQuestionToken:
          return oneOf([walk(node.left, inHelper), walk(node.right, inHelper)]);
        case ts.SyntaxKind.PlusToken:
          return all([walk(node.left, inHelper), walk(node.right, inHelper)]);
        default:
          return EMPTY;
      }
    }
    if (inHelper && ts.isObjectLiteralExpression(node)) {
      // A helper's object argument: its keys are the classes, its values
      // the conditions that pick them.
      return all(
        node.properties.map(property => {
          if (ts.isSpreadAssignment(property)) return walk(property.expression, true);
          const name = staticName(property.name);
          if (name !== undefined) {
            pieces.push(name);
            return name;
          }
          return ts.isComputedPropertyName(property.name) ? walk(property.name.expression, false) : EMPTY;
        })
      );
    }
    if (ts.isPropertyAssignment(node)) return walk(node.initializer, inHelper);
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
      pieces.push(node.text);
      return node.text;
    }
    if (ts.isTemplateExpression(node)) {
      templates.push(node);
      pieces.push(node.head.text);
      const parts: ClassTree[] = [node.head.text];
      for (const span of node.templateSpans) {
        parts.push(walk(span.expression, false));
        pieces.push(span.literal.text);
        parts.push(span.literal.text);
      }
      return all(parts);
    }
    if (ts.isIdentifier(node)) return isValueReference(node) ? follow(node, [], inHelper) : EMPTY;
    // Anything else (an array, a spread, a function body with its return
    // statements, ...) applies all of its parts.
    const children: ClassTree[] = [];
    ts.forEachChild(node, child => {
      children.push(walk(child, inHelper));
    });
    return all(children);
  };
  built[0].set(root, EMPTY);
  const tree = walk(root, false);
  return { pieces, templates, resolved, imported, tree };
};

const collectClassExpressions = (fileName: string, sources: SourceSet): ClassExpression[] => {
  const sourceFile = sources.parse(fileName);
  if (!sourceFile) return [];
  const found: (ClassExpression & { position: number })[] = [];
  // The class helper calls the class expressions found so far take in: those
  // are part of an expression already, not expressions of their own.
  const reachedHelpers = new Set<ts.Node>();
  const add = (origin: string, node: ts.Node) =>
    found.push({
      file: fileName,
      line: lineOf(sourceFile, node),
      origin,
      position: node.getStart(sourceFile),
      ...collectClassParts(node, sources, reachedHelpers)
    });
  // First the named class expressions: attributes, class-named variables,
  // properties, parameters and functions.
  const visitNamed = (node: ts.Node): void => {
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
    ts.forEachChild(node, visitNamed);
  };
  // Then every class helper call none of them took in -- one outside any
  // class expression, or inside one but behind something its walk does not
  // enter (a non-helper call's arguments: `useMemo(() => cn(...))`,
  // `items.map(i => clsx(...))`, `foo(cn(...))`). Outer calls come first, so
  // a helper call nested in another one is taken in by it.
  const visitHelpers = (node: ts.Node): void => {
    const name = helperName(node);
    if (name && !reachedHelpers.has(node)) add(`${name}()`, node);
    ts.forEachChild(node, visitHelpers);
  };
  visitNamed(sourceFile);
  visitHelpers(sourceFile);
  return found.sort((a, b) => a.position - b.position).map(({ position, ...expression }) => expression);
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

// Every place in the class expressions of a source set where a class name
// runs straight into `${`, as "file:line: <class>${" at the template's own
// file and line. A template reached from several class expressions (of one
// file or of several) is reported once.
const findGluedInSources = (sources: SourceSet): string[] => {
  const offenders: string[] = [];
  const reported = new Set<ts.Node>();
  for (const fileName of sources.files) {
    if (fileName.endsWith('.html')) {
      for (const { file, line, value } of collectHtmlClassExpressions(fileName, sources.text(fileName)!)) {
        for (const m of value.matchAll(GLUED_IN_TEXT)) offenders.push(`${file}:${line}: ${gluedSnippet(value.slice(0, m.index + 1))}`);
      }
      continue;
    }
    for (const { templates } of collectClassExpressions(fileName, sources)) {
      for (const template of templates) {
        if (reported.has(template)) continue;
        // The static text before each `${`: the head, and every span's
        // literal but the last (which closes the template).
        const beforeSubstitutions = [template.head, ...template.templateSpans.slice(0, -1).map(s => s.literal)];
        const glued = beforeSubstitutions.filter(part => GLUED_BEFORE.test(part.text));
        if (glued.length === 0) continue;
        reported.add(template);
        const sourceFile = template.getSourceFile();
        for (const part of glued) offenders.push(`${sourceFile.fileName}:${lineOf(sourceFile, part)}: ${gluedSnippet(part.text)}`);
      }
    }
  }
  return offenders;
};

const findGluedTemplateClasses = (fileName: string, text: string) =>
  findGluedInSources(createSourceSet([{ file: fileName, text }]));

const classTokens = (pieces: string[]) => new Set(pieces.flatMap(piece => piece.split(/\s+/)).filter(Boolean));

const hasWrapConflict = (tokens: Set<string>) => tokens.has('wrap-break-word') && tokens.has('wrap-anywhere');

// The combinations of wrap-break-word (1) and wrap-anywhere (2) a class tree
// can apply at the same time, as bit masks.
const WRAP_BITS: Record<string, number> = { 'wrap-break-word': 1, 'wrap-anywhere': 2 };
const wrapCombinations = (tree: ClassTree, memo = new Map<ClassTree, Set<number>>()): Set<number> => {
  if (typeof tree === 'string') {
    return new Set([[...classTokens([tree])].reduce((mask, token) => mask | (WRAP_BITS[token] ?? 0), 0)]);
  }
  const known = memo.get(tree);
  if (known) return known;
  let result: Set<number>;
  if (tree.oneOf) {
    result = new Set(tree.children.flatMap(child => [...wrapCombinations(child, memo)]));
  } else {
    result = new Set([0]);
    for (const child of tree.children) {
      const childCombinations = wrapCombinations(child, memo);
      result = new Set([...result].flatMap(a => [...childCombinations].map(b => a | b)));
    }
  }
  memo.set(tree, result);
  return result;
};

const mayApplyBothWraps = (tree: ClassTree) => wrapCombinations(tree).has(3);

// Every class expression of a source set that can apply plain
// wrap-break-word and wrap-anywhere at the same time, as "file:line (origin)".
const findWrapConflictsInSources = (sources: SourceSet): string[] =>
  sources.files.flatMap(fileName =>
    fileName.endsWith('.html')
      ? collectHtmlClassExpressions(fileName, sources.text(fileName)!)
          .filter(({ value }) => hasWrapConflict(classTokens([value])))
          .map(({ file, line }) => `${file}:${line} (class)`)
      : collectClassExpressions(fileName, sources)
          .filter(({ tree }) => mayApplyBothWraps(tree))
          .map(({ file, line, origin }) => `${file}:${line} (${origin})`)
  );

const findWrapConflicts = (fileName: string, text: string) => findWrapConflictsInSources(createSourceSet([{ file: fileName, text }]));

const REAL_SOURCE_SET = createSourceSet(RAW_SOURCES);

const REAL_CLASS_EXPRESSIONS = REAL_SOURCE_SET.files.flatMap(file => collectClassExpressions(file, REAL_SOURCE_SET));

const INDEX_HTML_CLASSES = RAW_SOURCES.filter(({ file }) => file.endsWith('.html')).flatMap(({ file, text }) =>
  collectHtmlClassExpressions(file, text)
);

// The class expressions of one real file whose tokens include all of `tokens`.
const realExpressionsWith = (file: string, tokens: string[]) =>
  REAL_CLASS_EXPRESSIONS.filter(e => e.file === file && tokens.every(t => classTokens(e.pieces).has(t)));

// A source set of synthetic files, for the tests of imports.
const sampleSources = (files: Record<string, string>) =>
  createSourceSet(Object.entries(files).map(([file, text]) => ({ file, text })));

describe('Tailwind v4 scanner: class names glued to ${', () => {
  it('finds no class name glued to ${ in src/ and index.html', () => {
    const offenders = findGluedInSources(REAL_SOURCE_SET);
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
      'whole object through an identifier key, even one named like a listed key',
      "const M = { k: 'p-1', a: `bg-red-100${x}` }; const E = () => <div className={M[k]} />;",
      'bg-red-100${'
    ],
    [
      'inner constant shadowing an outer one',
      "const x = 'p-1'; function F() { const x = `pr-2${y}`; return <div className={`p-2 ${x}`} />; }",
      'pr-2${'
    ],
    ['clsx argument', 'const E = () => <div className={clsx(`p-2${x}`)} />;', 'p-2${'],
    ['cn argument behind &&', "const E = () => <div className={cn('a', c && `mt-1${d}`)} />;", 'mt-1${'],
    ['cx object key (dynamic computed key)', 'const E = () => <div className={cx({ [`px-2${x}`]: c })} />;', 'px-2${'],
    ['twMerge array element', "const E = () => <div className={twMerge(['p-1', `pl-2${x}`])} />;", 'pl-2${'],
    ['twJoin ternary branch', "const E = () => <div className={twJoin('a', c ? `ml-2${x}` : '')} />;", 'ml-2${'],
    ['classNames call outside any class expression', 'const v = classNames(`m-1${x}`);', 'm-1${'],
    ['classnames argument through a constant', 'const A = `pt-1${x}`; const v = classnames(A, "b");', 'pt-1${'],
    [
      "helper object spread's dynamic key",
      'const O = { [`pb-3${x}`]: true }; const E = () => <div className={clsx({ ...O })} />;',
      'pb-3${'
    ],
    [
      'class name glued on both sides (the ${ after it drops it)',
      'const E = () => <div className={`${x}p-2${y}`} />;',
      'p-2${'
    ],
    [
      "helper call in a class-named constant's useMemo callback",
      'const buttonClass = useMemo(() => clsx(`p-2${x}`), [x]);',
      'p-2${'
    ],
    [
      "helper call in a className's .map callback",
      'const E = () => <div className={items.map(i => clsx(`pl-1${i}`)).join(" ")} />;',
      'pl-1${'
    ],
    ["helper call as a non-helper call's argument in a className", 'const E = () => <div className={foo(cn(`mt-2${x}`))} />;', 'mt-2${'],
    [
      'helper call nested in another one behind a non-helper call',
      "const E = () => <div className={foo(clsx('a', cn(`mb-2${x}`)))} />;",
      'mb-2${'
    ],
    [
      'helper call in a useMemo callback inside a className',
      'const E = () => <div className={useMemo(() => twMerge(`pr-1${x}`), [x])} />;',
      'pr-1${'
    ]
  ])('reports a class name glued to ${ (%s)', (_label, source, snippet) => {
    const offenders = findGluedTemplateClasses('sample.tsx', source);
    expect(offenders).toHaveLength(1);
    expect(offenders[0]).toContain(snippet);
  });

  it('reports a helper call behind a non-helper call at its own line, once', () => {
    expect(
      findGluedTemplateClasses(
        'sample.tsx',
        'const E = () => (\n  <div\n    className={items.map(i =>\n      cn(`p-2${i}`)\n    ).join(" ")}\n  />\n);'
      )
    ).toEqual(['sample.tsx:4: p-2${']);
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
    ],
    ["a class helper object's value (a condition)", 'const E = () => <div className={clsx({ "p-1": `k${x}` })} />;'],
    ['a non-helper call of a class-helper-like name', 'const E = () => <div className={`p-2 ${classList(`k${x}`)}`} />;']
  ])('does not report non-class template strings (%s)', (_label, source) => {
    expect(findGluedTemplateClasses('sample.tsx', source)).toEqual([]);
  });

  // The Oxide scanner takes a class right after a `}` as a candidate (see
  // the header), so these are left alone.
  it.each([
    ['at the start', 'const E = () => <div className={`${x}p-2`} />;'],
    ['in the middle', 'const E = () => <div className={`a ${b}p-2 c`} />;'],
    ['a negative value', 'const E = () => <div className={`${x}-mt-1`} />;'],
    ['an arbitrary property', 'const E = () => <div className={`${x}[mask-type:alpha]`} />;'],
    ['a variant', 'const E = () => <div className={`${x}hover:bg-red-100 p-1`} />;']
  ])('does not report a class name right after } (%s)', (_label, source) => {
    expect(findGluedTemplateClasses('sample.tsx', source)).toEqual([]);
  });

  describe('through imports', () => {
    const IMPORTER = (specifier: string, name = 'BASE', use = name) =>
      `import { ${name} } from '${specifier}';\nconst E = () => <div className={\`\${${use}} p-1\`} />;`;

    it.each([
      [
        'a named import',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'export const BASE = `px-2${x}`;' },
        ['src/b.ts:1: px-2${']
      ],
      [
        'an import with an explicit extension',
        { 'src/a.tsx': IMPORTER('./b.ts'), 'src/b.ts': 'export const BASE = `px-2${x}`;' },
        ['src/b.ts:1: px-2${']
      ],
      [
        'an aliased import',
        { 'src/a.tsx': IMPORTER('./b', 'BASE as B', 'B'), 'src/b.ts': 'export const BASE = `px-2${x}`;' },
        ['src/b.ts:1: px-2${']
      ],
      [
        'a ../ path to an index file',
        {
          'src/components/a.tsx': IMPORTER('../styles'),
          'src/styles/index.ts': '// shared classes\nexport const BASE = `mx-1${x}`;'
        },
        ['src/styles/index.ts:2: mx-1${']
      ],
      [
        'a .tsx file',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.tsx': 'export const BASE = `my-1${x}`;' },
        ['src/b.tsx:1: my-1${']
      ],
      [
        'export { local as X }',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'const local = `mt-1${x}`;\nexport { local as BASE };' },
        ['src/b.ts:1: mt-1${']
      ],
      [
        'export { X } of a plain const',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'const BASE = `mb-1${x}`;\nexport { BASE };' },
        ['src/b.ts:1: mb-1${']
      ],
      [
        "a constant the imported one refers to in its own file",
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'const INNER = `pl-2${x}`;\nexport const BASE = `${INNER} p-1`;' },
        ['src/b.ts:1: pl-2${']
      ],
      [
        'a chain of imports',
        {
          'src/a.tsx': IMPORTER('./b'),
          'src/b.ts': "import { INNER } from './c';\nexport const BASE = `${INNER} p-1`;",
          'src/c.ts': 'export const INNER = `pr-2${x}`;'
        },
        ['src/c.ts:1: pr-2${']
      ],
      [
        'a property through a static key, and not the others',
        {
          'src/a.tsx': "import { M } from './b';\nconst E = () => <div className={M.bg} />;",
          'src/b.ts': 'export const M = {\n  label: `Step${n}`,\n  bg: `bg-red-100${x}`\n};'
        },
        ['src/b.ts:3: bg-red-100${']
      ],
      [
        'the same template imported by two files (reported once)',
        {
          'src/a.tsx': IMPORTER('./b'),
          'src/c.tsx': IMPORTER('./b'),
          'src/b.ts': 'export const BASE = `px-2${x}`;'
        },
        ['src/b.ts:1: px-2${']
      ],
      [
        'a template both class-named in its file and imported (reported once)',
        { 'src/a.tsx': IMPORTER('./b', 'BASE_CLASS'), 'src/b.ts': 'export const BASE_CLASS = `px-2${x}`;' },
        ['src/b.ts:1: px-2${']
      ]
    ])('reports a template reached through %s', (_label, files, expected) => {
      expect(findGluedInSources(sampleSources(files))).toEqual(expected);
    });

    const GLUED = 'export const BASE = `px-2${x}`;';
    it.each([
      ['a package import', { 'src/a.tsx': IMPORTER('b'), 'src/b.ts': GLUED }],
      ['a src/-rooted non-relative import', { 'src/a.tsx': IMPORTER('src/b'), 'src/b.ts': GLUED }],
      ['a path out of src/', { 'src/a.tsx': IMPORTER('../b'), 'b.ts': GLUED }],
      ['a file not in the set', { 'src/a.tsx': IMPORTER('./missing'), 'src/b.ts': GLUED }],
      ['an exported let', { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'export let BASE = `px-2${x}`;' }],
      ['an exported function', { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'export function BASE() { return `px-2${x}`; }' }],
      [
        'an exported destructuring const',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'export const { BASE } = { BASE: `px-2${x}` };' }
      ],
      ['a const that is not exported', { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': 'const BASE = `px-2${x}`;' }],
      [
        'a re-export',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': "export { BASE } from './c';", 'src/c.ts': GLUED }
      ],
      ['export *', { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': "export * from './c';", 'src/c.ts': GLUED }],
      [
        'an import exported again as it is',
        { 'src/a.tsx': IMPORTER('./b'), 'src/b.ts': "import { BASE } from './c';\nexport { BASE };", 'src/c.ts': GLUED }
      ],
      ['a type-only import', { 'src/a.tsx': IMPORTER('./b', 'type BASE'), 'src/b.ts': GLUED }],
      [
        'an import type declaration',
        {
          'src/a.tsx': "import type { BASE } from './b';\nconst E = () => <div className={`${BASE} p-1`} />;",
          'src/b.ts': GLUED
        }
      ],
      [
        'a default import',
        {
          'src/a.tsx': "import BASE from './b';\nconst E = () => <div className={`${BASE} p-1`} />;",
          'src/b.ts': 'const BASE = `px-2${x}`;\nexport default BASE;'
        }
      ],
      [
        'a namespace import',
        {
          'src/a.tsx': "import * as S from './b';\nconst E = () => <div className={`${S.BASE} p-1`} />;",
          'src/b.ts': GLUED
        }
      ],
      [
        'an import shadowed by an inner constant',
        {
          'src/a.tsx':
            "import { BASE } from './b';\nfunction F() { const BASE = 'p-2'; return <div className={`${BASE} p-1`} />; }",
          'src/b.ts': GLUED
        }
      ]
    ])('does not report a template behind %s', (_label, files) => {
      expect(findGluedInSources(sampleSources(files))).toEqual([]);
    });

    it('records which constants came through an import', () => {
      const sources = sampleSources({ 'src/a.tsx': IMPORTER('./b', 'BASE as B', 'B'), 'src/b.ts': "export const BASE = 'px-2';" });
      const [expression] = collectClassExpressions('src/a.tsx', sources);
      expect(expression.imported).toEqual(['B']);
      expect(classTokens(expression.pieces)).toEqual(new Set(['px-2', 'p-1']));
    });

    it('records a constant referred to more than once under each name once', () => {
      const sources = sampleSources({
        'src/a.tsx':
          "import { BASE } from './b';\nconst W = 'p-1';\nconst E = () => <div className={c ? `${BASE} ${W}` : `${BASE} ${W} m-1`} />;",
        'src/b.ts': "export const BASE = 'px-2';"
      });
      const [expression] = collectClassExpressions('src/a.tsx', sources);
      expect(expression.resolved).toEqual(['BASE', 'W']);
      expect(expression.imported).toEqual(['BASE']);
    });
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
    const offenders = findWrapConflictsInSources(REAL_SOURCE_SET);
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
    [
      'the whole object through an identifier key named like a listed key',
      "const S = { a: 'wrap-anywhere', k: 'p-1' }; const E = () => <div className={`wrap-break-word ${S[k]}`} />;"
    ],
    [
      'an object literal with a dynamic computed key',
      "const S = { b: 'p-1', [k]: 'wrap-anywhere' }; const E = () => <div className={`wrap-break-word ${S.b}`} />;"
    ],
    ['one branch of a ternary', "const E = () => <div className={c ? 'wrap-anywhere wrap-break-word' : ''} />;"],
    [
      'two separate ternaries (both can hold)',
      "const E = () => <div className={`${c ? 'wrap-anywhere' : ''} ${d ? 'wrap-break-word' : ''}`} />;"
    ],
    ['an && part and a fixed class', "const E = () => <div className={`wrap-break-word ${c && 'wrap-anywhere'}`} />;"],
    [
      'a constant referred to in both branches',
      "const w = 'wrap-anywhere'; const E = () => <div className={c ? w : `${w} wrap-break-word`} />;"
    ],
    [
      'one side of a nested ternary',
      "const E = () => <div className={c ? `wrap-anywhere ${d ? 'wrap-break-word' : ''}` : 'wrap-break-word'} />;"
    ],
    ['the left side of || with both', "const E = () => <div className={('wrap-anywhere wrap-break-word' as string) || 'p-1'} />;"],
    [
      'the properties of an object taken in whole (reported conservatively)',
      "const S = { a: 'wrap-anywhere', b: 'wrap-break-word' }; const E = () => <div className={S[k]} />;"
    ],
    [
      "a class-named function's returns (reported conservatively)",
      "function wrapClass(c: boolean) { if (c) return 'wrap-anywhere'; return 'wrap-break-word'; }"
    ],
    [
      "a helper object's keys, whatever their conditions (reported conservatively)",
      "const E = () => <div className={clsx({ 'wrap-anywhere': c, 'wrap-break-word': !c })} />;"
    ],
    ['the arguments of a class helper', "const E = () => <div className={clsx('wrap-anywhere', 'wrap-break-word')} />;"],
    ["a helper argument and a helper object's key", "const E = () => <div className={cn('wrap-anywhere', { 'wrap-break-word': c })} />;"],
    ['a helper outside any class expression', "const v = twMerge(['wrap-anywhere', c && 'wrap-break-word']);"]
  ])('reports wrap-break-word combined with wrap-anywhere across %s', (_label, source) => {
    expect(findWrapConflicts('sample.tsx', source)).toHaveLength(1);
  });

  it('reports a helper call inside a className once, as the className', () => {
    expect(findWrapConflicts('sample.tsx', "const E = () => <div className={cn('wrap-anywhere', c && 'wrap-break-word')} />;")).toEqual([
      'sample.tsx:1 (className)'
    ]);
    expect(findWrapConflicts('sample.tsx', "const v = clsx('wrap-anywhere', { 'wrap-break-word': c });")).toEqual([
      'sample.tsx:1 (clsx())'
    ]);
  });

  it.each([
    [
      "a class-named constant's useMemo callback",
      "const buttonClass = useMemo(() => clsx('wrap-anywhere', 'wrap-break-word'), []);",
      ['sample.tsx:1 (clsx())']
    ],
    [
      "a className's .map callback",
      "const E = () => <div className={items.map(i => clsx('wrap-anywhere', i && 'wrap-break-word')).join(' ')} />;",
      ['sample.tsx:1 (clsx())']
    ],
    [
      "a non-helper call's argument in a className",
      "const E = () => <div className={foo(cn('wrap-anywhere', 'wrap-break-word'))} />;",
      ['sample.tsx:1 (cn())']
    ],
    [
      'a helper call nested in another one behind a non-helper call (the outer one only)',
      "const E = () => <div className={foo(clsx('wrap-anywhere', cn('wrap-break-word')))} />;",
      ['sample.tsx:1 (clsx())']
    ],
    [
      'a constant a className refers to (the className only)',
      "const w = clsx('wrap-anywhere', 'wrap-break-word');\nconst E = () => <div className={w} />;",
      ['sample.tsx:2 (className)']
    ]
  ])('reports a helper call behind a non-helper call, once (%s)', (_label, source, expected) => {
    expect(findWrapConflicts('sample.tsx', source)).toEqual(expected);
  });

  it("does not combine a helper call's classes with the classes around the non-helper call it is behind", () => {
    expect(
      findWrapConflicts('sample.tsx', "const E = () => <div className={`wrap-anywhere ${foo(cn('wrap-break-word'))}`} />;")
    ).toEqual([]);
  });

  it('reports the combination across an imported constant', () => {
    const sources = sampleSources({
      'src/components/A.tsx':
        "import { WRAP } from '../styles';\nconst E = () => <div className={`wrap-break-word ${WRAP}`} />;",
      'src/styles/index.ts': "export const WRAP = 'wrap-anywhere';"
    });
    expect(findWrapConflictsInSources(sources)).toEqual(['src/components/A.tsx:2 (className)']);
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
    ],
    ['exclusive ternary branches', "const E = () => <div className={c ? 'wrap-anywhere' : 'wrap-break-word'} />;"],
    [
      'exclusive ternary branches inside a template',
      "const E = () => <div className={`p-1 ${c ? 'wrap-anywhere' : 'wrap-break-word'}`} />;"
    ],
    [
      'exclusive branches of a nested ternary',
      "const E = () => <div className={`p-1 ${c ? 'wrap-anywhere' : d ? 'wrap-break-word' : ''}`} />;"
    ],
    [
      'the two sides of ||',
      "const w = c ? 'wrap-anywhere' : ''; const E = () => <div className={w || 'wrap-break-word'} />;"
    ],
    [
      'the two sides of ??',
      "const w = c ? 'wrap-anywhere' : undefined; const E = () => <div className={`p-1 ${w ?? 'wrap-break-word'}`} />;"
    ],
    [
      'exclusive ternary branches in a constant',
      "const w = c ? 'wrap-anywhere' : 'wrap-break-word'; const E = () => <div className={`p-1 ${w}`} />;"
    ],
    [
      'exclusive ternary branches in a helper argument',
      "const E = () => <div className={clsx('p-1', c ? 'wrap-anywhere' : 'wrap-break-word')} />;"
    ],
    [
      "a helper object's value (a condition, not a class)",
      "const E = () => <div className={clsx('wrap-anywhere', { 'p-1': 'wrap-break-word' })} />;"
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

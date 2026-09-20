// DFLT-00102: INVALID_NODE_STATE is the error the Web UI sees when a node is
// completed from a state it cannot be completed from -- most often an approval
// gate handled in the browser and again in a terminal.
//
// The UI renders the localized `errors.<code>` string, never the backend's own
// message (see src/lib/apiError.ts's translateErrorCode). That is why the
// wording is checked here at all: the engine's message tells a CLI user to run
// unstick-node or reopen-nodes, and translating that for someone looking at a
// web page would hand them commands they are not in a position to run. The
// translations say what a viewer of the page can actually do instead.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

type Tree = { [key: string]: string | Tree };

const errors = (tree: Tree) => tree.errors as Record<string, string>;

describe('error translations', () => {
  it('define the same error codes in ja and en', () => {
    expect(Object.keys(errors(ja as Tree)).sort()).toEqual(Object.keys(errors(en as Tree)).sort());
  });

  it('localize INVALID_NODE_STATE in both locales', () => {
    for (const [locale, tree] of [
      ['ja', ja],
      ['en', en],
    ] as const) {
      const message = errors(tree as Tree).INVALID_NODE_STATE;
      expect(message, `${locale} is missing errors.INVALID_NODE_STATE`).toBeTruthy();
      // No CLI command names: this string is read in a browser.
      expect(message).not.toContain('unstick-node');
      expect(message).not.toContain('reopen-nodes');
    }
  });
});

package config

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaults/locales/*.yaml is a generated mirror of
// packages/plugin/defaults/locales/*.yaml, kept in sync the same way as
// defaults/workflow.yaml (see load.go's doc comment on defaultYAML) --
// `npm run sync:defaults` copies the plugin's hand-edited source in here so
// it can be embedded into the compiled binary.
//
//go:embed defaults/locales/*.yaml
var defaultLocaleFS embed.FS

// localeFile is the on-disk/embedded shape of one defaults/locales/<code>.yaml
// file.
type localeFile struct {
	Version     int               `yaml:"version"`
	Language    string            `yaml:"language"`
	Nodes       map[string]string `yaml:"nodes"`
	ReviewGates map[string]string `yaml:"review_gates"`
}

// Locale is one language's display-name translations for the fixed workflow
// skeleton's nodes and the plugin's default review gates, keyed by node/gate
// id. It covers names only -- not criteria, max_iterations, or any other
// field (see the execution plan, section 1.2).
type Locale struct {
	Nodes       map[string]string
	ReviewGates map[string]string
}

// LoadLocale reads defaults/locales/<code>.yaml and returns its parsed
// contents. ok is false (with a zero Locale and nil error) when no locale
// file exists for code -- an unsupported/unknown language code is not an
// error, since ApplyLocale/LocalizedDefault treat it as "leave the English
// plugin default as-is" (see LocalizedDefault's doc comment).
func LoadLocale(code string) (Locale, bool, error) {
	if code == "" {
		return Locale{}, false, nil
	}
	raw, err := defaultLocaleFS.ReadFile("defaults/locales/" + code + ".yaml")
	if err != nil {
		return Locale{}, false, nil
	}
	var lf localeFile
	if err := yaml.Unmarshal(raw, &lf); err != nil {
		return Locale{}, false, fmt.Errorf("parsing locale %q: %w", code, err)
	}
	return Locale{Nodes: lf.Nodes, ReviewGates: lf.ReviewGates}, true, nil
}

// SupportedLocales returns every language code with an embedded locale file
// (e.g. ["ja"]), sorted, derived from the embedded filenames themselves
// rather than a hardcoded list -- so get-language-settings and tests reflect
// whatever locale files actually ship, not a list that can drift from them.
func SupportedLocales() []string {
	entries, err := defaultLocaleFS.ReadDir("defaults/locales")
	if err != nil {
		return nil
	}
	var codes []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		codes = append(codes, strings.TrimSuffix(name, ".yaml"))
	}
	sort.Strings(codes)
	return codes
}

// ApplyLocale returns a copy of doc with only the Name field of each
// workflow node and each review gate replaced by locale's translation, where
// one exists for that node/gate id; every other field (Criteria,
// MaxIterations, Enabled, DependsOn, Type, Gate, ...) is left untouched, and
// doc itself is never mutated. A node/gate id absent from locale keeps its
// original (English) Name.
func ApplyLocale(doc Document, locale Locale) Document {
	out := doc

	if len(locale.Nodes) > 0 && len(doc.Workflow.Nodes) > 0 {
		nodes := make([]NodeDef, len(doc.Workflow.Nodes))
		copy(nodes, doc.Workflow.Nodes)
		for i, n := range nodes {
			if name, ok := locale.Nodes[n.ID]; ok {
				n.Name = name
				nodes[i] = n
			}
		}
		out.Workflow.Nodes = nodes
	}

	if len(locale.ReviewGates) > 0 && len(doc.ReviewGates) > 0 {
		gates := make(map[string]ReviewGateDef, len(doc.ReviewGates))
		for id, g := range doc.ReviewGates {
			if name, ok := locale.ReviewGates[id]; ok {
				g.Name = name
			}
			gates[id] = g
		}
		out.ReviewGates = gates
	}

	return out
}

// ResolveLanguage picks the effective language code from an explicit
// override plus any number of Documents, in increasing priority order
// (tiers[0] lowest, tiers[len-1] highest) -- the same "later argument wins"
// convention as Merge(def, userDoc, teamDoc). languageOverride, when
// non-empty, always wins over every tier: it represents a single call's
// explicit, non-persistent choice (e.g. the CLI's --language flag), which
// this ticket's execution plan (section 1.4) deliberately ranks above the
// persistent setting precisely because it is only ever supplied when no
// persistent setting should be disturbed. With languageOverride empty, the
// result is the last non-empty Document.Language found scanning tiers
// front-to-back. Returns "" if nothing is set anywhere.
//
// The function itself is generic over whatever tiers it is handed; which
// tiers count is the caller's decision. The working language is a personal
// setting (DFLT-00153), so every production caller passes the user tier
// alone -- never the team tier's workflow.yaml (see LoadWithRoots and the
// CLI's get-language-settings, which warn about a team language instead).
func ResolveLanguage(languageOverride string, tiers ...Document) string {
	if languageOverride != "" {
		return languageOverride
	}
	var lang string
	for _, d := range tiers {
		if d.Language != "" {
			lang = d.Language
		}
	}
	return lang
}

// LocalizedDefault returns the plugin's default Document (DefaultDocument)
// with its fixed workflow skeleton and default review gate names localized
// to lang, if lang has a corresponding embedded locale file. This is the one
// shared entry point both config.LoadWithRoots and the httpserver settings
// handlers use to turn a resolved language code into a ready-to-merge
// "docs[0]" -- see the execution plan's section 1.3 for why this needed to
// be a standalone function rather than logic buried inside LoadWithRoots
// alone (settings.go does not go through LoadWithRoots at all).
//
// lang == "" or an unrecognized/unsupported code both yield the unmodified
// English default -- silently, not as an error -- so that a stale or
// not-yet-supported language code left over in a config file never breaks
// resolution (see ApplyLocale's doc comment and the plan's section 2.2 on
// this "silently ignore" decision).
func LocalizedDefault(lang string) (Document, error) {
	def, err := DefaultDocument()
	if err != nil {
		return Document{}, err
	}
	if lang == "" {
		return def, nil
	}
	locale, ok, err := LoadLocale(lang)
	if err != nil {
		return Document{}, err
	}
	if !ok {
		return def, nil
	}
	return ApplyLocale(def, locale), nil
}

// Package plugin defines the specialty-extension contract.
//
// A plugin adds clinical vocabulary and behaviour for one specialty without
// the core knowing anything about it. The core language does not know what a
// gestational age is; the obstetrics plugin does.
//
// # Capabilities are opt-in
//
// A plugin declares a Manifest and then implements whichever capability
// interfaces it needs. Nothing is mandatory beyond the manifest, so a plugin
// that only contributes autocomplete entries implements one method. This
// replaces the previous single fat interface, where every plugin had to supply
// five methods whether or not it used them.
//
// # Compatibility is declared, not hoped for
//
// Each manifest states the engine versions it works with. Once plugins are
// written by third parties, that declaration is the only thing standing between
// a core refactor and silent breakage, so a plugin outside the supported range
// is refused with a diagnostic rather than loaded and left to misbehave.
//
// # Determinism
//
// Plugins are held in registration order and consulted in that order. The
// previous engine dispatched profile tokens by ranging over a map, so when two
// registered prefixes both matched a token, which handler won varied between
// runs of identical input.
//
// # Additive only
//
// A plugin cannot shadow a core command. A collision is reported and the core
// behaviour is kept, so loading a specialty can never change what an existing
// note means.
package plugin

import (
	"fmt"
	"sort"
	"strings"

	"clinlang/pkg/lexicon"
	"clinlang/pkg/parser"
	"clinlang/pkg/sema"
)

// EngineVersion is the plugin ABI version of this engine build.
//
// Bump the major component when a capability interface changes shape, and the
// minor when one is added. Plugins declare the range they accept.
const EngineVersion = "2.0.0"

// Manifest identifies a plugin and declares its compatibility.
type Manifest struct {
	// Name is the identifier used in @profile.
	Name string

	// Version is the plugin's own semantic version.
	Version string

	// EngineRange is the engine ABI range this plugin supports, for example
	// ">=2.0.0 <3.0.0". Empty accepts any engine, which is only appropriate
	// for plugins shipped in this repository.
	EngineRange string

	// Description is a one-line human summary.
	Description string

	// Commands maps each contributed command to a one-line help string, for
	// documentation and the editor's command list.
	Commands map[string]string
}

// Plugin is the minimum a specialty extension must implement.
type Plugin interface {
	Manifest() Manifest
}

// GrammarProvider lets a plugin tell the parser how its commands are shaped.
//
// Without this a plugin command is parsed as structured arguments, which is
// wrong for prose commands: "ctx 4 in 10min lasting 50s" is a sentence, and
// tokenizing it produces a warning per word. Shape is a syntactic property, so
// it has to reach the parser rather than being discovered during analysis.
type GrammarProvider interface {
	// Shapes maps each contributed command to its argument shape.
	Shapes() map[string]parser.Shape

	// Keys contributes measurement keys for word splitting, for analytes whose
	// names contain digits and cannot be split by rule alone.
	Keys() []string
}

// VocabularyProvider lets a plugin contribute clinical terms.
//
// This is the capability that keeps medicine out of the language. The core
// lexicon ships notation only — units, durations, intensity markers, dosing
// and route abbreviations — and every drug, analyte, finding and abbreviation
// arrives through this interface.
//
// Contributions merge additively across plugins, and where two supply the same
// term the first registered wins. A specialty can therefore add its own
// vocabulary without displacing the general one.
type VocabularyProvider interface {
	Vocabulary() lexicon.Sources
}

// Analyzer is the semantic half of a plugin: it turns statements and arguments
// into IR contributions.
//
// Aliased to the interface sema already consumes, so there is one definition
// rather than two that must be kept in step.
type Analyzer = sema.Profile

// Completion is one autocomplete entry.
type Completion struct {
	// Text is inserted when the entry is chosen.
	Text string

	// Detail is the short description shown beside it.
	Detail string

	// Kind groups entries in the editor: "command", "token", "value".
	Kind string
}

// CompletionProvider lets a plugin contribute editor completions.
//
// This exists so plugin vocabulary reaches autocomplete from the plugin itself.
// The previous engine kept a hardcoded completion map in a separate package,
// which made it a third source of clinical truth alongside the JSON lexicons
// and the parser tables, and it drifted from both.
type CompletionProvider interface {
	Completions() []Completion
}

// ─────────────────────────────────────────────────────────────────────────────
// Registry
// ─────────────────────────────────────────────────────────────────────────────

// Registry holds the available plugins.
//
// A Registry is built once at start-up and then read concurrently. It is not a
// package-level global: the engine takes one explicitly, so a server can serve
// two tenants with different plugin sets and tests need no cleanup.
type Registry struct {
	order   []string
	plugins map[string]Plugin
	engine  Version
}

// NewRegistry returns an empty registry targeting the current engine version.
func NewRegistry() *Registry {
	v, err := ParseVersion(EngineVersion)
	if err != nil {
		panic("plugin: EngineVersion is not a valid version: " + EngineVersion)
	}
	return &Registry{plugins: map[string]Plugin{}, engine: v}
}

// Register adds a plugin.
//
// It fails when the name is taken or the plugin does not support this engine
// version. Registration is explicit and returns an error rather than happening
// in an init function, so a compatibility failure is reported to the operator
// instead of being discovered when a note silently loses data.
func (r *Registry) Register(p Plugin) error {
	m := p.Manifest()

	name := strings.ToLower(strings.TrimSpace(m.Name))
	if name == "" {
		return fmt.Errorf("plugin: manifest has no name")
	}
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q: already registered", name)
	}

	if m.EngineRange != "" {
		rng, err := ParseRange(m.EngineRange)
		if err != nil {
			return fmt.Errorf("plugin %q: %w", name, err)
		}
		if !rng.Allows(r.engine) {
			return fmt.Errorf(
				"plugin %q supports engine %s but this engine is %s",
				name, m.EngineRange, r.engine)
		}
	}

	r.plugins[name] = p
	r.order = append(r.order, name)
	return nil
}

// MustRegister is Register for plugins shipped in this repository, where a
// failure is a build-time mistake rather than an operator's problem.
func (r *Registry) MustRegister(p Plugin) {
	if err := r.Register(p); err != nil {
		panic(err)
	}
}

// Get returns a plugin by name.
func (r *Registry) Get(name string) (Plugin, bool) {
	p, ok := r.plugins[strings.ToLower(name)]
	return p, ok
}

// Names returns every registered plugin name in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Manifests returns every manifest, sorted by name for stable documentation.
func (r *Registry) Manifests() []Manifest {
	out := make([]Manifest, 0, len(r.plugins))
	for _, n := range r.order {
		out = append(out, r.plugins[n].Manifest())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Analyzers returns the plugins that contribute semantics, keyed by name, in a
// form sema accepts.
func (r *Registry) Analyzers() map[string]sema.Profile {
	out := make(map[string]sema.Profile, len(r.plugins))
	for _, n := range r.order {
		if a, ok := r.plugins[n].(Analyzer); ok {
			out[n] = a
		}
	}
	return out
}

// Grammar builds a parser Grammar combining the core shapes and keys with
// every plugin's contributions.
//
// Core shapes always win. A plugin declaring a shape for an existing core
// command is refused that entry, because a plugin must never change how an
// existing note parses.
func (r *Registry) Grammar(coreKeys []string) (*parser.StaticGrammar, []string) {
	shapes := make(map[string]parser.Shape, len(parser.CoreShapes)+8)
	for k, v := range parser.CoreShapes {
		shapes[k] = v
	}

	keys := make([]string, 0, len(coreKeys)+8)
	keys = append(keys, coreKeys...)

	var conflicts []string
	for _, n := range r.order {
		gp, ok := r.plugins[n].(GrammarProvider)
		if !ok {
			continue
		}
		for cmd, shape := range gp.Shapes() {
			cmd = strings.ToLower(cmd)
			if _, isCore := parser.CoreShapes[cmd]; isCore {
				conflicts = append(conflicts, fmt.Sprintf(
					"plugin %q: command %q is a core command and cannot be redefined", n, cmd))
				continue
			}
			shapes[cmd] = shape
		}
		keys = append(keys, gp.Keys()...)
	}

	sort.Strings(conflicts)
	return parser.NewStaticGrammar(shapes, keys), conflicts
}

// Vocabulary returns every plugin's lexicon contribution, in registration
// order so that merge precedence is deterministic.
func (r *Registry) Vocabulary() []lexicon.Sources {
	var out []lexicon.Sources
	for _, n := range r.order {
		if vp, ok := r.plugins[n].(VocabularyProvider); ok {
			out = append(out, vp.Vocabulary())
		}
	}
	return out
}

// Completions returns every plugin-contributed completion, sorted so the
// editor's list never depends on map or registration order.
func (r *Registry) Completions() []Completion {
	var out []Completion
	for _, n := range r.order {
		if cp, ok := r.plugins[n].(CompletionProvider); ok {
			out = append(out, cp.Completions()...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Text < out[j].Text
	})
	return out
}

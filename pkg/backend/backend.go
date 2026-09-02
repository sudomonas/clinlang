// Package backend renders the clinical IR into output formats.
//
// A backend is a visitor over the IR. It receives a finished case and returns
// bytes. It performs no file access, reads no globals, and consults no clock,
// which makes every backend trivially testable against a golden file and makes
// identical input produce identical output — the determinism the project
// promises and already tests for.
//
// # Why the IR and not the syntax tree
//
// Backends read meaning, not text. A prescription reaches a backend as a dose
// quantity, a route coding and a structured timing, so rendering it is a
// formatting decision rather than a parsing one. The previous engine formatted
// straight from the parsed struct, which is why the prescription-rendering
// block appeared nearly verbatim in two formatters and why the "three times
// daily" fact existed only as an English string in a Go map.
//
// # What backends must not do
//
// A backend renders what the IR contains. It does not rank findings, mark a
// value as notable, reorder by importance, or add any annotation the clinician
// did not write. Ordering is source order throughout, because ordering by
// anything else would be interpretation.
package backend

import (
	"fmt"
	"sort"
	"strings"

	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
)

// Options controls rendering. The zero value is the normal configuration.
type Options struct {
	// Verbatim renders exactly what the clinician typed, without expanding
	// abbreviations. The expanded form is the default because it is what a
	// note is read from; the original is always retained in the IR either way.
	Verbatim bool

	// HideDerived omits engine-computed values such as BMI and BSA.
	HideDerived bool

	// Diagnostics appends parser and analysis messages to the output. Off by
	// default: the API serves diagnostics on their own endpoint, and a note
	// handed to a colleague should not carry the engine's working notes.
	Diagnostics []diag.Diagnostic
}

// Backend renders an IR case.
type Backend interface {
	// Name is the stable identifier used by the CLI and API, such as "soap".
	Name() string

	// ContentType is the MIME type of the output.
	ContentType() string

	// Emit renders the case. The returned diagnostics describe rendering
	// problems only; analysis diagnostics arrive through Options.
	Emit(c *ir.Case, opts Options) ([]byte, []diag.Diagnostic, error)
}

// registry holds the available backends by name.
var registry = map[string]Backend{}

func register(b Backend) { registry[b.Name()] = b }

func init() {
	register(&SOAP{})
	register(&Plain{})
	register(&Markdown{})
	register(&JSON{})
}

// Get returns a backend by name.
func Get(name string) (Backend, bool) {
	b, ok := registry[strings.ToLower(name)]
	return b, ok
}

// Names returns every registered backend name, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Render is a convenience wrapper around Get and Emit.
func Render(name string, c *ir.Case, opts Options) ([]byte, []diag.Diagnostic, error) {
	b, ok := Get(name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown backend %q; available: %s",
			name, strings.Join(Names(), ", "))
	}
	return b.Emit(c, opts)
}

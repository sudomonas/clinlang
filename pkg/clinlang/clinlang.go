// Package clinlang is the public entry point to the engine.
//
// It wires the pipeline — lex, parse, analyse, render — and is what the CLI,
// the HTTP API and any embedding program call. Everything below it is
// independently usable, but nothing else has to know the order of the stages.
//
//	source text
//	   → lexer   → tokens with spans
//	   → parser  → AST, lossless and positioned
//	   → sema    → clinical IR, normalized and identified
//	   → backend → SOAP, plain, markdown, JSON
//
// # Concurrency
//
// A Result is owned by its caller and shares nothing mutable with any other
// parse. Options carry the lexicon and plugin registry explicitly rather than
// reaching for package globals, so a server can parse concurrently and can
// serve two tenants with different configurations. The previous engine kept the
// abbreviation table and the active reference ranges in package variables,
// which made both of those unsafe.
//
// # Bounded work
//
// Parse takes a context and enforces a size limit. A large paste cannot occupy
// a server goroutine indefinitely, and cancellation is observed between stages.
package clinlang

import (
	"context"
	"fmt"

	"clinlang/pkg/ast"
	"clinlang/pkg/backend"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/lexicon"
	"clinlang/pkg/parser"
	"clinlang/pkg/plugin"
	"clinlang/pkg/sema"
	"clinlang/pkg/source"
)

// DefaultMaxInputBytes bounds a single parse.
//
// A clinical note is a few kilobytes; a megabyte is already far outside normal
// use and is more likely a paste accident or an attempt to exhaust the server.
const DefaultMaxInputBytes = 1 << 20

// DefaultMaxDiagnostics bounds how many messages one parse may accumulate, so
// pathological input cannot turn into unbounded memory.
const DefaultMaxDiagnostics = 2000

// Options configures a parse.
//
// The zero value is valid and uses the embedded lexicon with no plugins.
type Options struct {
	// Lexicon supplies the clinical vocabulary. Nil uses the embedded default.
	Lexicon *lexicon.Lexicon

	// Plugins are the specialty extensions available to @profile. Nil means
	// none, and a document naming a profile then gets a diagnostic rather than
	// silently losing its specialty data.
	Plugins *plugin.Registry

	// MaxInputBytes overrides DefaultMaxInputBytes. Negative disables the cap.
	MaxInputBytes int

	// MaxDiagnostics overrides DefaultMaxDiagnostics.
	MaxDiagnostics int
}

// lex returns the lexicon for this parse.
//
// When the caller supplies none, one is built from the core notation plus
// whatever vocabulary the registered plugins contribute. That is the whole
// point of the split: the language ships no clinical terms, and medicine
// arrives as a plugin like any other specialty.
func (o Options) lex() *lexicon.Lexicon {
	if o.Lexicon != nil {
		return o.Lexicon
	}
	if o.Plugins == nil {
		return lexicon.Default()
	}
	lex, err := lexicon.Load(o.Plugins.Vocabulary()...)
	if err != nil {
		// A plugin shipping malformed vocabulary should not stop a note from
		// parsing; the language still works without any vocabulary at all.
		return lexicon.Default()
	}
	return lex
}

func (o Options) maxInput() int {
	switch {
	case o.MaxInputBytes < 0:
		return -1
	case o.MaxInputBytes == 0:
		return DefaultMaxInputBytes
	default:
		return o.MaxInputBytes
	}
}

func (o Options) maxDiagnostics() int {
	if o.MaxDiagnostics > 0 {
		return o.MaxDiagnostics
	}
	return DefaultMaxDiagnostics
}

// Result is everything one parse produced.
//
// All four are retained because different callers need different layers: the
// editor wants diagnostics and the AST, a backend wants the IR, and a
// quick-fix wants the file to resolve spans against.
type Result struct {
	File        *source.File
	Doc         *ast.Document
	Case        *ir.Case
	Diagnostics []diag.Diagnostic
}

// HasErrors reports whether any diagnostic is at error severity.
func (r *Result) HasErrors() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// Filter returns diagnostics at or above a severity.
func (r *Result) Filter(min diag.Severity) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range r.Diagnostics {
		if d.Severity >= min {
			out = append(out, d)
		}
	}
	return out
}

// Parse runs the full pipeline over src.
//
// It returns an error only for conditions that prevent parsing at all —
// oversized input, a cancelled context. Everything the engine could not
// interpret arrives as a positioned diagnostic in the Result, because a note
// must still render even when part of it was not understood.
func Parse(ctx context.Context, name, src string, opts Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if max := opts.maxInput(); max >= 0 && len(src) > max {
		return nil, fmt.Errorf("input is %d bytes, over the %d byte limit", len(src), max)
	}

	lex := opts.lex()
	bag := diag.NewBagWithLimit(opts.maxDiagnostics())
	file := source.NewFile(name, src)

	grammar, conflicts := buildGrammar(lex, opts.Plugins)
	for _, c := range conflicts {
		// Reported without a position: a collision is a configuration fact
		// about the loaded plugins, not about anything in this document.
		bag.Warn(diag.CommandConflict, source.NoSpan, c)
	}

	doc := parser.Parse(file, grammar, bag)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c := sema.Analyze(file, doc, bag, sema.Options{
		Lexicon:  lex,
		Profiles: profiles(opts.Plugins),
	})

	return &Result{
		File:        file,
		Doc:         doc,
		Case:        c,
		Diagnostics: bag.Sorted(),
	}, nil
}

// ParseString is Parse with a background context and default options.
func ParseString(src string) (*Result, error) {
	return Parse(context.Background(), "", src, Options{})
}

// Render renders a parsed result with the named backend.
func Render(r *Result, name string, opts backend.Options) ([]byte, error) {
	out, _, err := backend.Render(name, r.Case, opts)
	return out, err
}

// Backends returns the available backend names.
func Backends() []string { return backend.Names() }

// buildGrammar combines core and plugin grammar contributions.
func buildGrammar(lex *lexicon.Lexicon, reg *plugin.Registry) (parser.Grammar, []string) {
	if reg == nil {
		return parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys()), nil
	}
	return reg.Grammar(lex.MeasurementKeys())
}

func profiles(reg *plugin.Registry) map[string]sema.Profile {
	if reg == nil {
		return nil
	}
	return reg.Analyzers()
}

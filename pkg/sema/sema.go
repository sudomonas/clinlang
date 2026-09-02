// Package sema turns a ClinLang AST into the clinical IR.
//
// This is where the engine is allowed to know medicine. The lexer knows
// character shapes, the parser knows grammar, and neither can look anything up.
// Sema holds the lexicon, so it is the only stage that can decide that "tds"
// is a dosing frequency, that "hb" names an analyte, or that a trailing capital
// F on an age means female — and, crucially, the only stage that can raise a
// positioned diagnostic when one of those lookups fails.
//
// # Two rules that shape the design
//
// Sema never mutates the AST. It reads a tree and produces a new IR, so the
// tree stays valid as the editor's model and analysis can be re-run, cached and
// tested in isolation.
//
// Sema never evaluates a clinical value. It records that a temperature is 37
// with no unit, or that a haemoglobin is 8.2; it does not observe that either
// is low, unusual or worth attention. Recording is in scope, rating is not.
//
// # Scope is built before statements are walked
//
// Pragmas are collected by the parser and resolved here in a pre-pass, so a
// profile declaration takes effect for the whole document regardless of where
// it appears. The previous engine registered profiles mid-walk, which silently
// required @profile to precede any line using a profile token.
package sema

import (
	"strings"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/lexicon"
	"clinlang/pkg/source"
)

// Profile is a specialty extension.
//
// Kept minimal here so sema does not depend on the plugin package; the full
// capability-based plugin API composes with this rather than replacing it.
type Profile interface {
	// Name is the identifier used in @profile.
	Name() string

	// NewData allocates this profile's typed extension payload, which is
	// stored on the encounter and retrieved with ir.Extension[T].
	NewData() any

	// Command handles a statement whose command this profile owns. It returns
	// false if the command is not one of its own.
	Command(cmd string, st *ast.Statement, ctx *Context) bool

	// Argument handles an argument that a core command could not interpret,
	// letting a profile extend pt or vitals without touching core code. It
	// returns false when the argument is not its concern.
	Argument(cmd string, arg ast.Argument, ctx *Context) bool
}

// Options configures analysis.
type Options struct {
	// Lexicon supplies the vocabulary. Nil uses the embedded default.
	Lexicon *lexicon.Lexicon

	// Profiles are the specialty extensions available to be activated by an
	// @profile pragma. A profile that is registered but not declared stays
	// inert.
	Profiles map[string]Profile
}

// Context is the analysis state handed to profiles.
type Context struct {
	File      *source.File
	Lex       *lexicon.Lexicon
	Diags     *diag.Bag
	Case      *ir.Case
	Encounter *ir.Encounter

	// Profile is the name of the profile currently handling a statement,
	// used to key its extension data.
	Profile string
}

// Data returns the current profile's extension payload.
func (c *Context) Data() any {
	if c.Encounter == nil || c.Profile == "" {
		return nil
	}
	return c.Encounter.Extensions[c.Profile]
}

// Warn records a warning against a node.
func (c *Context) Warn(code diag.Code, n ast.Node, msg string) {
	span := source.NoSpan
	if n != nil {
		span = n.Span()
	}
	c.Diags.Warn(code, span, msg)
}

// analyzer holds per-run state.
type analyzer struct {
	file *source.File
	lex  *lexicon.Lexicon
	bag  *diag.Bag
	opts Options

	cs  *ir.Case
	enc *ir.Encounter

	profiles []Profile

	// seen tracks statements that may appear only once, for duplicate
	// reporting with a pointer back to the first occurrence.
	seen map[string]source.Span
}

// Analyze converts a parsed document into the clinical IR.
//
// It never fails. Input it cannot interpret produces diagnostics and is either
// recorded verbatim or omitted, so a document always yields a renderable case.
func Analyze(f *source.File, doc *ast.Document, bag *diag.Bag, opts Options) *ir.Case {
	lex := opts.Lexicon
	if lex == nil {
		lex = lexicon.Default()
	}

	a := &analyzer{
		file: f,
		lex:  lex,
		bag:  bag,
		opts: opts,
		cs:   &ir.Case{},
		seen: map[string]source.Span{},
	}
	a.enc = a.cs.Primary()
	a.enc.Narratives = map[string]ir.Narrative{}

	a.resolvePragmas(doc)

	for _, st := range doc.Statements {
		a.statement(st)
	}

	a.derive()
	a.tidy()
	return a.cs
}

// resolvePragmas builds the document scope before any statement is walked.
func (a *analyzer) resolvePragmas(doc *ast.Document) {
	for _, p := range doc.Pragmas {
		if p.Name == nil {
			continue
		}
		name := strings.ToLower(p.Name.Raw)

		switch name {
		case "clinlang":
			a.cs.LangVersion = argText(a.file, p.Args)

		case "profile":
			for _, spec := range p.Args {
				a.activateProfile(conceptText(spec), spec)
			}
			if len(p.Args) == 0 {
				a.bag.Warn(diag.MalformedPragma, p.Sp, "@profile names no profile")
			}

		default:
			// "@obgyn" is the short form of "@profile obgyn", and "@obgyn+peds"
			// stacks them. A directive naming at least one registered profile
			// is treated as a profile declaration; any unregistered component
			// is then reported individually, so a partially-available stack
			// still activates what it can.
			if a.namesAnyProfile(name) {
				a.activateProfile(name, p.Name)
				continue
			}
			a.bag.Warn(diag.UnknownPragma, p.Sp, "unknown directive @"+p.Name.Raw)
		}
	}
}

// namesAnyProfile reports whether a '+'-separated spec names at least one
// registered profile.
func (a *analyzer) namesAnyProfile(spec string) bool {
	for _, name := range strings.Split(strings.ToLower(spec), "+") {
		if _, ok := a.opts.Profiles[strings.TrimSpace(name)]; ok {
			return true
		}
	}
	return false
}

// activateProfile enables a profile, accepting the "a+b" combined form.
func (a *analyzer) activateProfile(spec string, n ast.Node) {
	for _, name := range strings.Split(strings.ToLower(strings.TrimSpace(spec)), "+") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if a.hasProfile(name) {
			continue
		}

		p, ok := a.opts.Profiles[name]
		if !ok {
			span := source.NoSpan
			if n != nil {
				span = n.Span()
			}
			a.bag.Warn(diag.UnknownProfile, span, "unknown profile '"+name+"'")
			continue
		}

		a.profiles = append(a.profiles, p)
		a.cs.Profiles = append(a.cs.Profiles, name)

		// Each profile gets its own typed payload, keyed by name. Multiple
		// profiles therefore coexist; the previous engine replaced the single
		// payload with a map when more than one was active, which made every
		// profile's type assertion fail at once and silently disabled them all.
		ir.SetExtension(a.enc, name, p.NewData())
	}
}

func (a *analyzer) hasProfile(name string) bool {
	for _, p := range a.profiles {
		if p.Name() == name {
			return true
		}
	}
	return false
}

// ctx builds the profile-facing context for the named profile.
func (a *analyzer) ctx(profile string) *Context {
	return &Context{
		File:      a.file,
		Lex:       a.lex,
		Diags:     a.bag,
		Case:      a.cs,
		Encounter: a.enc,
		Profile:   profile,
	}
}

// offerToProfiles gives each active profile a chance to handle a statement.
//
// Profiles are tried in declaration order, which is deterministic. The previous
// engine ranged over a map to dispatch profile tokens, so when two prefixes
// both matched, which one won varied between runs of the same input.
func (a *analyzer) offerStatement(cmd string, st *ast.Statement) bool {
	for _, p := range a.profiles {
		if p.Command(cmd, st, a.ctx(p.Name())) {
			return true
		}
	}
	return false
}

// offerArgument gives each active profile a chance to handle one argument that
// a core command could not interpret.
func (a *analyzer) offerArgument(cmd string, arg ast.Argument) bool {
	for _, p := range a.profiles {
		if p.Argument(cmd, arg, a.ctx(p.Name())) {
			return true
		}
	}
	return false
}

// markOnce reports a duplicate of a statement that may appear only once.
func (a *analyzer) markOnce(cmd string, sp source.Span) {
	if first, dup := a.seen[cmd]; dup {
		a.bag.Add(diag.
			New(diag.DuplicateStatement, diag.Warning, sp,
				"'"+cmd+"' appears more than once; the later value is used").
			WithRelated(first, "first given here"))
		return
	}
	a.seen[cmd] = sp
}

// tidy drops the empty narrative map so JSON output stays clean.
func (a *analyzer) tidy() {
	if len(a.enc.Narratives) == 0 {
		a.enc.Narratives = nil
	}
}

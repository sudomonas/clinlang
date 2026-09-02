// Package diag defines the diagnostic model shared by every ClinLang stage.
//
// A Diagnostic is a positioned, coded, severity-bearing statement about the
// source text. This replaces the previous engine's []string warnings, which
// had no position (line numbers were formatted into the message), no code (so
// nothing could be documented, filtered, or translated), and no severity (so
// everything was equally loud).
//
// The field semantics deliberately mirror the Language Server Protocol, so the
// editor layer converts a Diagnostic to an LSP diagnostic with a struct literal
// and no interpretation. That is what lets the web editor render squiggles from
// engine output instead of reimplementing parsing in TypeScript.
//
// Scope note: ClinLang diagnostics describe the *text*, never the *patient*.
// The engine reports that a token could not be parsed or that a dose has no
// unit. It does not report that a value is high, low, unusual, or concerning.
// See the package-level codes documentation for the reserved-but-unimplemented
// terminology and data-sanity ranges.
package diag

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"clinlang/pkg/source"
)

// Code is a stable diagnostic identifier such as "CLN2003".
type Code string

// Severity ranks how strongly a diagnostic should be surfaced.
//
// The ordering is significant: higher values are more severe, and callers
// filter with >=. Error is reserved for input the engine could not represent;
// unparseable *fragments* are warnings, because ClinLang always produces a
// usable document and never refuses to render a note.
type Severity int

const (
	// Hint is advisory and editors may render it subtly or not at all.
	Hint Severity = iota
	// Info states something factual about the parse worth surfacing.
	Info
	// Warning marks input that parsed but probably not as intended.
	Warning
	// Error marks input that could not be represented at all.
	Error
)

// String returns the lowercase name of the severity, as used in JSON and CLI
// output.
func (s Severity) String() string {
	switch s {
	case Hint:
		return "hint"
	case Info:
		return "info"
	case Warning:
		return "warning"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// MarshalText renders the severity as its name, so JSON consumers see
// "warning" rather than 2.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText parses a severity name, defaulting unknown input to Warning.
func (s *Severity) UnmarshalText(b []byte) error {
	switch string(b) {
	case "hint":
		*s = Hint
	case "info":
		*s = Info
	case "error":
		*s = Error
	default:
		*s = Warning
	}
	return nil
}

// Related is a secondary location that gives a diagnostic context, such as the
// earlier statement a duplicate conflicts with.
type Related struct {
	Span    source.Span `json:"span"`
	Message string      `json:"message"`
}

// TextEdit is a machine-applicable replacement of a span with new text.
type TextEdit struct {
	Span    source.Span `json:"span"`
	NewText string      `json:"new_text"`
}

// QuickFix is a suggested repair.
//
// Confidence runs 0..1 and governs how a fix may be offered. Nothing in
// ClinLang auto-applies a fix that rewrites clinical content: the engine does
// not silently substitute what a clinician typed. Confidence decides whether a
// fix is presented as the default suggestion or merely listed.
type QuickFix struct {
	Title      string     `json:"title"`
	Edits      []TextEdit `json:"edits"`
	Confidence float64    `json:"confidence"`
}

// Diagnostic is one positioned, coded statement about the source.
type Diagnostic struct {
	Code     Code        `json:"code"`
	Severity Severity    `json:"severity"`
	Span     source.Span `json:"span"`
	Message  string      `json:"message"`
	Related  []Related   `json:"related,omitempty"`
	Fixes    []QuickFix  `json:"fixes,omitempty"`
}

// New builds a diagnostic. Prefer the severity-named helpers below at call
// sites; this exists for cases where severity is itself computed.
func New(code Code, sev Severity, span source.Span, msg string) Diagnostic {
	return Diagnostic{Code: code, Severity: sev, Span: span, Message: msg}
}

// WithRelated returns a copy of d with an additional related location.
func (d Diagnostic) WithRelated(span source.Span, msg string) Diagnostic {
	d.Related = append(slices.Clone(d.Related), Related{Span: span, Message: msg})
	return d
}

// WithFix returns a copy of d with an additional quick fix.
func (d Diagnostic) WithFix(fix QuickFix) Diagnostic {
	d.Fixes = append(slices.Clone(d.Fixes), fix)
	return d
}

// Bag collects diagnostics during a parse.
//
// A Bag is not safe for concurrent writes; each parse owns exactly one and
// threads it through the pipeline. That is deliberate — the previous engine
// accumulated warnings on a shared struct and reached for process globals,
// which made concurrent parses in the hosted server unsafe.
type Bag struct {
	items []Diagnostic
	limit int
}

// NewBag returns an empty Bag with no cap on the number of diagnostics.
func NewBag() *Bag { return &Bag{} }

// NewBagWithLimit returns a Bag that stops accumulating after limit entries.
//
// A cap matters for pathological input: a 10 MB paste of garbage should not
// produce ten million diagnostics and exhaust server memory. Overflow is
// silent by count but visible via Truncated, and callers should say so rather
// than implying the list is complete.
func NewBagWithLimit(limit int) *Bag { return &Bag{limit: limit} }

// Add appends a diagnostic unless the bag is full.
func (b *Bag) Add(d Diagnostic) {
	if b == nil {
		return
	}
	if b.limit > 0 && len(b.items) >= b.limit {
		return
	}
	b.items = append(b.items, d)
}

// Hint records an advisory diagnostic.
func (b *Bag) Hint(code Code, span source.Span, msg string) {
	b.Add(New(code, Hint, span, msg))
}

// Info records an informational diagnostic.
func (b *Bag) Info(code Code, span source.Span, msg string) {
	b.Add(New(code, Info, span, msg))
}

// Warn records a warning diagnostic.
func (b *Bag) Warn(code Code, span source.Span, msg string) {
	b.Add(New(code, Warning, span, msg))
}

// Error records an error diagnostic.
func (b *Bag) Error(code Code, span source.Span, msg string) {
	b.Add(New(code, Error, span, msg))
}

// Len returns the number of diagnostics collected.
func (b *Bag) Len() int {
	if b == nil {
		return 0
	}
	return len(b.items)
}

// Truncated reports whether the bag stopped accepting diagnostics because it
// hit its limit.
func (b *Bag) Truncated() bool {
	return b != nil && b.limit > 0 && len(b.items) >= b.limit
}

// Items returns the diagnostics in the order they were recorded.
func (b *Bag) Items() []Diagnostic {
	if b == nil {
		return nil
	}
	return slices.Clone(b.items)
}

// Sorted returns the diagnostics in a deterministic, presentation-ready order:
// by start offset, then end offset, then code, then message.
//
// Emission order depends on the order passes happen to run, which is an
// implementation detail. Sorting here is what lets golden files and the
// determinism test compare diagnostic output byte for byte.
func (b *Bag) Sorted() []Diagnostic {
	out := b.Items()
	slices.SortStableFunc(out, func(x, y Diagnostic) int {
		if n := cmp.Compare(x.Span.Start, y.Span.Start); n != 0 {
			return n
		}
		if n := cmp.Compare(x.Span.End, y.Span.End); n != 0 {
			return n
		}
		if n := cmp.Compare(x.Code, y.Code); n != 0 {
			return n
		}
		return cmp.Compare(x.Message, y.Message)
	})
	return out
}

// Filter returns the diagnostics at or above the given severity, in sorted
// order.
func (b *Bag) Filter(min Severity) []Diagnostic {
	var out []Diagnostic
	for _, d := range b.Sorted() {
		if d.Severity >= min {
			out = append(out, d)
		}
	}
	return out
}

// HasErrors reports whether any diagnostic is at Error severity.
func (b *Bag) HasErrors() bool {
	if b == nil {
		return false
	}
	for _, d := range b.items {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Merge appends every diagnostic from other into b, respecting b's limit.
func (b *Bag) Merge(other *Bag) {
	if b == nil || other == nil {
		return
	}
	for _, d := range other.items {
		b.Add(d)
	}
}

// Counts returns the number of diagnostics at each severity.
func (b *Bag) Counts() map[Severity]int {
	out := make(map[Severity]int, 4)
	if b == nil {
		return out
	}
	for _, d := range b.items {
		out[d.Severity]++
	}
	return out
}

// Summary renders a short human-readable tally such as "2 warnings, 1 hint".
// It returns the empty string when there is nothing to report.
func (b *Bag) Summary() string {
	counts := b.Counts()
	var parts []string
	for _, sev := range []Severity{Error, Warning, Info, Hint} {
		n := counts[sev]
		if n == 0 {
			continue
		}
		name := sev.String()
		if n != 1 {
			name += "s"
		}
		parts = append(parts, strconv.Itoa(n)+" "+name)
	}
	return strings.Join(parts, ", ")
}

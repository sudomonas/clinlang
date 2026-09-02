package sema

import (
	"strings"
	"unicode"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/lexicon"
	"clinlang/pkg/source"
)

// coding resolves text to a concept, always keeping the verbatim input.
//
// An unresolved concept is normal, not an error: ClinLang accepts vocabulary it
// does not know and renders it exactly as typed. The identity is simply absent.
// Kinds are tried in the order given, so the caller expresses precedence for
// its own context: a symptom resolves as a finding before an abbreviation.
func (a *analyzer) coding(text string, sp source.Span, kinds ...lexicon.ConceptKind) ir.Coding {
	c := ir.Coding{Text: text, Origin: sp}
	if concept, ok := a.lex.Resolve(text, kinds...); ok {
		c.ConceptID = concept.ID
		c.Display = concept.Display
	}
	return c
}

// codingFromText builds a coding from a free-text node, expanding
// abbreviations for display while preserving the original.
func (a *analyzer) codingFromText(ft *ast.FreeText) ir.Coding {
	if ft == nil {
		return ir.Coding{}
	}
	c := ir.Coding{Text: ft.Raw, Origin: ft.Sp}
	if expanded := a.expand(ft.Raw); expanded != ft.Raw {
		c.Display = expanded
	}
	return c
}

// narrative builds a prose section, keeping input and display side by side.
func (a *analyzer) narrative(ft *ast.FreeText) ir.Narrative {
	n := ir.Narrative{Text: ft.Raw, Origin: ft.Sp}
	if expanded := a.expand(ft.Raw); expanded != ft.Raw {
		n.Display = expanded
	}
	return n
}

// grade converts a qualifier run on a symptom into a graded value.
func (a *analyzer) grade(q *ast.Qualifier) *ir.Grade {
	g := &ir.Grade{Symbol: q.Raw, Origin: q.Sp}
	if label, ok := a.lex.Intensity(q.Raw); ok {
		g.Label = label
	}
	return g
}

// resultGrade converts a qualifier run on an investigation.
//
// Separate from grade because the same notation means different things in the
// two contexts: "-" on a symptom is a trend and reads "improving", while on a
// result it is an outcome and reads "negative". Only the statement it appears
// in can settle which vocabulary applies.
func (a *analyzer) resultGrade(q *ast.Qualifier) *ir.Grade {
	g := &ir.Grade{Symbol: q.Raw, Origin: q.Sp}
	if label, ok := a.lex.Serology(q.Raw); ok {
		g.Label = label
	}
	return g
}

// duration converts a duration node, normalising the unit to its short form.
func (a *analyzer) duration(d *ast.Duration) *ir.Duration {
	if d == nil || d.Number == nil {
		return nil
	}
	out := &ir.Duration{Value: d.Number.Val, Origin: d.Sp}
	if d.Unit != nil {
		out.Raw = d.Number.Raw + d.Unit.Raw
		if du, ok := a.lex.DurationUnit(d.Unit.Raw); ok {
			out.Unit = du.Short
		} else {
			out.Unit = d.Unit.Raw
			a.bag.Hint(diag.UnknownUnit, d.Unit.Sp, "'"+d.Unit.Raw+"' is not a known time unit")
		}
	} else {
		out.Raw = d.Number.Raw
	}
	return out
}

// quantity builds a typed quantity from a measurement, checking that any unit
// belongs to the expected dimension.
func (a *analyzer) quantity(m *ast.Measurement, dimension, defaultUnit string) *ir.Quantity {
	num, ok := m.Value.(*ast.Number)
	if !ok {
		a.bag.Warn(diag.UnexpectedToken, m.Sp,
			"'"+m.Key.Raw+"' needs a number")
		return nil
	}

	q := &ir.Quantity{Value: num.Val, Raw: num.Raw, Dimension: dimension, Origin: m.Sp}

	if m.Unit == nil {
		// No unit written: record the conventional one rather than guessing at
		// a conversion. The number itself is untouched.
		q.Unit = defaultUnit
		q.Canonical = defaultUnit
		return q
	}

	u, known := a.lex.Unit(m.Unit.Raw)
	q.Unit = m.Unit.Raw
	if !known {
		a.bag.Warn(diag.UnknownUnit, m.Unit.Sp, "'"+m.Unit.Raw+"' is not a known unit")
		return q
	}
	if u.Dimension != dimension {
		a.bag.Warn(diag.UnknownUnit, m.Unit.Sp,
			"'"+m.Unit.Raw+"' measures "+u.Dimension+", but "+m.Key.Raw+" needs "+dimension)
		return q
	}
	q.Canonical = u.Canonical
	return q
}

// applyUnit attaches a unit to a quantity, resolving its canonical spelling.
func (a *analyzer) applyUnit(q *ir.Quantity, u *ast.Unit) {
	if u == nil {
		return
	}
	q.Unit = u.Raw
	if known, ok := a.lex.Unit(u.Raw); ok {
		q.Canonical = known.Canonical
		q.Dimension = known.Dimension
		return
	}
	a.bag.Hint(diag.UnknownUnit, u.Sp, "'"+u.Raw+"' is not a known unit")
}

// expand replaces known abbreviations for display.
//
// Built on stdlib string handling, so it is Unicode-correct and linear. The
// previous implementation reimplemented ToLower and Fields over ASCII byte
// ranges and concatenated with += inside a loop, which corrupted any non-ASCII
// text and was quadratic in the length of the field.
func (a *analyzer) expand(s string) string {
	if s == "" {
		return ""
	}

	var sb strings.Builder
	sb.Grow(len(s) + len(s)/2)

	for i, word := range strings.Fields(s) {
		if i > 0 {
			sb.WriteByte(' ')
		}
		lead, core, trail := splitEdges(word)
		sb.WriteString(lead)
		if exp, ok := a.lex.Abbreviation(core); ok {
			sb.WriteString(exp)
		} else {
			sb.WriteString(core)
		}
		sb.WriteString(trail)
	}
	return sb.String()
}

// splitEdges separates surrounding punctuation from a word so that "dm2," can
// be looked up as "dm2" without losing the comma.
func splitEdges(w string) (lead, core, trail string) {
	runes := []rune(w)
	start, end := 0, len(runes)

	for start < end && isEdgePunct(runes[start]) {
		start++
	}
	for end > start && isEdgePunct(runes[end-1]) {
		end--
	}
	return string(runes[:start]), string(runes[start:end]), string(runes[end:])
}

func isEdgePunct(r rune) bool {
	return unicode.IsPunct(r) && r != '-' && r != '_' && r != '/'
}

// ─────────────────────────────────────────────────────────────────────────────
// Demographic helpers
// ─────────────────────────────────────────────────────────────────────────────

// splitAgeUnit interprets the suffix attached to an age.
//
// Case is significant and the rule is documented: lowercase letters are time
// units, a trailing uppercase M, F or O is biological sex. So "6m" is six
// months, "34M" is a 34-year-old male, and "6moF" is a six-month-old female.
//
// The previous engine had no rule at all. It stripped a trailing capital M or F
// from every token that survived its earlier handlers, so an unrecognised token
// ending in either letter set the patient's sex as a side effect.
func splitAgeUnit(lex *lexicon.Lexicon, unit string) (durationUnit, sex string, ok bool) {
	if unit == "" {
		return "", "", true // a bare number is an age in years
	}

	last := unit[len(unit)-1]
	if last == 'M' || last == 'F' || last == 'O' {
		rest := unit[:len(unit)-1]
		if rest == "" {
			return "", string(last), true
		}
		if _, known := lex.DurationUnit(rest); known {
			return rest, string(last), true
		}
	}

	if _, known := lex.DurationUnit(unit); known {
		return unit, "", true
	}
	return "", "", false
}

// sexFromToken recognises a standalone sex marker.
func sexFromToken(tok string) (string, bool) {
	switch tok {
	case "M", "F", "O":
		return tok, true
	}
	return "", false
}

func sexDisplay(sex string) string {
	switch sex {
	case "M":
		return "Male"
	case "F":
		return "Female"
	case "O":
		return "Other"
	}
	return sex
}

// ─────────────────────────────────────────────────────────────────────────────
// Small helpers
// ─────────────────────────────────────────────────────────────────────────────

// unrecognized reports an argument no core rule and no active profile handled.
//
// Every one of these carries a span, so the editor can underline the exact
// token. The previous engine emitted position-free strings such as
// "Unrecognized vitals token: xyz", which no editor could place.
func (a *analyzer) unrecognized(cmd string, arg ast.Argument) {
	if _, isErr := arg.(*ast.Error); isErr {
		return // the parser already reported this one
	}
	a.bag.Warn(diag.UnrecognizedToken, arg.Span(),
		"'"+a.file.Text(arg.Span())+"' is not recognised in a "+cmd+" statement")
}

// argKey returns the leading identifier of an argument, or "".
func argKey(arg ast.Argument) string {
	switch v := arg.(type) {
	case *ast.Measurement:
		if v.Key != nil {
			return v.Key.Raw
		}
	case *ast.ConceptRef:
		if v.Name != nil {
			return v.Name.Raw
		}
	}
	return ""
}

// conceptText returns the text of an argument used as a bare name.
func conceptText(arg ast.Argument) string {
	switch v := arg.(type) {
	case *ast.ConceptRef:
		if v.Name != nil {
			return v.Name.Raw
		}
	case *ast.Measurement:
		if v.Key != nil {
			return v.Key.Raw
		}
	}
	return ""
}

// argText joins the text of several arguments, used for pragma values.
func argText(f *source.File, args []ast.Argument) string {
	var parts []string
	for _, a := range args {
		parts = append(parts, f.Text(a.Span()))
	}
	return strings.Join(parts, " ")
}

// valueText renders a measurement value as plain text.
func valueText(v ast.Value) string {
	switch t := v.(type) {
	case *ast.Number:
		return t.Raw
	case *ast.Text:
		return t.Raw
	case *ast.Ident:
		return t.Raw
	}
	return ""
}

func spanMerge(a, b source.Span) source.Span { return source.Merge(a, b) }

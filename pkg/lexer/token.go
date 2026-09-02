package lexer

import (
	"strings"

	"clinlang/pkg/source"
)

// Kind classifies a token.
//
// The set is deliberately small and clinically neutral. The lexer knows about
// shapes — words, numbers, separators — and nothing about medicine. There is no
// unit table and no analyte list here, because every attempt to put clinical
// knowledge in a lexer produces the hardcoded escape list this rewrite exists
// to delete. Deciding that "mg" is a unit or that "pco2" is one identifier
// rather than an identifier plus a number is the job of later stages that have
// the lexicon.
type Kind uint8

const (
	// Invalid is a control character that cannot appear in source. Always
	// accompanied by a diagnostic; retained in the stream so spans stay
	// contiguous and the printer can still round-trip the input.
	//
	// Ordinary prose punctuation is Punct, not Invalid. Half of ClinLang's
	// fields are free text, so a full stop at the end of a chief complaint
	// must never produce a diagnostic.
	Invalid Kind = iota

	// EOF terminates every token stream.
	EOF

	// Newline ends a statement. Significant, because ClinLang is line-oriented.
	Newline

	// Comment is a # or // run to end of line, retained for lossless printing.
	Comment

	// Word is a run beginning with a letter: hb, amoxicillin, mg, pco2,
	// hb13.5, non-productive-cough. Internal digits, dots and hyphens are
	// included when they continue the run (see the scanner for the exact
	// rules). Splitting a Word into key, value and unit needs the lexicon and
	// happens in the parser, not here.
	Word

	// Number is a numeric literal: 140, 13.5, 0.5.
	Number

	// Date is an unambiguous ISO timestamp: YYYY-MM-DD, optionally followed by
	// THH:MM and seconds. The whole thing is one token, so the colons inside a
	// time can never be mistaken for the key/value colon.
	//
	// Slash-separated dates are deliberately not recognised: "12/25" is
	// indistinguishable from a ratio, and blood pressure is the far more common
	// reading.
	Date

	// String is a double-quoted run, allowing free text to contain characters
	// that would otherwise tokenize: rx "vitamin b12" 500mcg.
	String

	// Slash separates the sides of a ratio: bp140/90.
	Slash

	// Colon explicitly delimits a key from its value: ga:34w, cxr:wnl.
	Colon

	// Comma separates list items in free text.
	Comma

	// At introduces a pragma: @profile obgyn.
	At

	// Qualifier is a run of + or - carrying intensity or serology: +++, --.
	// Runs are never mixed; +- lexes as two Qualifier tokens.
	Qualifier

	// Percent is the % unit symbol.
	Percent

	// Punct is any other printable character: full stops, semicolons,
	// brackets, parentheses. It carries no grammatical meaning and exists so
	// that prose inside free-text fields round-trips without diagnostics.
	Punct
)

var kindNames = map[Kind]string{
	Invalid:   "Invalid",
	EOF:       "EOF",
	Newline:   "Newline",
	Comment:   "Comment",
	Word:      "Word",
	Number:    "Number",
	Date:      "Date",
	String:    "String",
	Slash:     "Slash",
	Colon:     "Colon",
	Comma:     "Comma",
	At:        "At",
	Qualifier: "Qualifier",
	Percent:   "Percent",
	Punct:     "Punct",
}

// String returns the kind's name, used in debug output and golden files.
func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return "Kind(?)"
}

// Token is one lexical unit with its exact source extent.
//
// SpaceBefore is load-bearing, not cosmetic. Adjacency is what distinguishes
// "hb13.5" (one measurement) from "hb 13.5" (two arguments), and "500mg" (a
// quantity) from "500 mg". The previous engine had no way to express this and
// compensated with prefix matching, which is the root of several of its bugs.
type Token struct {
	Kind        Kind
	Text        string
	Span        source.Span
	SpaceBefore bool
}

// Adjacent reports whether t follows the previous token with no whitespace.
func (t Token) Adjacent() bool { return !t.SpaceBefore }

// Is reports whether t has the given kind.
func (t Token) Is(k Kind) bool { return t.Kind == k }

// IsOneOf reports whether t has any of the given kinds.
func (t Token) IsOneOf(kinds ...Kind) bool {
	for _, k := range kinds {
		if t.Kind == k {
			return true
		}
	}
	return false
}

// EndsStatement reports whether t terminates the current statement.
func (t Token) EndsStatement() bool { return t.Kind == Newline || t.Kind == EOF }

// Lower returns the token text lowercased.
//
// Callers use this for lookups only. The AST always stores Text verbatim: the
// engine never silently changes what a clinician typed, and a note must render
// the original casing of a drug or eponym.
func (t Token) Lower() string { return strings.ToLower(t.Text) }

// String renders the token for debug output and golden files.
func (t Token) String() string {
	var sb strings.Builder
	sb.WriteString(t.Kind.String())
	if t.Kind != EOF && t.Kind != Newline {
		sb.WriteString("(")
		sb.WriteString(t.Text)
		sb.WriteString(")")
	}
	if t.SpaceBefore {
		sb.WriteString(" ")
	} else {
		sb.WriteString("·") // adjacency is visible in golden files
	}
	return sb.String()
}

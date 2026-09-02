// Package ast defines the syntax tree ClinLang source parses into.
//
// The AST records what the clinician typed, not what it means. It is lossless,
// positioned, and never normalized: every leaf keeps its Raw source text with
// original casing and spelling, and every node carries a Span. Interpretation —
// that "tds" is a frequency, that "34F" is an age and a sex, that "hb" names an
// analyte — happens in the semantic layer, which reads this tree and produces
// the IR.
//
// # Why the tree is separate from the output model
//
// The previous engine parsed directly into ClinicalCase, the same struct that
// was the JSON DTO and the SOAP formatter's input. That made the parser take
// presentation-shaped decisions at parse time and is the root cause of most of
// the defects the rewrite exists to fix. Keeping syntax separate means the
// parser answers only syntactic questions, and every backend reads a model
// built for it rather than one bent from SOAP's shape.
//
// # The verbatim invariant
//
// Nodes store Raw exactly as it appeared. Nothing here lowercases, expands an
// abbreviation, or substitutes a canonical term. That is a medicolegal property
// as much as a technical one: a note must be able to show precisely what was
// entered. Display forms are computed downstream and carried alongside the
// original, never in place of it.
package ast

import (
	"clinlang/pkg/source"
)

// Kind identifies a node's concrete type without a type switch.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindDocument
	KindPragma
	KindStatement
	KindMeasurement
	KindConceptRef
	KindQuantity
	KindDuration
	KindIdent
	KindNumber
	KindRatio
	KindText
	KindUnit
	KindQualifier
	KindFreeText
	KindComment
	KindError
)

var kindNames = map[Kind]string{
	KindInvalid:     "Invalid",
	KindDocument:    "Document",
	KindPragma:      "Pragma",
	KindStatement:   "Statement",
	KindMeasurement: "Measurement",
	KindConceptRef:  "ConceptRef",
	KindQuantity:    "Quantity",
	KindDuration:    "Duration",
	KindIdent:       "Ident",
	KindNumber:      "Number",
	KindRatio:       "Ratio",
	KindText:        "Text",
	KindUnit:        "Unit",
	KindQualifier:   "Qualifier",
	KindFreeText:    "FreeText",
	KindComment:     "Comment",
	KindError:       "Error",
}

func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return "Kind(?)"
}

// Node is implemented by every AST node.
type Node interface {
	Span() source.Span
	Kind() Kind
}

// Argument is a node that can appear as an argument to a statement or pragma.
//
// The unexported marker method keeps the sum type closed: adding a new argument
// shape is a deliberate change here, not something a downstream package can do
// by accident.
type Argument interface {
	Node
	argument()
}

// Value is a node that can appear on the right of a measurement key.
type Value interface {
	Node
	value()
}

// ─────────────────────────────────────────────────────────────────────────────
// Leaves
// ─────────────────────────────────────────────────────────────────────────────

// Ident is a name exactly as typed: hb, amoxicillin, back-pain, Löfgren.
//
// Raw is never lowercased. Lookups downstream fold case themselves; the tree
// keeps the clinician's spelling so the note can render it.
type Ident struct {
	Raw string
	Sp  source.Span
}

func (n *Ident) Span() source.Span { return n.Sp }
func (n *Ident) Kind() Kind        { return KindIdent }
func (n *Ident) value()            {}

// Number is a numeric literal with both its source text and parsed value.
//
// Raw is kept because "07" and "7.0" are the same value but not the same text,
// and a note that echoes a lab value should echo what was written.
type Number struct {
	Raw string
	Val float64
	Sp  source.Span
}

func (n *Number) Span() source.Span { return n.Sp }
func (n *Number) Kind() Kind        { return KindNumber }
func (n *Number) value()            {}

// Unit is a unit token as typed: mg, kg, mmHg, w, %.
//
// The AST does not judge whether a unit is known or meaningful; an unrecognised
// unit is still recorded so the semantic layer can diagnose it with a position.
type Unit struct {
	Raw string
	Sp  source.Span
}

func (n *Unit) Span() source.Span { return n.Sp }
func (n *Unit) Kind() Kind        { return KindUnit }

// Qualifier is a run of + or -: intensity on a symptom, serology on a result.
type Qualifier struct {
	Raw string
	Sp  source.Span
}

func (n *Qualifier) Span() source.Span { return n.Sp }
func (n *Qualifier) Kind() Kind        { return KindQualifier }

// Text is a non-numeric value: the "wnl" in cxr:wnl, or a quoted string.
//
// Quoted is true when the source form carried quotes; Raw excludes them, so a
// consumer gets the content while the printer can still restore the syntax.
type Text struct {
	Raw    string
	Quoted bool
	Sp     source.Span
}

func (n *Text) Span() source.Span { return n.Sp }
func (n *Text) Kind() Kind        { return KindText }
func (n *Text) value()            {}

// Ratio is a slash-separated pair, the shape of a blood pressure.
//
// This is the node that makes bp a first-class value instead of the string
// "140/90" that the previous engine stored unvalidated and then re-parsed in a
// different layer. Either side may be nil when the input was malformed; the
// parser records a diagnostic and keeps the node so the rest of the line
// still parses.
type Ratio struct {
	Numerator   *Number
	Denominator *Number
	Sp          source.Span
}

func (n *Ratio) Span() source.Span { return n.Sp }
func (n *Ratio) Kind() Kind        { return KindRatio }
func (n *Ratio) value()            {}

// Comment is a retained # or // run.
type Comment struct {
	Raw string
	Sp  source.Span
}

func (n *Comment) Span() source.Span { return n.Sp }
func (n *Comment) Kind() Kind        { return KindComment }

// FreeText is an un-tokenized remainder, used by prose commands such as cc and
// hpi. Raw is the exact source slice, whitespace and punctuation included.
type FreeText struct {
	Raw string
	Sp  source.Span
}

func (n *FreeText) Span() source.Span { return n.Sp }
func (n *FreeText) Kind() Kind        { return KindFreeText }
func (n *FreeText) argument()         {}

// ─────────────────────────────────────────────────────────────────────────────
// Arguments
// ─────────────────────────────────────────────────────────────────────────────

// Measurement is a key with an associated value: bp140/90, hb13.5, ga:34w,
// wt65kg, hiv-.
//
// It is the single shape that the previous engine re-invented in five
// incompatible recognizers. Every field except Key is optional, which is what
// lets one node cover a bare flag (Key only), a serology result (Key plus
// Qualifier), and a quantity with units (Key, Value and Unit).
type Measurement struct {
	Key      *Ident
	Explicit bool // the source used a colon, as in ga:34w
	Value    Value
	Unit     *Unit
	Qual     *Qualifier
	Sp       source.Span
}

func (n *Measurement) Span() source.Span { return n.Sp }
func (n *Measurement) Kind() Kind        { return KindMeasurement }
func (n *Measurement) argument()         {}

// ConceptRef is a named clinical concept with optional grade and duration:
// chestpain+++4h, nausea++, back-pain.
//
// The parser does not decide what the concept is. In an rx statement, "tds" and
// "po" both arrive here as plain concept references; classifying one as a
// frequency and the other as a route is the semantic layer's job, where the
// tables live and a positioned diagnostic can be raised.
type ConceptRef struct {
	Name     *Ident
	Grade    *Qualifier
	Duration *Duration
	Sp       source.Span
}

func (n *ConceptRef) Span() source.Span { return n.Sp }
func (n *ConceptRef) Kind() Kind        { return KindConceptRef }
func (n *ConceptRef) argument()         {}

// Quantity is a bare number with an optional unit: 500mg, 1g, 65.
type Quantity struct {
	Number *Number
	Unit   *Unit
	Sp     source.Span
}

func (n *Quantity) Span() source.Span { return n.Sp }
func (n *Quantity) Kind() Kind        { return KindQuantity }
func (n *Quantity) argument()         {}
func (n *Quantity) value()            {}

// Duration is a time span: 7d, 4h, 30min, and the x-prefixed x7d form.
//
// Prefixed records whether the source wrote the leading x, so the printer can
// restore it.
type Duration struct {
	Number   *Number
	Unit     *Unit
	Prefixed bool
	Sp       source.Span
}

func (n *Duration) Span() source.Span { return n.Sp }
func (n *Duration) Kind() Kind        { return KindDuration }
func (n *Duration) argument()         {}
func (n *Duration) value()            {}

// Error marks source the parser could not interpret.
//
// Error nodes are retained in the tree rather than dropped. A document always
// parses to a complete Document, so an editor still gets a usable tree while a
// line is half-typed, and nothing is silently discarded the way the previous
// parser discarded the tail of "non-productive-cough".
type Error struct {
	Raw string
	Sp  source.Span
}

func (n *Error) Span() source.Span { return n.Sp }
func (n *Error) Kind() Kind        { return KindError }
func (n *Error) argument()         {}

// ─────────────────────────────────────────────────────────────────────────────
// Containers
// ─────────────────────────────────────────────────────────────────────────────

// Pragma is an @-directive: @profile obgyn, @clinlang 1.0.
//
// Pragmas are collected separately from statements so the semantic layer can
// build its scope in a pre-pass. That removes the previous engine's ordering
// constraint, where @profile had to appear before any command using a plugin
// token because registration happened mid-walk.
type Pragma struct {
	Name *Ident
	Args []Argument
	Sp   source.Span
}

func (n *Pragma) Span() source.Span { return n.Sp }
func (n *Pragma) Kind() Kind        { return KindPragma }

// Statement is one logical line: a command and its arguments.
//
// Trailing carries the un-tokenized remainder for prose commands. A statement
// has either structured Args or a Trailing FreeText, never both.
type Statement struct {
	Command  *Ident
	Args     []Argument
	Trailing *FreeText
	Sp       source.Span
}

func (n *Statement) Span() source.Span { return n.Sp }
func (n *Statement) Kind() Kind        { return KindStatement }

// Document is the root node.
type Document struct {
	Pragmas    []*Pragma
	Statements []*Statement
	Comments   []*Comment
	Sp         source.Span
}

func (n *Document) Span() source.Span { return n.Sp }
func (n *Document) Kind() Kind        { return KindDocument }

// FindStatement returns the first statement whose command matches name,
// case-insensitively, or nil.
func (n *Document) FindStatement(name string) *Statement {
	for _, st := range n.Statements {
		if st.Command != nil && equalFold(st.Command.Raw, name) {
			return st
		}
	}
	return nil
}

// FindPragma returns the first pragma with the given name, or nil.
func (n *Document) FindPragma(name string) *Pragma {
	for _, p := range n.Pragmas {
		if p.Name != nil && equalFold(p.Name.Raw, name) {
			return p
		}
	}
	return nil
}

// equalFold is ASCII-insensitive comparison for command and pragma names,
// which are language keywords rather than clinical text.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

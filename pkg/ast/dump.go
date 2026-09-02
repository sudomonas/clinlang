package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// Dump renders a tree as stable, indented text for golden-file tests.
//
// The format exists to make grammar drift visible in a diff. Any change to how
// input parses shows up as a changed snapshot, which is the regression net that
// lets the parser be refactored with confidence.
//
// Spans are omitted by default because they shift whenever unrelated text
// changes, which would make every snapshot churn. DumpWithSpans includes them
// for the tests that specifically assert positions.
func Dump(n Node) string {
	var sb strings.Builder
	dump(&sb, n, 0, false)
	return sb.String()
}

// DumpWithSpans renders a tree including each node's byte range.
func DumpWithSpans(n Node) string {
	var sb strings.Builder
	dump(&sb, n, 0, true)
	return sb.String()
}

func dump(sb *strings.Builder, n Node, depth int, spans bool) {
	if n == nil || isNilNode(n) {
		return
	}

	indent := strings.Repeat("  ", depth)
	sb.WriteString(indent)
	sb.WriteString(n.Kind().String())

	if detail := describe(n); detail != "" {
		sb.WriteString(" ")
		sb.WriteString(detail)
	}
	if spans {
		s := n.Span()
		fmt.Fprintf(sb, " @%d:%d", s.Start, s.End)
	}
	sb.WriteString("\n")

	for _, child := range children(n) {
		dump(sb, child, depth+1, spans)
	}
}

// describe returns the one-line payload shown beside a node's kind.
func describe(n Node) string {
	switch v := n.(type) {
	case *Ident:
		return strconv.Quote(v.Raw)
	case *Number:
		return strconv.Quote(v.Raw)
	case *Unit:
		return strconv.Quote(v.Raw)
	case *Qualifier:
		return strconv.Quote(v.Raw)
	case *Comment:
		return strconv.Quote(v.Raw)
	case *FreeText:
		return strconv.Quote(v.Raw)
	case *Error:
		return strconv.Quote(v.Raw)
	case *Text:
		if v.Quoted {
			return strconv.Quote(v.Raw) + " quoted"
		}
		return strconv.Quote(v.Raw)
	case *Measurement:
		if v.Explicit {
			return "explicit"
		}
		return ""
	case *Duration:
		if v.Prefixed {
			return "x-prefixed"
		}
		return ""
	default:
		return ""
	}
}

// children returns a node's children in source order.
func children(n Node) []Node {
	var out []Node
	add := func(c Node) {
		if c != nil && !isNilNode(c) {
			out = append(out, c)
		}
	}

	switch v := n.(type) {
	case *Document:
		for _, p := range v.Pragmas {
			add(p)
		}
		for _, s := range v.Statements {
			add(s)
		}
		for _, c := range v.Comments {
			add(c)
		}
	case *Pragma:
		add(v.Name)
		for _, a := range v.Args {
			add(a)
		}
	case *Statement:
		add(v.Command)
		for _, a := range v.Args {
			add(a)
		}
		add(v.Trailing)
	case *Measurement:
		add(v.Key)
		add(v.Value)
		add(v.Unit)
		add(v.Qual)
	case *ConceptRef:
		add(v.Name)
		add(v.Grade)
		add(v.Duration)
	case *Quantity:
		add(v.Number)
		add(v.Unit)
	case *Duration:
		add(v.Number)
		add(v.Unit)
	case *Ratio:
		add(v.Numerator)
		add(v.Denominator)
	}
	return out
}

// isNilNode reports whether n is a typed nil pointer stored in an interface.
//
// A nil *Number assigned to a Value interface is non-nil as an interface value,
// which would otherwise print a phantom child or panic on Span().
func isNilNode(n Node) bool {
	switch v := n.(type) {
	case *Document:
		return v == nil
	case *Pragma:
		return v == nil
	case *Statement:
		return v == nil
	case *Measurement:
		return v == nil
	case *ConceptRef:
		return v == nil
	case *Quantity:
		return v == nil
	case *Duration:
		return v == nil
	case *Ident:
		return v == nil
	case *Number:
		return v == nil
	case *Ratio:
		return v == nil
	case *Text:
		return v == nil
	case *Unit:
		return v == nil
	case *Qualifier:
		return v == nil
	case *FreeText:
		return v == nil
	case *Comment:
		return v == nil
	case *Error:
		return v == nil
	}
	return false
}

// Walk calls fn for n and every descendant, depth first in source order.
// Returning false from fn skips that node's children.
func Walk(n Node, fn func(Node) bool) {
	if n == nil || isNilNode(n) {
		return
	}
	if !fn(n) {
		return
	}
	for _, c := range children(n) {
		Walk(c, fn)
	}
}

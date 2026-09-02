package backend

import (
	"fmt"
	"strings"

	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
)

// Plain renders a flat, label-per-line clinical note.
type Plain struct{}

func (Plain) Name() string        { return "plain" }
func (Plain) ContentType() string { return "text/plain; charset=utf-8" }

// Emit renders the case as a plain note.
func (b Plain) Emit(c *ir.Case, opts Options) ([]byte, []diag.Diagnostic, error) {
	r := renderer{opts: opts}
	e := c.Primary()

	var sb strings.Builder
	rule := strings.Repeat("─", 50)

	field := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&sb, "%-22s: %s\n", label, value)
		}
	}

	sb.WriteString(rule + "\nCLINICAL NOTE\n" + rule + "\n")
	if c.Subject.ID != "" {
		field("ID", c.Subject.ID)
	}
	field("Patient", r.patientLine(c.Subject))
	field("Context", e.Context)
	if e.Allergies != nil {
		field("Allergies", r.narrative(*e.Allergies))
	}
	sb.WriteString(rule + "\n")

	for _, s := range sectionOrder {
		if n, ok := e.Narrative(s.Key); ok {
			field(s.Label, r.narrative(n))
		}
	}

	present, denied := splitFindings(e.Findings)
	if len(present) > 0 {
		sb.WriteString("Symptoms:\n")
		for _, f := range present {
			fmt.Fprintf(&sb, "  ▸ %s\n", r.finding(f))
		}
	}
	if len(denied) > 0 {
		field("Denies", joinFindings(r, denied))
	}

	for _, g := range r.observationsByGroup(e) {
		fmt.Fprintf(&sb, "%s:\n", g.Label)
		for _, o := range g.Items {
			if o.Value.Kind == ir.ValueFlag {
				fmt.Fprintf(&sb, "  ▸ %s\n", r.text(o.Code))
				continue
			}
			fmt.Fprintf(&sb, "  ▸ %-20s %s\n", r.text(o.Code), r.value(o.Value))
		}
	}

	for _, sec := range ir.Present(e, c.Profiles) {
		fmt.Fprintf(&sb, "%s:\n", sec.Title)
		for _, it := range sec.Items {
			fmt.Fprintf(&sb, "  ▸ %-20s %s\n", it.Label, it.Value)
		}
	}

	if len(e.Orders) > 0 {
		sb.WriteString(rule + "\nPlan:\n")
		for _, o := range e.Orders {
			fmt.Fprintf(&sb, "  ▸ %s\n", r.order(o))
		}
	}

	sb.WriteString(rule + "\n")
	if len(e.Assessments) > 0 {
		field("Diagnosis", joinCodings(r, e.Assessments))
	}
	if len(e.Differentials) > 0 {
		field("Differential", joinCodings(r, e.Differentials))
	}
	sb.WriteString(rule + "\n")

	writeDiagnostics(&sb, opts)
	return []byte(sb.String()), nil, nil
}

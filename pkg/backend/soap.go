package backend

import (
	"fmt"
	"strings"

	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
)

// SOAP renders the Subjective / Objective / Assessment / Plan layout.
type SOAP struct{}

func (SOAP) Name() string        { return "soap" }
func (SOAP) ContentType() string { return "text/plain; charset=utf-8" }

// Emit renders the case as a SOAP note.
func (b SOAP) Emit(c *ir.Case, opts Options) ([]byte, []diag.Diagnostic, error) {
	r := renderer{opts: opts}
	e := c.Primary()

	var sb strings.Builder
	rule := strings.Repeat("─", 50)
	sub := strings.Repeat("─", 25)

	// ── Header ───────────────────────────────────────────────────────────────
	sb.WriteString(rule + "\n")
	fmt.Fprintf(&sb, "Patient: %s", r.patientLine(c.Subject))
	if c.Subject.ID != "" {
		fmt.Fprintf(&sb, "  |  ID: %s", c.Subject.ID)
	}
	sb.WriteString("\n")

	if e.Context != "" {
		fmt.Fprintf(&sb, "Context: %s\n", e.Context)
	}
	if len(c.Profiles) > 0 {
		fmt.Fprintf(&sb, "Profile: %s\n", strings.Join(c.Profiles, "+"))
	}
	if e.Allergies != nil {
		fmt.Fprintf(&sb, "\n[!] ALLERGIES: %s [!]\n",
			strings.ToUpper(r.narrative(*e.Allergies)))
	}
	sb.WriteString(rule + "\n\n")

	// ── S ────────────────────────────────────────────────────────────────────
	sb.WriteString("S — SUBJECTIVE\n" + sub + "\n")
	wrote := false
	for _, s := range sectionOrder {
		if s.Key == "pe" {
			continue // examination belongs to Objective
		}
		if n, ok := e.Narrative(s.Key); ok {
			fmt.Fprintf(&sb, "%-15s: %s\n", s.Label, r.narrative(n))
			wrote = true
		}
	}
	present, denied := splitFindings(e.Findings)
	if line := joinFindings(r, present); line != "" {
		fmt.Fprintf(&sb, "%-15s: %s\n", "Symptoms", line)
		wrote = true
	}
	if line := joinFindings(r, denied); line != "" {
		fmt.Fprintf(&sb, "%-15s: %s\n", "Denies", line)
		wrote = true
	}
	if !wrote {
		sb.WriteString("[not documented]\n")
	}
	sb.WriteString("\n")

	// ── O ────────────────────────────────────────────────────────────────────
	sb.WriteString("O — OBJECTIVE\n" + sub + "\n")
	wrote = false
	if n, ok := e.Narrative("pe"); ok {
		fmt.Fprintf(&sb, "%-15s: %s\n", "Physical Exam", r.narrative(n))
		wrote = true
	}
	for _, g := range r.observationsByGroup(e) {
		fmt.Fprintf(&sb, "%-15s: %s\n", g.Label, strings.Join(r.pairs(g.Items), " | "))
		wrote = true
	}
	for _, sec := range ir.Present(e, c.Profiles) {
		fmt.Fprintf(&sb, "%-15s: %s\n", sec.Title, joinItems(sec.Items))
		wrote = true
	}
	if !wrote {
		sb.WriteString("[not documented]\n")
	}
	sb.WriteString("\n")

	// ── A ────────────────────────────────────────────────────────────────────
	sb.WriteString("A — ASSESSMENT\n" + sub + "\n")
	if len(e.Assessments) > 0 {
		fmt.Fprintf(&sb, "%-15s: %s\n", "Diagnosis", joinCodings(r, e.Assessments))
	} else {
		fmt.Fprintf(&sb, "%-15s: [pending]\n", "Diagnosis")
	}
	if len(e.Differentials) > 0 {
		fmt.Fprintf(&sb, "%-15s: %s\n", "Differential", joinCodings(r, e.Differentials))
	}
	sb.WriteString("\n")

	// ── P ────────────────────────────────────────────────────────────────────
	sb.WriteString("P — PLAN\n" + sub + "\n")
	if len(e.Orders) == 0 {
		sb.WriteString("  [not documented]\n")
	}
	for _, o := range e.Orders {
		fmt.Fprintf(&sb, "  ▸ %s: %s\n", orderLabel(o.Kind), r.order(o))
	}

	sb.WriteString("\n" + rule + "\n")
	writeDiagnostics(&sb, opts)
	return []byte(sb.String()), nil, nil
}

// joinFindings renders a list of findings in source order.
func joinFindings(r renderer, fs []ir.Finding) string {
	if len(fs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		if f.Absent {
			parts = append(parts, r.text(f.Code))
			continue
		}
		parts = append(parts, r.finding(f))
	}
	return strings.Join(parts, "; ")
}

// joinItems renders an extension section's values on one line.
func joinItems(items []ir.Item) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.Label+" "+it.Value)
	}
	return strings.Join(parts, " | ")
}

func joinCodings(r renderer, cs []ir.Coding) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, r.text(c))
	}
	return strings.Join(parts, "; ")
}

func orderLabel(k ir.OrderKind) string {
	switch k {
	case ir.OrderMedication:
		return "Rx"
	case ir.OrderLaboratory:
		return "Lab"
	case ir.OrderImaging:
		return "Imaging"
	case ir.OrderReferral:
		return "Refer"
	case ir.OrderProcedure:
		return "Procedure"
	default:
		return "Order"
	}
}

// writeDiagnostics appends analysis messages when the caller asked for them.
func writeDiagnostics(sb *strings.Builder, opts Options) {
	if len(opts.Diagnostics) == 0 {
		return
	}
	sb.WriteString("Notes:\n")
	for _, d := range opts.Diagnostics {
		fmt.Fprintf(sb, "   • %s [%s]\n", d.Message, d.Code)
	}
}

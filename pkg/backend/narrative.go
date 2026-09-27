package backend

import (
	"strconv"
	"strings"
	"unicode"

	"clinlang/pkg/ir"
)

// renderer holds the formatting decisions every text backend shares.
//
// SOAP, plain and markdown differ in layout, not in how a dose or a finding is
// worded. Keeping the wording here is what stops the prescription block from
// being written three times — it appeared nearly verbatim in two formatters in
// the previous engine, and drifted between them.
type renderer struct {
	opts Options
}

// sectionOrder fixes the order narrative sections appear in.
//
// Explicit rather than derived from map iteration: the IR stores narratives in
// a map, and ranging over it would make section order vary between runs of the
// same input.
var sectionOrder = []struct {
	Key   string
	Label string
}{
	{"cc", "Chief Complaint"},
	{"hpi", "HPI"},
	{"pmh", "PMH"},
	{"rhx", "Past Treatment"},
	{"sh", "Social History"},
	{"fh", "Family History"},
	{"pe", "Physical Exam"},
}

// observationGroups fixes the order observation groups are printed in.
var observationGroups = []struct {
	Key   string
	Label string
}{
	{"vitals", "Vitals"},
	{"exam", "Examination"},
	{"lab", "Labs"},
	{"imaging", "Imaging"},
	{"derived", "Derived"},
}

// text returns the display form of a coding, or the verbatim input when the
// caller asked for it.
//
// Both are always present in the IR. Choosing between them is a rendering
// decision; neither replaces the other in the record.
func (r renderer) text(c ir.Coding) string {
	if r.opts.Verbatim {
		return c.Text
	}
	return c.Label()
}

func (r renderer) narrative(n ir.Narrative) string {
	if r.opts.Verbatim {
		return n.Text
	}
	return n.Label()
}

// value renders an observation value.
func (r renderer) value(v ir.Value) string {
	switch v.Kind {
	case ir.ValueQuantity:
		return r.quantity(v.Quantity)
	case ir.ValueRatio:
		return r.ratio(v.Ratio)
	case ir.ValueText:
		return v.Text
	case ir.ValueGrade:
		if v.Grade == nil {
			return ""
		}
		if v.Grade.Label != "" {
			return v.Grade.Label
		}
		return v.Grade.Symbol
	case ir.ValueFlag:
		return "requested"
	default:
		return ""
	}
}

// quantity renders a number with its unit.
//
// The canonical spelling is preferred for display, so "temp37.4c" renders as
// "37.4 C" and "wt82kgs" as "82 kg". That is notation being normalised, not a
// clinical term being substituted: the value is untouched, the unit means the
// same thing, and the IR still carries exactly what was typed. Verbatim mode
// renders the original spelling for callers who want the raw record.
func (r renderer) quantity(q *ir.Quantity) string {
	if q == nil {
		return ""
	}
	num := formatNumber(q.Value)

	unit := q.Canonical
	if r.opts.Verbatim && q.Unit != "" {
		unit = q.Unit
	}
	if unit == "" {
		unit = q.Unit
	}
	if unit == "" {
		return num
	}
	if unit == "%" {
		return num + unit
	}
	return num + " " + unit
}

func (r renderer) ratio(v *ir.Ratio) string {
	if v == nil {
		return ""
	}
	s := formatNumber(v.Numerator) + "/" + formatNumber(v.Denominator)
	if v.Unit != "" {
		s += " " + v.Unit
	}
	return s
}

func (r renderer) duration(d *ir.Duration) string {
	if d == nil {
		return ""
	}
	unit := durationWord(d.Unit, d.Value != 1)
	return formatNumber(d.Value) + " " + unit
}

// splitFindings separates reported symptoms from explicit denials.
//
// They are rendered on separate lines because they are different statements: a
// list of positives reads as the presentation, and a list of denials reads as
// the review of systems. Interleaving them makes a reader check each entry for
// a "(denied)" suffix.
func splitFindings(fs []ir.Finding) (present, denied []ir.Finding) {
	for _, f := range fs {
		if f.Absent {
			denied = append(denied, f)
		} else {
			present = append(present, f)
		}
	}
	return present, denied
}

// finding renders a symptom or examination finding.
//
// An absent finding is stated as denied. The engine only ever knows a finding
// is absent because the clinician said so, never from its omission.
func (r renderer) finding(f ir.Finding) string {
	name := r.text(f.Code)
	if f.Absent {
		return name + " (denied)"
	}

	var parts []string
	if f.Grade != nil && f.Grade.Label != "" {
		parts = append(parts, f.Grade.Label)
	}
	if f.Duration != nil {
		parts = append(parts, r.duration(f.Duration))
	}
	if len(parts) == 0 {
		return name
	}
	return name + " (" + strings.Join(parts, ", ") + ")"
}

// order renders a medication or investigation order.
//
// This is the single implementation. Frequency and as-needed-ness are rendered
// as the two independent facts they are, so a four-times-daily as-needed order
// says both — the previous engine kept them in one field and printed only
// whichever was assigned last.
func (r renderer) order(o ir.Order) string {
	var sb strings.Builder
	sb.WriteString(titleCase(r.text(o.Code)))

	if o.Dose != nil {
		sb.WriteString(" ")
		sb.WriteString(r.quantity(o.Dose))
	}
	if o.Route != nil {
		sb.WriteString(" ")
		sb.WriteString(r.text(*o.Route))
	}

	var tail []string
	if o.Timing != nil && o.Timing.Display != "" {
		tail = append(tail, lowerFirst(o.Timing.Display))
	}
	if o.AsNeeded {
		tail = append(tail, "as needed")
	}
	if o.Priority == "stat" && o.Timing == nil {
		tail = append(tail, "immediately")
	}
	if len(tail) > 0 {
		sb.WriteString(", ")
		sb.WriteString(strings.Join(tail, ", "))
	}

	if o.Duration != nil {
		sb.WriteString(" for ")
		sb.WriteString(r.duration(o.Duration))
	}
	return sb.String()
}

// patientLine renders the demographic summary.
func (r renderer) patientLine(s ir.Subject, unit string) string {
	age := "Age?"
	if s.Age != nil {
		age = formatNumber(s.Age.Value) + s.Age.Unit
	}
	sex := "?"
	if s.Sex != nil {
		sex = s.Sex.Text
	}

	line := age + "/" + sex
	if s.Weight != nil {
		line += " | Wt: " + r.quantity(s.Weight)
	}
	if s.Height != nil {
		line += " | Ht: " + r.quantity(s.Height)
	}
	if s.Bed != "" {
		line += " | Bed: " + s.Bed
	}
	if unit != "" {
		line += " | " + unit
	}
	return line
}

// observationsByGroup collects observations in a fixed group order, preserving
// source order inside each group.
func (r renderer) observationsByGroup(e *ir.Encounter) []groupedObservations {
	var out []groupedObservations

	seen := map[string]bool{}
	for _, g := range observationGroups {
		if g.Key == "derived" && r.opts.HideDerived {
			seen[g.Key] = true
			continue
		}
		seen[g.Key] = true
		if obs := e.ObservationsIn(g.Key); len(obs) > 0 {
			out = append(out, groupedObservations{Label: g.Label, Items: obs})
		}
	}

	// Groups from ad-hoc commands, in first-appearance order so the result
	// does not depend on map iteration.
	var extra []string
	for _, o := range e.Observations {
		if seen[o.Group] {
			continue
		}
		seen[o.Group] = true
		extra = append(extra, o.Group)
	}
	for _, g := range extra {
		out = append(out, groupedObservations{
			Label: strings.ToUpper(g),
			Items: e.ObservationsIn(g),
		})
	}
	return out
}

type groupedObservations struct {
	Label string
	Items []ir.Observation
}

// pairs renders a group of observations as "KEY value" strings.
func (r renderer) pairs(obs []ir.Observation) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		label := r.text(o.Code)
		v := r.value(o.Value)
		if o.Value.Kind == ir.ValueFlag {
			out = append(out, label)
			continue
		}
		out = append(out, label+" "+v)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Formatting helpers
// ─────────────────────────────────────────────────────────────────────────────

// formatNumber renders a float without a trailing ".0".
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

var durationWords = map[string]string{
	"s": "second", "min": "minute", "h": "hour", "d": "day",
	"w": "week", "wk": "week", "mo": "month", "y": "year",
}

func durationWord(unit string, plural bool) string {
	w, ok := durationWords[unit]
	if !ok {
		return unit
	}
	if plural {
		return w + "s"
	}
	return w
}

// titleCase upper-cases the first rune.
//
// Rune-aware, not byte-aware: the previous implementation indexed s[0], which
// corrupts any name beginning with a multi-byte character.
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

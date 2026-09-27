// Package ir defines ClinLang's normalized clinical model.
//
// The AST records what the clinician typed; the IR records what it means. The
// semantic layer reads one and produces the other, and every backend — SOAP,
// markdown, plain text, JSON — reads only the IR. That separation is the point
// of the rewrite: the previous engine parsed straight into the struct that was
// simultaneously the syntax tree, the semantic model, the parse context, the
// diagnostic sink and the JSON DTO, so the shape the SOAP formatter wanted
// dictated what the parser was allowed to produce.
//
// # Provenance
//
// Every node carries an Origin span pointing back at the source text it came
// from. Nothing here is anonymous: a rendered value can always be traced to the
// characters that produced it, which is what makes precise editor diagnostics
// and audit trails possible.
//
// # Verbatim text is never lost
//
// Coding always populates Text with exactly what was typed. Display holds the
// expanded human form when the lexicon has one. Backends render Display and
// fall back to Text; neither ever replaces the original. The engine does not
// silently substitute clinical terms.
//
// # Coded terminology
//
// Coding carries System and Code fields that are always empty today. ClinLang
// binds no terminology: SNOMED CT, LOINC and RxNorm are separately licensed and
// cannot be embedded in an MIT-licensed binary. The fields exist so that
// attaching a terminology provider later is additive rather than a remodelling,
// and so backends can already distinguish "uncoded" from "absent".
//
// # What the IR deliberately cannot express
//
// There is no severity, no priority, no normal range, no interpretation flag
// and no ordering by clinical importance. ClinLang records and renders; it does
// not evaluate. Those absences are load-bearing and are asserted by tests.
package ir

import (
	"clinlang/pkg/source"
)

// Coding is a clinical concept: what was typed, what it identifies, what it
// displays as, and — when a terminology is ever attached — what it codes to.
type Coding struct {
	// Text is the clinician's input, verbatim and never modified.
	Text string `json:"text"`

	// ConceptID is ClinLang's own stable identifier for the thing named, such
	// as "drug.metformin" or "measurement.hb". Empty when the lexicon did not
	// recognise the input, which is normal and not an error: unrecognised text
	// still renders, it simply carries no identity.
	//
	// This is the join key the rest of the system works in. Without it,
	// "metformin", "met" and "glucophage" are three unrelated strings, a plugin
	// has nothing stable to attach data to, and an external code has nowhere to
	// bind. With it, all three resolve to one identity and everything
	// downstream — completions, plugin extensions, future terminology packs —
	// refers to that rather than to spelling.
	ConceptID string `json:"concept_id,omitempty"`

	// Display is the expanded human-readable form, empty when the lexicon has
	// no expansion. Backends prefer it and fall back to Text.
	Display string `json:"display,omitempty"`

	// System and Code are always empty. See the package documentation.
	System string `json:"system,omitempty"`
	Code   string `json:"code,omitempty"`

	Origin source.Span `json:"origin"`
}

// Resolved reports whether the lexicon recognised this concept.
func (c Coding) Resolved() bool { return c.ConceptID != "" }

// Label returns Display when present, otherwise Text.
func (c Coding) Label() string {
	if c.Display != "" {
		return c.Display
	}
	return c.Text
}

// IsZero reports whether the coding carries no content.
func (c Coding) IsZero() bool { return c.Text == "" && c.Display == "" }

// Quantity is a number with a unit.
//
// Unit is what the clinician wrote; Canonical is the lexicon's normalized
// spelling of that same unit. The value is never converted: a weight entered in
// pounds is recorded in pounds. Conversion is available to a caller that asks,
// but the record keeps what was written.
type Quantity struct {
	Value     float64     `json:"value"`
	Raw       string      `json:"raw"`                 // the digits as typed, e.g. "07"
	Unit      string      `json:"unit,omitempty"`      // as typed, e.g. "kg"
	Canonical string      `json:"canonical,omitempty"` // lexicon form, e.g. "kg"
	Dimension string      `json:"dimension,omitempty"` // mass, length, time…
	Origin    source.Span `json:"origin"`
}

// HasUnit reports whether a unit was recorded.
func (q Quantity) HasUnit() bool { return q.Unit != "" }

// Ratio is a paired value such as a blood pressure.
//
// Structured at parse time rather than stored as the string "140/90" and
// re-parsed by a later layer, which is what the previous engine did.
type Ratio struct {
	Numerator   float64     `json:"numerator"`
	Denominator float64     `json:"denominator"`
	Unit        string      `json:"unit,omitempty"`
	Origin      source.Span `json:"origin"`
}

// Duration is an elapsed time.
type Duration struct {
	Value  float64     `json:"value"`
	Unit   string      `json:"unit"`          // canonical short form: h, d, wk, mo, y
	Raw    string      `json:"raw,omitempty"` // as typed, e.g. "3d"
	Origin source.Span `json:"origin"`
}

// Time is a timestamp exactly as written.
//
// Stored as components rather than a time.Time, deliberately. A note saying
// "2026-08-01T14:20" means the wall clock where it was written; turning that
// into an instant would require inventing a timezone the author never gave.
// Raw is kept so a note can render the string its author typed.
//
// HasTime distinguishes a date from a date-and-time, so an encounter recorded
// as a day is not rendered as though it happened at midnight.
type Time struct {
	Raw     string      `json:"raw"`
	Year    int         `json:"year"`
	Month   int         `json:"month"`
	Day     int         `json:"day"`
	Hour    int         `json:"hour,omitempty"`
	Minute  int         `json:"minute,omitempty"`
	Second  int         `json:"second,omitempty"`
	HasTime bool        `json:"has_time,omitempty"`
	Origin  source.Span `json:"origin"`
}

// ValueKind discriminates the payload of a Value.
type ValueKind uint8

const (
	ValueNone ValueKind = iota
	ValueQuantity
	ValueRatio
	ValueText
	ValueFlag // presence only: an investigation ordered, a finding recorded
	ValueGrade
)

// Value is the payload of an observation or finding.
//
// A tagged union rather than an `any`: the previous engine's SpecialtyData was
// an untyped any, and every consumer type-asserted it independently. When an
// assertion failed the handler silently did nothing, so a plugin bug became
// silent data loss.
type Value struct {
	Kind     ValueKind `json:"kind"`
	Quantity *Quantity `json:"quantity,omitempty"`
	Ratio    *Ratio    `json:"ratio,omitempty"`
	Text     string    `json:"text,omitempty"`
	Grade    *Grade    `json:"grade,omitempty"`
}

// Grade is an intensity or serology marker such as +++ or --.
//
// Symbol keeps the typed run and Label the lexicon's wording. Grade carries no
// ordering and no numeric rank: ClinLang does not rate how bad something is,
// and sorting findings by grade would be interpretation.
type Grade struct {
	Symbol string      `json:"symbol"`
	Label  string      `json:"label,omitempty"`
	Origin source.Span `json:"origin"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Clinical statements
// ─────────────────────────────────────────────────────────────────────────────

// Observation is a recorded measurement or finding.
//
// Vitals and labs are one type. They were separate in the previous model only
// because the SOAP layout printed them in different blocks, which is a
// presentation concern leaking into the data model.
//
// Derived marks a value the engine computed rather than one the clinician
// recorded — BMI and BSA. The previous model made them indistinguishable from
// measured values.
type Observation struct {
	Code      Coding      `json:"code"`
	Value     Value       `json:"value"`
	Group     string      `json:"group,omitempty"` // vitals, lab, imaging
	Derived   bool        `json:"derived,omitempty"`
	DerivedAs string      `json:"derived_as,omitempty"` // e.g. "weight / height²"
	Origin    source.Span `json:"origin"`
}

// Finding is a symptom or examination finding.
//
// Absent records a pertinent negative: the clinician stated the symptom was not
// present, as in "ros fever cough". That is a recorded fact, not an inference —
// the engine never concludes a symptom is absent from its omission, only from
// its explicit denial.
//
// Absence needs its own field because the qualifier notation cannot express it.
// A trailing "-" means improving, which is a trend in something the patient
// does have; "denied" and "getting better" are different clinical statements
// and conflating them would misreport the history.
type Finding struct {
	Code     Coding      `json:"code"`
	Grade    *Grade      `json:"grade,omitempty"`
	Duration *Duration   `json:"duration,omitempty"`
	Absent   bool        `json:"absent,omitempty"`
	Origin   source.Span `json:"origin"`
}

// OrderKind classifies an order.
type OrderKind uint8

const (
	OrderUnknown OrderKind = iota
	OrderMedication
	OrderLaboratory
	OrderImaging
	OrderReferral
	OrderProcedure
)

func (k OrderKind) String() string {
	switch k {
	case OrderMedication:
		return "medication"
	case OrderLaboratory:
		return "laboratory"
	case OrderImaging:
		return "imaging"
	case OrderReferral:
		return "referral"
	case OrderProcedure:
		return "procedure"
	default:
		return "unknown"
	}
}

// MarshalText renders the kind by name in JSON.
func (k OrderKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText parses a kind name.
//
// Every type that marshals by name needs this, or the engine's own output
// cannot be read back — which would silently break any integration that
// round-trips a case through storage.
func (k *OrderKind) UnmarshalText(b []byte) error {
	switch string(b) {
	case "medication":
		*k = OrderMedication
	case "laboratory":
		*k = OrderLaboratory
	case "imaging":
		*k = OrderImaging
	case "referral":
		*k = OrderReferral
	case "procedure":
		*k = OrderProcedure
	default:
		*k = OrderUnknown
	}
	return nil
}

// Timing is a dosing schedule.
//
// Count administrations per Period of PeriodUnit: "tds" is 3 per 1 d. This is
// the structured fact the previous engine only ever held as the English string
// "Three times daily" in a Go map.
type Timing struct {
	Code       string      `json:"code,omitempty"`    // TDS, BD, Q6H
	Display    string      `json:"display,omitempty"` // Three times daily
	Count      int         `json:"count,omitempty"`
	Period     float64     `json:"period,omitempty"`
	PeriodUnit string      `json:"period_unit,omitempty"`
	When       []string    `json:"when,omitempty"`
	Origin     source.Span `json:"origin"`
}

// Order generalizes prescriptions, investigations, imaging and referrals.
type Order struct {
	Kind OrderKind `json:"kind"`
	Code Coding    `json:"code"`

	Dose     *Quantity `json:"dose,omitempty"`
	Route    *Coding   `json:"route,omitempty"`
	Timing   *Timing   `json:"timing,omitempty"`
	Duration *Duration `json:"duration,omitempty"`

	// AsNeeded is orthogonal to Timing, not a value of it.
	//
	// This is the field that fixes the PRN defect. "rx paracetamol 1g qds prn"
	// previously set Frequency to QDS and then overwrote it with PRN, so the
	// four-times-daily ceiling never appeared in the note. Four times daily and
	// as-needed are two independent facts and are stored as two fields.
	AsNeeded bool   `json:"as_needed,omitempty"`
	Priority string `json:"priority,omitempty"` // "stat"

	Origin source.Span `json:"origin"`
}

// Narrative is a free-text field, kept verbatim with its expanded form
// alongside.
type Narrative struct {
	Text    string      `json:"text"`              // exactly as typed
	Display string      `json:"display,omitempty"` // abbreviations expanded
	Origin  source.Span `json:"origin"`
}

// Label returns Display when present, otherwise Text.
func (n Narrative) Label() string {
	if n.Display != "" {
		return n.Display
	}
	return n.Text
}

// ─────────────────────────────────────────────────────────────────────────────
// Subject and encounter
// ─────────────────────────────────────────────────────────────────────────────

// Subject is the patient.
//
// Age is a Quantity rather than an int plus a unit string, so "6mo" and "34y"
// are the same shape. Sex is recorded as typed, with no inferred default: the
// previous engine set it as a side effect of stripping a trailing capital from
// any unrecognised token.
type Subject struct {
	ID     string      `json:"id,omitempty"`
	Age    *Quantity   `json:"age,omitempty"`
	Sex    *Coding     `json:"sex,omitempty"`
	Weight *Quantity   `json:"weight,omitempty"`
	Height *Quantity   `json:"height,omitempty"`
	Bed    string      `json:"bed,omitempty"`
	Origin source.Span `json:"origin"`
}

// Encounter is one clinical contact.
//
// Today a document produces exactly one, which reproduces the previous model's
// single-case semantics. The container exists so multi-encounter documents,
// progress notes and discharge summaries do not require reshaping the model.
type Encounter struct {
	// When the encounter took place, as written. Nil for a note that records
	// no timestamp, which is the ordinary single-visit case.
	When *Time `json:"when,omitempty"`

	// Type is the kind of contact: opd, er, admission, ward, telecon. Recorded
	// verbatim and given a display name by vocabulary when one exists, like any
	// other concept. The language defines no fixed set, because the set differs
	// by health system and is not the language's to decide.
	Type *Coding `json:"type,omitempty"`

	// Unit is the ward, clinic or location.
	//
	// It belongs to the encounter, not the patient: a person is not "in the
	// ICU", a stay is. It moved here from Subject for that reason.
	Unit string `json:"unit,omitempty"`

	// Context is a free-text label such as "Day 3 post-op".
	Context string `json:"context,omitempty"`

	Complaints    []Finding     `json:"complaints,omitempty"`
	Findings      []Finding     `json:"findings,omitempty"`
	Observations  []Observation `json:"observations,omitempty"`
	Assessments   []Coding      `json:"assessments,omitempty"`
	Differentials []Coding      `json:"differentials,omitempty"`
	Orders        []Order       `json:"orders,omitempty"`

	// Narratives is keyed by section: cc, hpi, pmh, sh, fh, pe.
	Narratives map[string]Narrative `json:"narratives,omitempty"`

	Allergies *Narrative `json:"allergies,omitempty"`

	// Extensions holds profile-contributed data, keyed by profile name.
	// Read it through the Extension helper rather than asserting directly.
	Extensions map[string]any `json:"extensions,omitempty"`

	Origin source.Span `json:"origin"`
}

// ObservationsIn returns the observations belonging to a group.
func (e *Encounter) ObservationsIn(group string) []Observation {
	var out []Observation
	for _, o := range e.Observations {
		if o.Group == group {
			out = append(out, o)
		}
	}
	return out
}

// OrdersOf returns the orders of a given kind.
func (e *Encounter) OrdersOf(kind OrderKind) []Order {
	var out []Order
	for _, o := range e.Orders {
		if o.Kind == kind {
			out = append(out, o)
		}
	}
	return out
}

// Narrative returns a named narrative section.
func (e *Encounter) Narrative(section string) (Narrative, bool) {
	n, ok := e.Narratives[section]
	return n, ok
}

// Case is the root of the IR.
type Case struct {
	LangVersion string      `json:"lang_version,omitempty"`
	Profiles    []string    `json:"profiles,omitempty"`
	Subject     Subject     `json:"subject"`
	Encounters  []Encounter `json:"encounters"`
}

// IsEmpty reports whether the encounter records nothing.
//
// Used to decide whether an "enc" statement fills the implicit first encounter
// or starts a new one, so a document opening with "enc" does not leave an empty
// encounter in front of it.
func (e *Encounter) IsEmpty() bool {
	return e.When == nil && (e.Type == nil || e.Type.IsZero()) && e.Unit == "" && e.Context == "" &&
		len(e.Complaints) == 0 && len(e.Findings) == 0 && len(e.Observations) == 0 &&
		len(e.Assessments) == 0 && len(e.Differentials) == 0 && len(e.Orders) == 0 &&
		len(e.Narratives) == 0 && e.Allergies == nil
}

// Primary returns the first encounter, creating one if the case is empty.
//
// Every backend needs an encounter to render, and a document with no
// recognisable statements should still produce an empty note rather than
// requiring nil checks at each call site.
func (c *Case) Primary() *Encounter {
	if len(c.Encounters) == 0 {
		c.Encounters = append(c.Encounters, Encounter{
			Narratives: map[string]Narrative{},
		})
	}
	return &c.Encounters[0]
}

// Extension retrieves typed profile data from an encounter.
//
// This replaces the previous engine's `SpecialtyData any` with its repeated
// unchecked assertions. Two defects came from that design: a failed assertion
// silently discarded the write, and multi-profile documents stored a
// map[string]any so every single-type assertion failed at once, making all five
// OB/GYN commands no-op without a word of warning.
func Extension[T any](e *Encounter, profile string) (T, bool) {
	var zero T
	if e == nil || e.Extensions == nil {
		return zero, false
	}
	v, ok := e.Extensions[profile]
	if !ok {
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

// Item is one labelled value in a rendered extension section.
type Item struct {
	Label string
	Value string
}

// Section is a titled group of extension values for display.
type Section struct {
	Title string
	Items []Item
}

// Presentable is implemented by profile extension data that wants to appear in
// rendered notes.
//
// The contract lives here, in the model, rather than in the plugin package, so
// backends can render specialty data without importing the plugin machinery —
// and so each profile decides how its own data reads. The previous engine
// JSON-dumped the payload into the note and stripped the braces, which meant a
// plugin had no say in its own presentation and the output carried field names
// rather than clinical labels.
//
// Ordering is the plugin's own, and backends preserve it.
type Presentable interface {
	Sections() []Section
}

// Present returns the display sections for every profile on the encounter, in
// profile registration order as recorded on the case.
//
// Data that does not implement Presentable contributes nothing, which is the
// correct default: a profile storing internal state should not have it leak
// into a clinical note.
func Present(e *Encounter, profiles []string) []Section {
	if e == nil {
		return nil
	}
	var out []Section
	for _, name := range profiles {
		p, ok := e.Extensions[name].(Presentable)
		if !ok {
			continue
		}
		for _, s := range p.Sections() {
			if len(s.Items) > 0 {
				out = append(out, s)
			}
		}
	}
	return out
}

// SetExtension stores profile data on an encounter.
func SetExtension(e *Encounter, profile string, data any) {
	if e == nil {
		return
	}
	if e.Extensions == nil {
		e.Extensions = make(map[string]any, 1)
	}
	e.Extensions[profile] = data
}

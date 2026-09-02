// Package obgyn provides the obstetrics and gynaecology specialty plugin.
//
// It is also the reference implementation of the plugin contract, and exercises
// every capability a plugin can offer: contributed commands, a declared grammar
// shape for them, inline tokens extending core commands, typed extension data,
// and editor completions.
//
// Usage:
//
//	@profile obgyn
//	pt 28F wt65 ga:34w           ← ga: extends the core pt command
//	vitals bp120/75 fhr:142      ← fhr: extends the core vitals command
//	lmp 2025-06-15
//	edd 2026-03-22
//	gpal G2P1A0L1
//	fhs 142 regular, reactive
//	ctx 4 in 10min lasting 45s
package obgyn

import (
	"strings"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/parser"
	"clinlang/pkg/plugin"
	"clinlang/pkg/sema"
)

// Data is the plugin's typed extension payload.
//
// It is retrieved with ir.Extension[*Data], which either returns it or reports
// that it is absent. The previous design stored an untyped any and every
// handler asserted it independently; a failed assertion silently discarded the
// write, and under a combined profile every assertion failed at once, so all
// five commands became no-ops without a word of warning.
type Data struct {
	// Gestational age, set by the inline ga: token on pt.
	GA *ir.Quantity `json:"gestational_age,omitempty"`

	// Fetal heart rate, set by the inline fhr: token on vitals.
	FHR *ir.Quantity `json:"fetal_heart_rate,omitempty"`

	LMP  *ir.Narrative `json:"lmp,omitempty"`
	EDD  *ir.Narrative `json:"edd,omitempty"`
	GPAL *ir.Narrative `json:"gpal,omitempty"`
	FHS  *ir.Narrative `json:"fetal_heart_sounds,omitempty"`
	CTX  *ir.Narrative `json:"contractions,omitempty"`
}

// Plugin is the obstetrics specialty.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "obgyn",
		Version:     "2.0.0",
		EngineRange: ">=2.0.0 <3.0.0",
		Description: "Obstetrics and gynaecology",
		Commands: map[string]string{
			"lmp":           "Last menstrual period (lmp 2025-06-15)",
			"edd":           "Estimated date of delivery (edd 2026-03-22)",
			"gpal":          "Gravida/para/abortus/living (gpal G2P1A0L1)",
			"fhs":           "Fetal heart sounds (fhs 142 regular reactive)",
			"ctx":           "Contractions (ctx 4 in 10min lasting 45s)",
			"[pt] ga:":      "Gestational age inside pt (pt 28F ga:34w)",
			"[vitals] fhr:": "Fetal heart rate inside vitals (vitals fhr:142)",
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Grammar
// ─────────────────────────────────────────────────────────────────────────────

// Shapes implements plugin.GrammarProvider.
//
// All five commands take prose. Declaring that matters: without it the parser
// tokenizes "ctx 4 in 10min lasting 45s" into arguments and analysis reports
// one unrecognised token per word, which is exactly what the corpus showed
// before this plugin was migrated.
func (p *Plugin) Shapes() map[string]parser.Shape {
	return map[string]parser.Shape{
		"lmp":  parser.FreeText,
		"edd":  parser.FreeText,
		"gpal": parser.FreeText,
		"fhs":  parser.FreeText,
		"ctx":  parser.FreeText,
	}
}

// Keys implements plugin.GrammarProvider.
//
// None needed: ga and fhr contain no digits, so the ordinary splitting rule
// handles them, and both are normally written with an explicit colon anyway.
func (p *Plugin) Keys() []string { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// Semantics
// ─────────────────────────────────────────────────────────────────────────────

// Name implements sema.Profile.
func (p *Plugin) Name() string { return "obgyn" }

// NewData implements sema.Profile.
func (p *Plugin) NewData() any { return &Data{} }

// Command implements sema.Profile, handling this plugin's own commands.
func (p *Plugin) Command(cmd string, st *ast.Statement, ctx *sema.Context) bool {
	data, ok := p.data(ctx)
	if !ok {
		return false
	}

	field := map[string]**ir.Narrative{
		"lmp":  &data.LMP,
		"edd":  &data.EDD,
		"gpal": &data.GPAL,
		"fhs":  &data.FHS,
		"ctx":  &data.CTX,
	}[strings.ToLower(cmd)]
	if field == nil {
		return false
	}

	if st.Trailing == nil {
		ctx.Warn(diag.EmptyCommand, st, "'"+cmd+"' has no value")
		return true // claimed, but empty
	}
	*field = &ir.Narrative{Text: st.Trailing.Raw, Origin: st.Trailing.Sp}
	return true
}

// Argument implements sema.Profile, handling inline tokens on core commands.
//
// The core pt and vitals parsers know nothing about obstetrics. They offer any
// argument they cannot interpret to the active profiles, which is how a
// specialty extends a core command without the core being edited.
func (p *Plugin) Argument(cmd string, arg ast.Argument, ctx *sema.Context) bool {
	data, ok := p.data(ctx)
	if !ok {
		return false
	}

	m, isMeasurement := arg.(*ast.Measurement)
	if !isMeasurement || m.Key == nil {
		return false
	}
	key := strings.ToLower(m.Key.Raw)

	switch {
	case cmd == "pt" && key == "ga":
		q, ok := quantity(m, "w")
		if !ok {
			ctx.Warn(diag.UnexpectedToken, m, "gestational age needs a number of weeks")
			return true
		}
		data.GA = q
		return true

	case cmd == "vitals" && key == "fhr":
		q, ok := quantity(m, "bpm")
		if !ok {
			ctx.Warn(diag.UnexpectedToken, m, "fetal heart rate needs a number")
			return true
		}
		data.FHR = q
		return true
	}
	return false
}

// data retrieves this plugin's typed payload for the current encounter.
func (p *Plugin) data(ctx *sema.Context) (*Data, bool) {
	return ir.Extension[*Data](ctx.Encounter, "obgyn")
}

// quantity reads a numeric measurement value, applying a default unit.
func quantity(m *ast.Measurement, defaultUnit string) (*ir.Quantity, bool) {
	num, ok := m.Value.(*ast.Number)
	if !ok {
		return nil, false
	}
	unit := defaultUnit
	if m.Unit != nil {
		unit = m.Unit.Raw
	}
	return &ir.Quantity{
		Value: num.Val, Raw: num.Raw,
		Unit: unit, Canonical: defaultUnit,
		Origin: m.Sp,
	}, true
}

// ─────────────────────────────────────────────────────────────────────────────
// Completions
// ─────────────────────────────────────────────────────────────────────────────

// Completions implements plugin.CompletionProvider.
//
// The plugin owns its own editor vocabulary, so there is no separate hardcoded
// completion table to drift out of step with it.
func (p *Plugin) Completions() []plugin.Completion {
	return []plugin.Completion{
		{Text: "lmp", Detail: "Last menstrual period", Kind: "command"},
		{Text: "edd", Detail: "Estimated date of delivery", Kind: "command"},
		{Text: "gpal", Detail: "Gravida/para/abortus/living", Kind: "command"},
		{Text: "fhs", Detail: "Fetal heart sounds", Kind: "command"},
		{Text: "ctx", Detail: "Contractions", Kind: "command"},
		{Text: "ga:", Detail: "Gestational age in weeks", Kind: "token"},
		{Text: "fhr:", Detail: "Fetal heart rate", Kind: "token"},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Presentation
// ─────────────────────────────────────────────────────────────────────────────

// Sections implements ir.Presentable.
//
// The plugin decides how its own data reads in a note. The previous engine
// JSON-dumped the payload and stripped the braces, so notes carried field names
// like "ga_weeks" instead of clinical labels and the plugin had no say.
func (d *Data) Sections() []ir.Section {
	s := ir.Section{Title: "Obstetric"}

	add := func(label, value string) {
		if value != "" {
			s.Items = append(s.Items, ir.Item{Label: label, Value: value})
		}
	}
	quantity := func(q *ir.Quantity) string {
		if q == nil {
			return ""
		}
		unit := q.Canonical
		if unit == "" {
			unit = q.Unit
		}
		return q.Raw + unit
	}
	narrative := func(n *ir.Narrative) string {
		if n == nil {
			return ""
		}
		return n.Text
	}

	add("Gestational age", quantity(d.GA))
	add("LMP", narrative(d.LMP))
	add("EDD", narrative(d.EDD))
	add("GPAL", narrative(d.GPAL))
	add("Fetal heart rate", quantity(d.FHR))
	add("Fetal heart sounds", narrative(d.FHS))
	add("Contractions", narrative(d.CTX))

	return []ir.Section{s}
}

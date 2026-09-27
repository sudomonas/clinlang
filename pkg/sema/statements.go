package sema

import (
	"math"
	"strings"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/lexicon"
	"clinlang/pkg/source"
)

// narrativeSections maps a prose command to the IR narrative key it fills.
var narrativeSections = map[string]string{
	"cc":  "cc",
	"hpi": "hpi",
	"pmh": "pmh",
	"sh":  "sh",
	"fh":  "fh",
	"rhx": "rhx",
}

func (a *analyzer) statement(st *ast.Statement) {
	if st.Command == nil {
		return
	}
	cmd := strings.ToLower(st.Command.Raw)

	// Profiles are additive only and never shadow a core command, so core
	// dispatch is attempted first.
	switch cmd {
	case "pt":
		a.markOnce(cmd, st.Sp)
		a.patient(st)
		return
	case "vitals":
		a.observations(st, "vitals")
		return
	case "sx":
		a.symptoms(st)
		return
	case "ros":
		a.reviewOfSystems(st)
		return
	case "pe", "oe", "exam":
		a.examination(st)
		return
	case "lab", "labs":
		a.observations(st, "lab")
		return
	case "rad":
		a.observations(st, "imaging")
		return
	case "ix":
		a.investigations(st)
		return
	case "rx":
		a.prescription(st)
		return
	case "dx":
		a.markOnce(cmd, st.Sp)
		a.enc.Assessments = append(a.enc.Assessments, a.codingFromText(st.Trailing))
		return
	case "ddx":
		a.enc.Differentials = append(a.enc.Differentials, a.codingFromText(st.Trailing))
		return
	case "day":
		if st.Trailing != nil {
			a.enc.Context = st.Trailing.Raw
		}
		return
	case "alg", "allergy":
		if st.Trailing != nil {
			n := a.narrative(st.Trailing)
			a.enc.Allergies = &n
		}
		return
	case "id":
		if st.Trailing != nil {
			a.cs.Subject.ID = strings.Fields(st.Trailing.Raw)[0]
		}
		return
	}

	if section, ok := narrativeSections[cmd]; ok {
		a.markOnce(cmd, st.Sp)
		if st.Trailing != nil {
			a.enc.Narratives[section] = a.narrative(st.Trailing)
		} else {
			a.bag.Warn(diag.EmptyCommand, st.Sp, "'"+cmd+"' has no text")
		}
		return
	}

	if a.offerStatement(cmd, st) {
		return
	}

	// An unrecognised command still records its data, grouped under its own
	// name. Ad-hoc fields are a deliberate feature, so this is a hint rather
	// than a warning: nothing is wrong, it is simply not a known command.
	a.bag.Hint(diag.UnknownCommand, st.Command.Sp,
		"'"+cmd+"' is not a known command; its values are recorded under that name")
	a.observations(st, cmd)
}

// ─────────────────────────────────────────────────────────────────────────────
// pt
// ─────────────────────────────────────────────────────────────────────────────

func (a *analyzer) patient(st *ast.Statement) {
	subj := &a.cs.Subject
	subj.Origin = st.Sp

	for _, arg := range st.Args {
		switch v := arg.(type) {
		case *ast.Quantity:
			a.patientQuantity(subj, v)

		case *ast.Measurement:
			a.patientMeasurement(subj, v)

		case *ast.ConceptRef:
			if sex, ok := sexFromToken(v.Name.Raw); ok {
				subj.Sex = &ir.Coding{
					Text: v.Name.Raw, Display: sexDisplay(sex),
					ConceptID: "sex." + strings.ToLower(sex), Origin: v.Sp,
				}
				continue
			}
			if a.offerArgument("pt", arg) {
				continue
			}
			a.unrecognized("pt", arg)

		default:
			if !a.offerArgument("pt", arg) {
				a.unrecognized("pt", arg)
			}
		}
	}

	if subj.Age == nil {
		a.bag.Hint(diag.MissingDemographic, st.Sp, "patient age not stated")
	}
	if subj.Sex == nil {
		a.bag.Hint(diag.MissingDemographic, st.Sp, "patient sex not stated")
	}
}

// patientQuantity reads a bare number in a pt statement as an age, with an
// optional sex suffix.
//
// The suffix rule is case-sensitive and that is deliberate: lowercase "m" is
// the duration alias for month, so "pt 6m" is a six-month-old and "pt 34M" is
// a 34-year-old male. The previous engine stripped a trailing capital M or F
// from every token that reached the end of its handler chain, so any
// unrecognised token ending in those letters silently set the patient's sex.
func (a *analyzer) patientQuantity(subj *ir.Subject, q *ast.Quantity) {
	unit := ""
	if q.Unit != nil {
		unit = q.Unit.Raw
	}

	durUnit, sex, ok := splitAgeUnit(a.lex, unit)
	if !ok {
		if !a.offerArgument("pt", q) {
			a.unrecognized("pt", q)
		}
		return
	}

	if sex != "" {
		subj.Sex = &ir.Coding{
			Text: sex, Display: sexDisplay(sex),
			ConceptID: "sex." + strings.ToLower(sex), Origin: q.Sp,
		}
	}

	canonical := "y"
	if durUnit != "" {
		if du, ok := a.lex.DurationUnit(durUnit); ok {
			canonical = du.Short
		} else {
			canonical = durUnit
		}
	}
	subj.Age = &ir.Quantity{
		Value: q.Number.Val, Raw: q.Number.Raw,
		Unit: canonical, Canonical: canonical, Dimension: "time",
		Origin: q.Sp,
	}
}

func (a *analyzer) patientMeasurement(subj *ir.Subject, m *ast.Measurement) {
	key := strings.ToLower(m.Key.Raw)

	switch key {
	case "wt":
		subj.Weight = a.quantity(m, "mass", "kg")
	case "ht":
		subj.Height = a.quantity(m, "length", "cm")
	case "bed":
		subj.Bed = valueText(m.Value)
	case "unit":
		a.enc.Unit = valueText(m.Value)
	case "age":
		if q := a.quantity(m, "time", "y"); q != nil {
			subj.Age = q
		}
	default:
		if a.offerArgument("pt", m) {
			return
		}
		a.unrecognized("pt", m)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// vitals, labs, imaging
// ─────────────────────────────────────────────────────────────────────────────

// observations converts measurement-shaped arguments into IR observations.
//
// Vitals and labs share this path because they are the same kind of thing. The
// previous model kept them in separate fields only because the SOAP template
// printed them in different blocks.
func (a *analyzer) observations(st *ast.Statement, group string) {
	cmd := strings.ToLower(st.Command.Raw)
	for _, arg := range st.Args {
		a.recordObservation(cmd, arg, group)
	}
}

// investigations routes each argument to labs or imaging by its key.
func (a *analyzer) investigations(st *ast.Statement) {
	for _, arg := range st.Args {
		group := "lab"
		if a.lex.IsRadiologyKey(argKey(arg)) {
			group = "imaging"
		}
		a.recordObservation("ix", arg, group)
	}
}

// recordObservation files one argument, giving profiles their turn.
//
// The order matters and is deliberate:
//
//  1. A key the lexicon knows is core vocabulary and is handled here, so a
//     profile can never take over "hr" or "hb". Profiles are additive.
//  2. Anything else is offered to the active profiles, which is how "fhr:142"
//     reaches obstetrics without the core vitals code knowing it exists.
//  3. Whatever no profile claims is still recorded under the command's group,
//     because ad-hoc fields are a feature: an unlisted assay must not vanish.
func (a *analyzer) recordObservation(cmd string, arg ast.Argument, group string) {
	key := argKey(arg)
	_, known := a.lex.Resolve(key, lexicon.KindMeasurement)

	if !known && a.offerArgument(cmd, arg) {
		return
	}

	obs, ok := a.observation(arg, group)
	if !ok {
		a.unrecognized(cmd, arg)
		return
	}
	a.enc.Observations = append(a.enc.Observations, obs)
}

func (a *analyzer) observation(arg ast.Argument, group string) (ir.Observation, bool) {
	switch v := arg.(type) {
	case *ast.Measurement:
		if v.Key == nil {
			return ir.Observation{}, false
		}
		obs := ir.Observation{
			Code:   a.coding(v.Key.Raw, v.Key.Sp, lexicon.KindMeasurement),
			Group:  a.group(v.Key.Raw, group),
			Origin: v.Sp,
		}
		obs.Value = a.value(v)
		return obs, true

	case *ast.ConceptRef:
		if v.Name == nil {
			return ir.Observation{}, false
		}
		obs := ir.Observation{
			Code:   a.coding(v.Name.Raw, v.Name.Sp, lexicon.KindMeasurement),
			Group:  a.group(v.Name.Raw, group),
			Origin: v.Sp,
		}
		if v.Grade != nil {
			obs.Value = ir.Value{Kind: ir.ValueGrade, Grade: a.resultGrade(v.Grade)}
		} else {
			// A bare name records that the investigation was requested.
			obs.Value = ir.Value{Kind: ir.ValueFlag}
		}
		return obs, true

	default:
		return ir.Observation{}, false
	}
}

// group prefers the lexicon's own classification over the command's default,
// so "ix hb13" files haemoglobin under labs whichever command introduced it.
func (a *analyzer) group(key, fallback string) string {
	if c, ok := a.lex.Resolve(key, lexicon.KindMeasurement); ok {
		if g := c.Attr("group"); g != "" {
			return g
		}
	}
	return fallback
}

// value converts a measurement's payload, attaching the lexicon's conventional
// unit when the clinician wrote none.
func (a *analyzer) value(m *ast.Measurement) ir.Value {
	switch v := m.Value.(type) {
	case *ast.Ratio:
		r := &ir.Ratio{Origin: v.Sp}
		if v.Numerator != nil {
			r.Numerator = v.Numerator.Val
		}
		if v.Denominator != nil {
			r.Denominator = v.Denominator.Val
		}
		if c, ok := a.lex.Resolve(m.Key.Raw, lexicon.KindMeasurement); ok {
			r.Unit = c.Attr("unit")
		}
		return ir.Value{Kind: ir.ValueRatio, Ratio: r}

	case *ast.Number:
		q := &ir.Quantity{Value: v.Val, Raw: v.Raw, Origin: v.Sp}
		a.applyUnit(q, m.Unit)
		if q.Unit == "" {
			if c, ok := a.lex.Resolve(m.Key.Raw, lexicon.KindMeasurement); ok {
				// Conventional unit for display. Not a conversion and not a
				// claim about the value: the number is recorded as typed.
				q.Canonical = c.Attr("unit")
			}
		}
		return ir.Value{Kind: ir.ValueQuantity, Quantity: q}

	case *ast.Text:
		return ir.Value{Kind: ir.ValueText, Text: v.Raw}

	case *ast.Quantity:
		q := &ir.Quantity{Value: v.Number.Val, Raw: v.Number.Raw, Origin: v.Sp}
		a.applyUnit(q, v.Unit)
		return ir.Value{Kind: ir.ValueQuantity, Quantity: q}
	}

	if m.Qual != nil {
		return ir.Value{Kind: ir.ValueGrade, Grade: a.resultGrade(m.Qual)}
	}
	return ir.Value{Kind: ir.ValueFlag}
}

// ─────────────────────────────────────────────────────────────────────────────
// sx
// ─────────────────────────────────────────────────────────────────────────────

func (a *analyzer) symptoms(st *ast.Statement) {
	for _, arg := range st.Args {
		switch v := arg.(type) {
		case *ast.ConceptRef:
			f := ir.Finding{
				Code:   a.coding(v.Name.Raw, v.Name.Sp, lexicon.KindFinding, lexicon.KindAbbreviation),
				Origin: v.Sp,
			}
			if v.Grade != nil {
				f.Grade = a.grade(v.Grade)
			}
			if v.Duration != nil {
				f.Duration = a.duration(v.Duration)
			}
			a.enc.Findings = append(a.enc.Findings, f)

		case *ast.Measurement:
			// "cough3d": syntactically a key with a value, semantically a
			// finding with a duration. Only sema can tell, because only sema
			// knows that "d" is a time unit.
			f := ir.Finding{
				Code:   a.coding(v.Key.Raw, v.Key.Sp, lexicon.KindFinding, lexicon.KindAbbreviation),
				Origin: v.Sp,
			}
			if v.Qual != nil {
				f.Grade = a.grade(v.Qual)
			}
			if num, ok := v.Value.(*ast.Number); ok && v.Unit != nil {
				if du, ok := a.lex.DurationUnit(v.Unit.Raw); ok {
					f.Duration = &ir.Duration{
						Value: num.Val, Unit: du.Short,
						Raw: num.Raw + v.Unit.Raw, Origin: v.Sp,
					}
				}
			}
			a.enc.Findings = append(a.enc.Findings, f)

		case *ast.Quantity:
			// A duration standing on its own attaches to the finding before
			// it, so "back-pain++ 2d" reads the way it is written. Without a
			// preceding finding there is nothing for it to qualify.
			if d := a.standaloneDuration(v); d != nil {
				if n := len(a.enc.Findings); n > 0 {
					a.enc.Findings[n-1].Duration = d
					continue
				}
				a.bag.Warn(diag.UnexpectedToken, v.Sp,
					"a duration must follow the symptom it describes")
				continue
			}
			if !a.offerArgument("sx", arg) {
				a.unrecognized("sx", arg)
			}

		default:
			if !a.offerArgument("sx", arg) {
				a.unrecognized("sx", arg)
			}
		}
	}
}

// standaloneDuration reads a bare quantity as a duration when its unit is a
// time unit, and returns nil otherwise.
func (a *analyzer) standaloneDuration(q *ast.Quantity) *ir.Duration {
	if q.Unit == nil || q.Number == nil {
		return nil
	}
	du, ok := a.lex.DurationUnit(q.Unit.Raw)
	if !ok {
		return nil
	}
	return &ir.Duration{
		Value: q.Number.Val, Unit: du.Short,
		Raw: q.Number.Raw + q.Unit.Raw, Origin: q.Sp,
	}
}

// reviewOfSystems records pertinent negatives.
//
// Everything named under ros is recorded as explicitly denied. A separate
// command rather than a symptom modifier because "denied" and "improving" are
// different clinical statements, and the qualifier notation already means the
// second: "chills-" says the chills are getting better, not that there are
// none.
func (a *analyzer) reviewOfSystems(st *ast.Statement) {
	for _, arg := range st.Args {
		name := argKey(arg)
		if name == "" {
			if !a.offerArgument("ros", arg) {
				a.unrecognized("ros", arg)
			}
			continue
		}
		a.enc.Findings = append(a.enc.Findings, ir.Finding{
			Code:   a.coding(name, arg.Span(), lexicon.KindFinding, lexicon.KindAbbreviation),
			Absent: true,
			Origin: arg.Span(),
		})
	}
}

// examination records physical findings, written either as prose or as a
// per-system record.
//
// Which one the clinician used is decided by the parser from the presence of a
// key, so "pe chest clear bilaterally" stays a sentence while "pe cvs:rr s1s2
// rs:nvb" becomes two system findings.
func (a *analyzer) examination(st *ast.Statement) {
	if st.Trailing != nil {
		a.enc.Narratives["pe"] = a.narrative(st.Trailing)
		return
	}
	for _, arg := range st.Args {
		obs, ok := a.observation(arg, "exam")
		if !ok {
			if a.offerArgument("pe", arg) {
				continue
			}
			a.unrecognized("pe", arg)
			continue
		}
		obs.Group = "exam"
		a.enc.Observations = append(a.enc.Observations, obs)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// rx
// ─────────────────────────────────────────────────────────────────────────────

// prescription builds a medication order.
//
// This replaces a 130-line cascade of conditionals that decided token roles by
// trying each possibility in turn, and that scanned the entire drug list on
// every line to find where the name ended. Here the name is one trie lookup and
// every other token is classified by asking the lexicon what it is.
func (a *analyzer) prescription(st *ast.Statement) {
	if len(st.Args) == 0 {
		a.bag.Warn(diag.MissingDrug, st.Sp, "rx names no medication")
		return
	}

	order := ir.Order{Kind: ir.OrderMedication, Origin: st.Sp}
	rest := a.takeDrugName(&order, st.Args)

	for _, arg := range rest {
		switch v := arg.(type) {
		case *ast.Quantity:
			// A quantity in a time unit is a course length, not a second dose.
			// "rx pantoprazole 40mg iv od 3d" writes the duration without the
			// x prefix, and reading 3d as a dose would both lose the course
			// and silently replace the real dose.
			if d := a.standaloneDuration(v); d != nil {
				order.Duration = d
				continue
			}
			a.applyDose(&order, v)

		case *ast.Duration:
			order.Duration = a.duration(v)

		case *ast.ConceptRef:
			if !a.applyModifier(&order, v) {
				if a.offerArgument("rx", arg) {
					continue
				}
				a.bag.Warn(diag.UnrecognizedToken, v.Sp,
					"'"+v.Name.Raw+"' is not a known frequency, route or duration")
			}

		case *ast.Measurement:
			// Frequencies whose names contain digits — q6h, q12h — are split
			// by the parser into a key and a value, because syntactically they
			// look exactly like "hb13". Reassembling the source text and
			// asking the lexicon is what tells them apart, and it is only
			// possible here because only sema has the lexicon.
			if f, ok := a.lex.Frequency(a.file.Text(v.Sp)); ok {
				a.applyFrequency(&order, f, v.Sp)
				continue
			}

			// A bare duration written without the x prefix, such as "7d".
			if num, ok := v.Value.(*ast.Number); ok && v.Unit != nil {
				if du, ok := a.lex.DurationUnit(v.Unit.Raw); ok {
					order.Duration = &ir.Duration{
						Value: num.Val, Unit: du.Short,
						Raw: num.Raw + v.Unit.Raw, Origin: v.Sp,
					}
					continue
				}
			}
			if !a.offerArgument("rx", arg) {
				a.unrecognized("rx", arg)
			}

		default:
			if !a.offerArgument("rx", arg) {
				a.unrecognized("rx", arg)
			}
		}
	}

	if order.Timing == nil && !order.AsNeeded {
		a.bag.Hint(diag.MissingFrequency, st.Sp,
			"no dosing frequency given for "+order.Code.Label())
	}
	a.enc.Orders = append(a.enc.Orders, order)
}

// takeDrugName consumes the leading arguments naming the medication and returns
// what remains.
func (a *analyzer) takeDrugName(order *ir.Order, args []ast.Argument) []ast.Argument {
	// Gather the leading word-shaped arguments so a multi-word name such as
	// "vitamin b12" can be matched as one phrase.
	var words []string
	var nodes []ast.Argument
	for _, arg := range args {
		c, ok := arg.(*ast.ConceptRef)
		if !ok || c.Name == nil || c.Grade != nil {
			break
		}
		words = append(words, c.Name.Raw)
		nodes = append(nodes, arg)
	}

	if len(words) > 0 {
		if concept, n := a.lex.ResolvePhrase(words, lexicon.KindDrug); n > 0 {
			span := nodes[0].Span()
			for _, nd := range nodes[1:n] {
				span = spanMerge(span, nd.Span())
			}
			order.Code = ir.Coding{
				Text:      strings.Join(words[:n], " "),
				ConceptID: concept.ID,
				Display:   concept.Display,
				Origin:    span,
			}
			return args[n:]
		}
	}

	// Unknown medication: take leading words until one classifies as a
	// frequency or route, so a local formulation still records a usable name.
	take := 0
	for take < len(words) {
		if _, ok := a.lex.Resolve(words[take], lexicon.KindFrequency, lexicon.KindRoute); ok && take > 0 {
			break
		}
		take++
	}
	if take == 0 {
		take = 1
	}
	if take > len(nodes) {
		take = len(nodes)
	}
	if take == 0 {
		a.bag.Warn(diag.MissingDrug, args[0].Span(), "rx names no medication")
		return args
	}

	span := nodes[0].Span()
	for _, nd := range nodes[1:take] {
		span = spanMerge(span, nd.Span())
	}
	order.Code = ir.Coding{Text: strings.Join(words[:take], " "), Origin: span}
	return args[take:]
}

// applyModifier classifies a bare word in a prescription.
//
// Frequency is tried before route because the frequency vocabulary is the more
// specific of the two; the order is fixed rather than depending on which table
// happens to be consulted first.
func (a *analyzer) applyModifier(order *ir.Order, c *ast.ConceptRef) bool {
	name := c.Name.Raw

	if f, ok := a.lex.Frequency(name); ok {
		a.applyFrequency(order, f, c.Sp)
		return true
	}

	if r, ok := a.lex.Route(name); ok {
		order.Route = &ir.Coding{
			Text: name, ConceptID: "route." + strings.ToLower(r.Code),
			Display: r.Display, Origin: c.Sp,
		}
		return true
	}
	return false
}

// applyFrequency records a dosing frequency or the as-needed flag.
//
// As-needed and frequency are independent facts and are stored independently.
// This is the fix for the PRN defect: "qds prn" previously assigned QDS and
// then overwrote it with PRN, so the four-times-daily ceiling vanished from the
// note. PRN now sets a flag and leaves Timing untouched, and a frequency
// arriving afterwards cannot clear the flag either.
func (a *analyzer) applyFrequency(order *ir.Order, f lexicon.Frequency, sp source.Span) {
	if f.AsNeeded {
		order.AsNeeded = true
		return
	}
	if f.Priority != "" {
		order.Priority = f.Priority
	}

	if order.Timing != nil {
		a.bag.Add(diag.
			New(diag.ConflictingValue, diag.Warning, sp,
				"a second dosing frequency replaces the first").
			WithRelated(order.Timing.Origin, "first frequency here"))
	}
	order.Timing = &ir.Timing{
		Code: f.Code, Display: f.Display,
		Count: f.Count, Period: f.Period, PeriodUnit: f.Unit,
		When: f.When, Origin: sp,
	}
}

func (a *analyzer) applyDose(order *ir.Order, q *ast.Quantity) {
	dose := &ir.Quantity{Value: q.Number.Val, Raw: q.Number.Raw, Origin: q.Sp}
	a.applyUnit(dose, q.Unit)

	if order.Dose != nil {
		a.bag.Add(diag.
			New(diag.ConflictingValue, diag.Warning, q.Sp,
				"a second dose replaces the first").
			WithRelated(order.Dose.Origin, "first dose here"))
	}
	order.Dose = dose

	if dose.Unit == "" {
		a.bag.Hint(diag.MissingUnit, q.Sp, "dose has no unit")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Derivations
// ─────────────────────────────────────────────────────────────────────────────

// derive computes values the clinician did not record.
//
// Each is marked Derived with the formula used. The previous model stored BMI
// in the same field shape as a measured value, making the two indistinguishable
// downstream; a computed number and a recorded one are different kinds of fact.
func (a *analyzer) derive() {
	w, h := a.cs.Subject.Weight, a.cs.Subject.Height
	if w == nil || h == nil {
		return
	}

	kg, ok1 := a.toBase(w, "mass")
	cm, ok2 := a.toBase(h, "length")
	if !ok1 || !ok2 || kg <= 0 || cm <= 0 {
		return
	}

	m := cm / 100
	bmi := kg / (m * m)
	bsa := math.Sqrt(cm * kg / 3600)

	a.enc.Observations = append(a.enc.Observations,
		ir.Observation{
			Code:      ir.Coding{Text: "bmi", ConceptID: "derived.bmi", Display: "Body mass index"},
			Value:     ir.Value{Kind: ir.ValueQuantity, Quantity: &ir.Quantity{Value: round2(bmi), Canonical: "kg/m2"}},
			Group:     "derived",
			Derived:   true,
			DerivedAs: "weight / height²",
			Origin:    a.cs.Subject.Origin,
		},
		ir.Observation{
			Code:      ir.Coding{Text: "bsa", ConceptID: "derived.bsa", Display: "Body surface area"},
			Value:     ir.Value{Kind: ir.ValueQuantity, Quantity: &ir.Quantity{Value: round2(bsa), Canonical: "m2"}},
			Group:     "derived",
			Derived:   true,
			DerivedAs: "Mosteller: √(height × weight / 3600)",
			Origin:    a.cs.Subject.Origin,
		},
	)
}

// toBase converts a recorded quantity into its dimension's base unit.
//
// This is where a real unit table replaces character trimming. The previous
// code ran strings.TrimRight(numPart, "cmCM"), which strips any trailing
// character in that set rather than the suffix "cm", so "ht1.7m" was recorded
// as a height of 1.7 centimetres and BMI was then computed from it.
func (a *analyzer) toBase(q *ir.Quantity, dimension string) (float64, bool) {
	if q.Unit == "" {
		return q.Value, true // already in the conventional unit
	}
	u, ok := a.lex.Unit(q.Unit)
	if !ok || u.Dimension != dimension {
		return 0, false
	}
	return u.ToBase(q.Value), true
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

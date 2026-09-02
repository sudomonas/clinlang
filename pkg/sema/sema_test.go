package sema

import (
	"encoding/json"
	"strings"
	"testing"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/lexicon"
	"clinlang/pkg/parser"
	"clinlang/pkg/source"
	"clinlang/pkg/vocab"
)

func analyze(t *testing.T, src string) (*ir.Case, *diag.Bag) {
	t.Helper()
	// The language ships no clinical vocabulary; medicine is a plugin.
	lex := vocab.Lexicon()
	f := source.NewFile("test.cln", src)
	bag := diag.NewBag()
	g := parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys())
	doc := parser.Parse(f, g, bag)
	return Analyze(f, doc, bag, Options{Lexicon: lex}), bag
}

func firstOrder(t *testing.T, c *ir.Case) ir.Order {
	t.Helper()
	orders := c.Primary().Orders
	if len(orders) == 0 {
		t.Fatal("no orders produced")
	}
	return orders[0]
}

func findObs(c *ir.Case, text string) (ir.Observation, bool) {
	for _, o := range c.Primary().Observations {
		if o.Code.Text == text {
			return o, true
		}
	}
	return ir.Observation{}, false
}

// ─────────────────────────────────────────────────────────────────────────────
// The PRN defect
// ─────────────────────────────────────────────────────────────────────────────

// "rx paracetamol 1g qds prn" previously set Frequency to QDS and then
// overwrote it with PRN, so the four-times-daily ceiling never reached the
// note. Both facts must now survive.
func TestPRNDoesNotOverwriteFrequency(t *testing.T) {
	c, _ := analyze(t, "rx paracetamol 1g qds prn\n")
	o := firstOrder(t, c)

	if o.Timing == nil {
		t.Fatal("frequency was lost when prn was applied")
	}
	if o.Timing.Code != "QDS" || o.Timing.Count != 4 {
		t.Errorf("timing = %+v, want QDS four times daily", o.Timing)
	}
	if !o.AsNeeded {
		t.Error("as-needed was lost")
	}
}

// Order must not matter either: prn first, then the frequency.
func TestPRNBeforeFrequency(t *testing.T) {
	c, _ := analyze(t, "rx paracetamol 1g prn qds\n")
	o := firstOrder(t, c)

	if o.Timing == nil || o.Timing.Code != "QDS" {
		t.Errorf("timing = %+v, want QDS", o.Timing)
	}
	if !o.AsNeeded {
		t.Error("as-needed was cleared by a later frequency")
	}
}

func TestFrequencyProducesStructuredTiming(t *testing.T) {
	cases := map[string]struct {
		code   string
		count  int
		period float64
		unit   string
	}{
		"tds": {"TDS", 3, 1, "d"},
		"bd":  {"BD", 2, 1, "d"},
		"q6h": {"Q6H", 1, 6, "h"},
		"qw":  {"QW", 1, 1, "wk"},
	}
	for alias, want := range cases {
		c, _ := analyze(t, "rx amoxicillin 500mg "+alias+"\n")
		o := firstOrder(t, c)
		if o.Timing == nil {
			t.Errorf("%s produced no timing", alias)
			continue
		}
		if o.Timing.Code != want.code || o.Timing.Count != want.count ||
			o.Timing.Period != want.period || o.Timing.PeriodUnit != want.unit {
			t.Errorf("%s gave %+v, want %s %d per %g%s",
				alias, o.Timing, want.code, want.count, want.period, want.unit)
		}
	}
}

func TestPrescriptionFullyClassified(t *testing.T) {
	c, bag := analyze(t, "rx amoxicillin 500mg tds po x7d\n")
	o := firstOrder(t, c)

	if o.Kind != ir.OrderMedication {
		t.Errorf("kind = %v, want medication", o.Kind)
	}
	if o.Code.Text != "amoxicillin" || o.Code.ConceptID != "drug.amoxicillin" {
		t.Errorf("drug = %+v", o.Code)
	}
	if o.Dose == nil || o.Dose.Value != 500 || o.Dose.Canonical != "mg" {
		t.Errorf("dose = %+v, want 500 mg", o.Dose)
	}
	if o.Route == nil || o.Route.Display != "Orally" {
		t.Errorf("route = %+v, want Orally", o.Route)
	}
	if o.Duration == nil || o.Duration.Value != 7 || o.Duration.Unit != "d" {
		t.Errorf("duration = %+v, want 7 d", o.Duration)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Sorted())
	}
}

func TestMultiWordDrugName(t *testing.T) {
	c, _ := analyze(t, "rx vitamin b12 500mcg od im\n")
	o := firstOrder(t, c)

	if o.Code.Text != "vitamin b12" {
		t.Errorf("drug = %q, want %q", o.Code.Text, "vitamin b12")
	}
	if o.Code.ConceptID != "drug.vitamin_b12" {
		t.Errorf("concept = %q", o.Code.ConceptID)
	}
	if o.Dose == nil || o.Dose.Value != 500 {
		t.Errorf("dose = %+v, want 500mcg", o.Dose)
	}
}

func TestUnknownDrugStillRecorded(t *testing.T) {
	c, _ := analyze(t, "rx localformulation 10mg bd\n")
	o := firstOrder(t, c)

	if o.Code.Text != "localformulation" {
		t.Errorf("drug = %q", o.Code.Text)
	}
	if o.Code.Resolved() {
		t.Error("an unknown drug must not claim an identity")
	}
	if o.Timing == nil || o.Timing.Code != "BD" {
		t.Error("modifiers after an unknown drug were not classified")
	}
}

func TestMissingFrequencyIsAHint(t *testing.T) {
	_, bag := analyze(t, "rx amoxicillin 500mg\n")
	found := false
	for _, d := range bag.Sorted() {
		if d.Code == diag.MissingFrequency {
			found = true
			if d.Severity != diag.Hint {
				t.Errorf("severity = %v, want hint", d.Severity)
			}
		}
	}
	if !found {
		t.Error("expected a MissingFrequency diagnostic")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// The unit defect
// ─────────────────────────────────────────────────────────────────────────────

// strings.TrimRight(numPart, "cmCM") strips characters in a set, not the
// suffix "cm", so "ht1.7m" was recorded as a height of 1.7 centimetres and BMI
// was computed from a 1.7 cm patient.
func TestHeightInMetresIsNotTreatedAsCentimetres(t *testing.T) {
	c, _ := analyze(t, "pt 34M wt70kg ht1.7m\n")

	h := c.Subject.Height
	if h == nil {
		t.Fatal("height not recorded")
	}
	if h.Value != 1.7 || h.Canonical != "m" {
		t.Errorf("height = %+v, want 1.7 m recorded as typed", h)
	}

	bmi, ok := findObs(c, "bmi")
	if !ok {
		t.Fatal("BMI was not derived")
	}
	got := bmi.Value.Quantity.Value
	if got < 24 || got > 25 {
		t.Errorf("BMI = %g, want about 24.2; a 1.7 cm patient would give a nonsense value", got)
	}
}

func TestWeightAndHeightUnitsAreChecked(t *testing.T) {
	// A length unit on a weight is a real error and must be reported.
	_, bag := analyze(t, "pt 34M wt70cm\n")
	found := false
	for _, d := range bag.Sorted() {
		if d.Code == diag.UnknownUnit {
			found = true
		}
	}
	if !found {
		t.Error("expected a dimension mismatch diagnostic for wt70cm")
	}
}

func TestDerivedValuesAreMarked(t *testing.T) {
	c, _ := analyze(t, "pt 34M wt70kg ht170cm\n")

	for _, name := range []string{"bmi", "bsa"} {
		o, ok := findObs(c, name)
		if !ok {
			t.Fatalf("%s was not derived", name)
		}
		if !o.Derived {
			t.Errorf("%s must be marked derived", name)
		}
		if o.DerivedAs == "" {
			t.Errorf("%s must record how it was computed", name)
		}
	}

	// A recorded value is not derived.
	wt := c.Subject.Weight
	if wt == nil || wt.Value != 70 {
		t.Errorf("weight = %+v", wt)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Demographics
// ─────────────────────────────────────────────────────────────────────────────

func TestAgeAndSex(t *testing.T) {
	tests := []struct {
		src     string
		age     float64
		ageUnit string
		sex     string
	}{
		{"pt 34M", 34, "y", "M"},
		{"pt 28F", 28, "y", "F"},
		{"pt 6mo", 6, "mo", ""},
		{"pt 6moF", 6, "mo", "F"},
		{"pt 3wF", 3, "w", "F"},
		{"pt 45", 45, "y", ""},
	}
	for _, tc := range tests {
		c, _ := analyze(t, tc.src+"\n")
		if c.Subject.Age == nil {
			t.Errorf("%s: no age recorded", tc.src)
			continue
		}
		if c.Subject.Age.Value != tc.age || c.Subject.Age.Unit != tc.ageUnit {
			t.Errorf("%s: age = %g%s, want %g%s", tc.src,
				c.Subject.Age.Value, c.Subject.Age.Unit, tc.age, tc.ageUnit)
		}
		gotSex := ""
		if c.Subject.Sex != nil {
			gotSex = c.Subject.Sex.Text
		}
		if gotSex != tc.sex {
			t.Errorf("%s: sex = %q, want %q", tc.src, gotSex, tc.sex)
		}
	}
}

// Lowercase "m" is the month alias, uppercase "M" is male. The distinction is
// documented and case-sensitive; the previous engine had no rule and stripped a
// trailing capital from any token that reached the end of its handler chain.
func TestLowercaseMIsMonthsNotMale(t *testing.T) {
	c, _ := analyze(t, "pt 6m\n")
	if c.Subject.Age == nil || c.Subject.Age.Unit != "mo" {
		t.Errorf("age = %+v, want 6 months", c.Subject.Age)
	}
	if c.Subject.Sex != nil {
		t.Errorf("sex = %+v, want none; lowercase m is a time unit", c.Subject.Sex)
	}
}

// An unrecognised token must never set sex as a side effect.
func TestUnknownTokenDoesNotSetSex(t *testing.T) {
	c, bag := analyze(t, "pt 34M zzzF\n")
	if c.Subject.Sex == nil || c.Subject.Sex.Text != "M" {
		t.Errorf("sex = %+v, want M from the age token only", c.Subject.Sex)
	}
	found := false
	for _, d := range bag.Sorted() {
		if d.Code == diag.UnrecognizedToken {
			found = true
		}
	}
	if !found {
		t.Error("the unrecognised token should be reported, not silently consumed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Observations
// ─────────────────────────────────────────────────────────────────────────────

func TestBloodPressureIsAStructuredRatio(t *testing.T) {
	c, _ := analyze(t, "vitals bp140/90\n")
	o, ok := findObs(c, "bp")
	if !ok {
		t.Fatal("bp not recorded")
	}
	if o.Value.Kind != ir.ValueRatio || o.Value.Ratio == nil {
		t.Fatalf("bp value = %+v, want a ratio", o.Value)
	}
	if o.Value.Ratio.Numerator != 140 || o.Value.Ratio.Denominator != 90 {
		t.Errorf("bp = %+v, want 140/90", o.Value.Ratio)
	}
	if o.Value.Ratio.Unit != "mmHg" {
		t.Errorf("bp unit = %q, want mmHg from the lexicon", o.Value.Ratio.Unit)
	}
}

func TestVitalsAndLabsShareOneType(t *testing.T) {
	c, _ := analyze(t, "vitals hr98\nlab hb13.5\n")

	if len(c.Primary().ObservationsIn("vitals")) != 1 {
		t.Error("heart rate not filed under vitals")
	}
	if len(c.Primary().ObservationsIn("lab")) != 1 {
		t.Error("haemoglobin not filed under labs")
	}
	hb, _ := findObs(c, "hb")
	if hb.Code.ConceptID != "measurement.hb" {
		t.Errorf("hb concept = %q", hb.Code.ConceptID)
	}
	if hb.Value.Quantity == nil || hb.Value.Quantity.Value != 13.5 {
		t.Errorf("hb value = %+v", hb.Value)
	}
}

func TestIxRoutesByLexicon(t *testing.T) {
	c, _ := analyze(t, "ix hb12.1 cxr:wnl\n")

	hb, ok := findObs(c, "hb")
	if !ok || hb.Group != "lab" {
		t.Errorf("hb group = %q, want lab", hb.Group)
	}
	cxr, ok := findObs(c, "cxr")
	if !ok || cxr.Group != "imaging" {
		t.Errorf("cxr group = %q, want imaging", cxr.Group)
	}
	if cxr.Value.Kind != ir.ValueText || cxr.Value.Text != "wnl" {
		t.Errorf("cxr value = %+v", cxr.Value)
	}
}

// The same qualifier means different things in different statements. On a
// result "-" is an outcome and reads "negative"; on a symptom it is a trend and
// reads "improving". Rendering "HIV improving" would be wrong and alarming.
func TestQualifierMeaningDependsOnStatement(t *testing.T) {
	lab, _ := analyze(t, "lab hiv- crp+++\n")

	hiv, ok := findObs(lab, "hiv")
	if !ok || hiv.Value.Kind != ir.ValueGrade {
		t.Fatalf("hiv = %+v", hiv)
	}
	if hiv.Value.Grade.Symbol != "-" || hiv.Value.Grade.Label != "negative" {
		t.Errorf("hiv grade = %+v, want negative", hiv.Value.Grade)
	}
	crp, _ := findObs(lab, "crp")
	if crp.Value.Grade.Label != "strongly positive" {
		t.Errorf("crp grade = %+v, want strongly positive", crp.Value.Grade)
	}

	// The identical notation on a symptom keeps the trend vocabulary.
	sx, _ := analyze(t, "sx cough-\n")
	f := sx.Primary().Findings
	if len(f) != 1 || f[0].Grade == nil {
		t.Fatalf("findings = %+v", f)
	}
	if f[0].Grade.Label != "improving" {
		t.Errorf("cough grade = %+v, want improving", f[0].Grade)
	}
}

func TestBareInvestigationIsAFlag(t *testing.T) {
	c, _ := analyze(t, "lab pco2\n")
	o, ok := findObs(c, "pco2")
	if !ok {
		t.Fatal("pco2 not recorded")
	}
	if o.Value.Kind != ir.ValueFlag {
		t.Errorf("value = %+v, want a flag meaning requested", o.Value)
	}
	if o.Code.ConceptID != "measurement.pco2" {
		t.Errorf("concept = %q", o.Code.ConceptID)
	}
}

// Temperature is recorded exactly as written. The engine does not infer a
// missing unit, because inferring one would be a claim about the value.
func TestTemperatureUnitRecordedAsTyped(t *testing.T) {
	c, _ := analyze(t, "vitals temp37.2c\n")
	o, _ := findObs(c, "temp")
	if o.Value.Quantity == nil || o.Value.Quantity.Value != 37.2 {
		t.Fatalf("temp = %+v", o.Value)
	}
	if o.Value.Quantity.Unit != "c" {
		t.Errorf("unit = %q, want the unit as typed", o.Value.Quantity.Unit)
	}

	bare, _ := analyze(t, "vitals temp37.2\n")
	o2, _ := findObs(bare, "temp")
	if o2.Value.Quantity.Unit != "" {
		t.Errorf("unit = %q, want none: the engine must not guess",
			o2.Value.Quantity.Unit)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Symptoms
// ─────────────────────────────────────────────────────────────────────────────

func TestSymptomsWithGradeAndDuration(t *testing.T) {
	c, _ := analyze(t, "sx chestpain+++4h nausea++ non-productive-cough\n")
	f := c.Primary().Findings

	if len(f) != 3 {
		t.Fatalf("got %d findings, want 3", len(f))
	}
	if f[0].Code.Text != "chestpain" || f[0].Grade.Label != "very severe" {
		t.Errorf("first finding = %+v", f[0])
	}
	if f[0].Duration == nil || f[0].Duration.Value != 4 || f[0].Duration.Unit != "h" {
		t.Errorf("duration = %+v", f[0].Duration)
	}
	// The hyphenated name survives intact; the old parser reduced it to "non".
	if f[2].Code.Text != "non-productive-cough" {
		t.Errorf("third finding = %q, want the full hyphenated name", f[2].Code.Text)
	}
}

func TestSymptomWithGluedDuration(t *testing.T) {
	c, _ := analyze(t, "sx cough3d\n")
	f := c.Primary().Findings
	if len(f) != 1 {
		t.Fatalf("got %d findings", len(f))
	}
	if f[0].Code.Text != "cough" {
		t.Errorf("name = %q", f[0].Code.Text)
	}
	if f[0].Duration == nil || f[0].Duration.Value != 3 || f[0].Duration.Unit != "d" {
		t.Errorf("duration = %+v, want 3 days", f[0].Duration)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Narratives
// ─────────────────────────────────────────────────────────────────────────────

func TestNarrativeKeepsInputAndExpansion(t *testing.T) {
	c, _ := analyze(t, "pmh dm2, htn since 2010.\n")
	n, ok := c.Primary().Narrative("pmh")
	if !ok {
		t.Fatal("pmh not recorded")
	}
	// The verbatim text is what the clinician typed.
	if n.Text != "dm2, htn since 2010." {
		t.Errorf("text = %q, want the input unchanged", n.Text)
	}
	// The display form expands abbreviations without losing the punctuation.
	want := "Type 2 Diabetes Mellitus, Hypertension since 2010."
	if n.Display != want {
		t.Errorf("display = %q, want %q", n.Display, want)
	}
}

func TestNarrativeUnicodeSafe(t *testing.T) {
	c, _ := analyze(t, "hpi Ménière disease, 5µg dose, Löfgren syndrome\n")
	n, _ := c.Primary().Narrative("hpi")
	for _, want := range []string{"Ménière", "µg", "Löfgren"} {
		if !contains(n.Text, want) {
			t.Errorf("text lost %q: %q", want, n.Text)
		}
	}
}

func TestOEIsAnAliasForPE(t *testing.T) {
	c, _ := analyze(t, "oe chest clear\n")
	if _, ok := c.Primary().Narrative("pe"); !ok {
		t.Error("oe should fill the same section as pe, not a second one")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scope and structure
// ─────────────────────────────────────────────────────────────────────────────

// A profile declaration applies to the whole document wherever it appears. The
// previous engine registered profiles mid-walk, so @profile silently had to
// come first.
func TestProfileOrderDoesNotMatter(t *testing.T) {
	lex := lexicon.Default()
	p := &fakeProfile{name: "obgyn"}

	for _, src := range []string{
		"@profile obgyn\npt 28F\n",
		"pt 28F\n@profile obgyn\n",
	} {
		f := source.NewFile("t.cln", src)
		bag := diag.NewBag()
		g := parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys())
		doc := parser.Parse(f, g, bag)
		c := Analyze(f, doc, bag, Options{
			Lexicon:  lex,
			Profiles: map[string]Profile{"obgyn": p},
		})
		if len(c.Profiles) != 1 || c.Profiles[0] != "obgyn" {
			t.Errorf("%q: profiles = %v", src, c.Profiles)
		}
	}
}

func TestMultipleProfilesCoexist(t *testing.T) {
	lex := lexicon.Default()
	f := source.NewFile("t.cln", "@profile obgyn+peds\npt 28F\n")
	bag := diag.NewBag()
	g := parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys())
	doc := parser.Parse(f, g, bag)

	c := Analyze(f, doc, bag, Options{
		Lexicon: lex,
		Profiles: map[string]Profile{
			"obgyn": &fakeProfile{name: "obgyn"},
			"peds":  &fakeProfile{name: "peds"},
		},
	})

	if len(c.Profiles) != 2 {
		t.Fatalf("profiles = %v, want both", c.Profiles)
	}
	// Both payloads must be retrievable. The previous engine replaced the
	// single payload with a map when more than one profile was active, so
	// every type assertion failed and all profile commands silently no-opped.
	for _, name := range []string{"obgyn", "peds"} {
		if _, ok := ir.Extension[*fakeData](c.Primary(), name); !ok {
			t.Errorf("%s extension data missing under a combined profile", name)
		}
	}
}

func TestUnknownProfileReported(t *testing.T) {
	_, bag := analyze(t, "@profile nosuchprofile\npt 34M\n")
	found := false
	for _, d := range bag.Sorted() {
		if d.Code == diag.UnknownProfile {
			found = true
		}
	}
	if !found {
		t.Error("expected an UnknownProfile diagnostic")
	}
}

func TestDuplicateStatementReportsBothPositions(t *testing.T) {
	_, bag := analyze(t, "pt 34M\npt 40F\n")
	for _, d := range bag.Sorted() {
		if d.Code == diag.DuplicateStatement {
			if len(d.Related) != 1 {
				t.Error("duplicate diagnostic should point back at the first occurrence")
			}
			return
		}
	}
	t.Error("expected a DuplicateStatement diagnostic")
}

func TestUnknownCommandStillRecordsData(t *testing.T) {
	c, bag := analyze(t, "partogram dil4 eff80\n")

	if len(c.Primary().ObservationsIn("partogram")) != 2 {
		t.Errorf("ad-hoc command data was not recorded: %+v", c.Primary().Observations)
	}
	// Recording ad-hoc fields is a feature, so this is a hint, not a warning.
	for _, d := range bag.Sorted() {
		if d.Code == diag.UnknownCommand && d.Severity != diag.Hint {
			t.Errorf("severity = %v, want hint", d.Severity)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Whole-document behaviour
// ─────────────────────────────────────────────────────────────────────────────

func TestRealisticNoteProducesNoWarnings(t *testing.T) {
	src := "@clinlang 1.0\n" +
		"pt 28F wt65kg ht162cm\n" +
		"cc reduced fetal movements since yesterday.\n" +
		"hpi worsening overnight, no bleeding.\n" +
		"pmh dm2, htn\n" +
		"vitals bp118/72 hr84 temp36.8c spo298\n" +
		"sx nausea+ back-pain++ 2d\n" +
		"lab hb11.2 plt210 pco2:38\n" +
		"ix usg:normal\n" +
		"rx amoxicillin 500mg tds po x7d\n" +
		"dx uncomplicated pregnancy\n"

	c, bag := analyze(t, src)

	for _, d := range bag.Sorted() {
		if d.Severity >= diag.Warning {
			t.Errorf("unexpected %s %s at %+v: %s", d.Severity, d.Code, d.Span, d.Message)
		}
	}
	if c.Subject.Age == nil || c.Subject.Sex == nil {
		t.Error("demographics missing")
	}
	if len(c.Primary().Orders) != 1 {
		t.Errorf("orders = %d, want 1", len(c.Primary().Orders))
	}
	if len(c.Primary().Findings) != 2 {
		t.Errorf("findings = %d, want 2", len(c.Primary().Findings))
	}
}

func TestAnalysisIsDeterministic(t *testing.T) {
	src := "@profile x\npt 28F wt65kg ht162cm\nvitals bp118/72 hr84\n" +
		"lab hb11.2 na138 k4.1 pco2:38\nrx amoxicillin 500mg tds po x7d\n" +
		"rx metoprolol 25mg bd\n"

	first := renderCase(t, src)
	for i := 0; i < 30; i++ {
		if got := renderCase(t, src); got != first {
			t.Fatalf("analysis %d differed from the first", i)
		}
	}
}

func TestEmptyAndGarbageInput(t *testing.T) {
	for _, src := range []string{"", "\n\n", "!!!\n", "@@@\n", "   "} {
		c, _ := analyze(t, src)
		if c == nil {
			t.Fatalf("Analyze(%q) returned nil", src)
		}
		if len(c.Encounters) != 1 {
			t.Errorf("Analyze(%q) produced %d encounters, want 1", src, len(c.Encounters))
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

type fakeData struct{ Touched bool }

// fakeProfile is an inert profile: it registers a name and a typed payload but
// claims no commands, which is what the scope tests need.
type fakeProfile struct{ name string }

func (p *fakeProfile) Name() string { return p.name }
func (p *fakeProfile) NewData() any { return &fakeData{} }

func (p *fakeProfile) Command(string, *ast.Statement, *Context) bool { return false }
func (p *fakeProfile) Argument(string, ast.Argument, *Context) bool  { return false }

// renderCase serialises the analysed case and its diagnostics, so determinism
// can be compared byte for byte.
func renderCase(t *testing.T, src string) string {
	t.Helper()
	c, bag := analyze(t, src)

	caseJSON, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	diagJSON, err := json.Marshal(bag.Sorted())
	if err != nil {
		t.Fatal(err)
	}
	return string(caseJSON) + "|" + string(diagJSON)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// ─────────────────────────────────────────────────────────────────────────────
// Sectioned values and review of systems
// ─────────────────────────────────────────────────────────────────────────────

// A colon value runs to the next key, so a multi-word finding no longer has to
// be glued together with underscores. This is the rule the documentation
// already described and neither engine implemented.
func TestSectionedExamination(t *testing.T) {
	c, bag := analyze(t, "pe cvs:rr s1s2 rs:nvb abd:tender rif guarding\n")

	want := map[string]string{
		"cvs": "rr s1s2",
		"rs":  "nvb",
		"abd": "tender rif guarding",
	}
	for key, text := range want {
		o, ok := findObs(c, key)
		if !ok {
			t.Errorf("%s not recorded", key)
			continue
		}
		if o.Group != "exam" {
			t.Errorf("%s group = %q, want exam", key, o.Group)
		}
		if o.Value.Kind != ir.ValueText || o.Value.Text != text {
			t.Errorf("%s = %+v, want text %q", key, o.Value, text)
		}
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Sorted())
	}
}

// Examination without any key is a sentence and must stay one.
func TestExaminationWithoutKeysStaysProse(t *testing.T) {
	c, _ := analyze(t, "pe chest clear bilaterally, no added sounds\n")
	n, ok := c.Primary().Narrative("pe")
	if !ok {
		t.Fatal("prose examination not recorded as a narrative")
	}
	if n.Text != "chest clear bilaterally, no added sounds" {
		t.Errorf("text = %q", n.Text)
	}
	if len(c.Primary().ObservationsIn("exam")) != 0 {
		t.Error("prose examination must not be tokenized into observations")
	}
}

func TestExamIsAnAliasForPE(t *testing.T) {
	c, _ := analyze(t, "exam chest clear\n")
	if _, ok := c.Primary().Narrative("pe"); !ok {
		t.Error("exam should fill the same section as pe")
	}
}

// Numeric results and a descriptive finding coexist on one investigation line.
func TestSectionedInvestigationsMixWithNumerics(t *testing.T) {
	c, bag := analyze(t, "ix hb9.2 wbc18000 cxr:patchy consolidation left lower lobe\n")

	hb, ok := findObs(c, "hb")
	if !ok || hb.Value.Quantity == nil || hb.Value.Quantity.Value != 9.2 {
		t.Errorf("hb = %+v, want a numeric 9.2", hb.Value)
	}
	cxr, ok := findObs(c, "cxr")
	if !ok {
		t.Fatal("cxr not recorded")
	}
	if cxr.Value.Text != "patchy consolidation left lower lobe" {
		t.Errorf("cxr = %q, want the whole finding as one value", cxr.Value.Text)
	}
	if cxr.Group != "imaging" {
		t.Errorf("cxr group = %q, want imaging", cxr.Group)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Sorted())
	}
}

// Single-token values keep the existing grammar, so quantities and ratios are
// unaffected by sectioning.
func TestSingleTokenSectionValuesUnchanged(t *testing.T) {
	c, _ := analyze(t, "ix pco2:45 widal:1/60 cxr:wnl\n")

	pco2, _ := findObs(c, "pco2")
	if pco2.Value.Kind != ir.ValueQuantity || pco2.Value.Quantity.Value != 45 {
		t.Errorf("pco2 = %+v, want quantity 45", pco2.Value)
	}
	widal, _ := findObs(c, "widal")
	if widal.Value.Kind != ir.ValueRatio || widal.Value.Ratio.Numerator != 1 {
		t.Errorf("widal = %+v, want ratio 1/60", widal.Value)
	}
	cxr, _ := findObs(c, "cxr")
	if cxr.Value.Kind != ir.ValueText || cxr.Value.Text != "wnl" {
		t.Errorf("cxr = %+v, want text wnl", cxr.Value)
	}
}

// Denial and improvement are different clinical statements. "chills-" says the
// chills are getting better; only ros says there are none.
func TestReviewOfSystemsRecordsDenials(t *testing.T) {
	c, bag := analyze(t, "sx cough++\nros fever sob nausea\n")

	f := c.Primary().Findings
	if len(f) != 4 {
		t.Fatalf("findings = %d, want 4", len(f))
	}
	if f[0].Code.Text != "cough" || f[0].Absent {
		t.Errorf("cough = %+v, want a present symptom", f[0])
	}
	for _, denied := range f[1:] {
		if !denied.Absent {
			t.Errorf("%q should be recorded as absent", denied.Code.Text)
		}
		if denied.Grade != nil {
			t.Errorf("%q should carry no grade", denied.Code.Text)
		}
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Sorted())
	}
}

func TestDeniedIsNotImproving(t *testing.T) {
	c, _ := analyze(t, "sx chills-\nros chills\n")
	f := c.Primary().Findings
	if len(f) != 2 {
		t.Fatalf("findings = %d, want 2", len(f))
	}
	if f[0].Absent {
		t.Error("chills- means improving, not absent")
	}
	if f[0].Grade == nil || f[0].Grade.Label != "improving" {
		t.Errorf("chills- grade = %+v, want improving", f[0].Grade)
	}
	if !f[1].Absent {
		t.Error("ros chills must record absence")
	}
}

// Hyphens need no Shift key and already work, so a multi-word symptom needs no
// underscore.
func TestHyphenatedSymptomsReplaceUnderscores(t *testing.T) {
	c, _ := analyze(t, "sx abd-pain+++12h non-productive-cough++\n")
	f := c.Primary().Findings
	if len(f) != 2 {
		t.Fatalf("findings = %d, want 2", len(f))
	}
	if f[0].Code.Text != "abd-pain" || f[0].Grade.Label != "very severe" {
		t.Errorf("first = %+v", f[0])
	}
	if f[0].Duration == nil || f[0].Duration.Value != 12 || f[0].Duration.Unit != "h" {
		t.Errorf("duration = %+v, want 12h", f[0].Duration)
	}
	if f[1].Code.Text != "non-productive-cough" {
		t.Errorf("second = %q", f[1].Code.Text)
	}
}

// Every command the parser calls core must be dispatched by sema.
//
// The two lists live in different packages — the parser needs shapes, sema
// needs behaviour — so they can drift. This test is the link between them: a
// core command with no handler would silently fall through to the ad-hoc
// recording path and quietly stop doing what it was supposed to do.
func TestEveryCoreCommandIsHandled(t *testing.T) {
	for cmd := range parser.CoreShapes {
		src := cmd + " placeholder\n"
		_, bag := analyze(t, src)

		for _, d := range bag.Sorted() {
			if d.Code == diag.UnknownCommand {
				t.Errorf("core command %q has no handler in sema; it fell through "+
					"to the ad-hoc recording path", cmd)
			}
		}
	}
}

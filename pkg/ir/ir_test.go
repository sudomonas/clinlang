package ir

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"clinlang/pkg/source"
)

// Frequency and as-needed-ness are independent fields, so an order can carry
// both. The previous model crammed them into one string, so "qds prn" set QDS
// and then overwrote it with PRN, dropping the four-times-daily ceiling from
// the output entirely.
func TestAsNeededIsOrthogonalToTiming(t *testing.T) {
	o := Order{
		Kind: OrderMedication,
		Code: Coding{Text: "paracetamol"},
		Dose: &Quantity{Value: 1, Unit: "g"},
		Timing: &Timing{
			Code: "QDS", Display: "Four times daily",
			Count: 4, Period: 1, PeriodUnit: "d",
		},
		AsNeeded: true,
	}

	if o.Timing == nil || o.Timing.Count != 4 {
		t.Fatal("frequency was lost")
	}
	if !o.AsNeeded {
		t.Fatal("as-needed was lost")
	}

	// Both must survive serialisation, since that is where the old model
	// dropped one of them.
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"count":4`) || !strings.Contains(s, `"as_needed":true`) {
		t.Errorf("JSON lost one of the two facts: %s", s)
	}
}

func TestCodingLabelPrefersDisplayButKeepsText(t *testing.T) {
	c := Coding{Text: "dm2", Display: "Type 2 Diabetes Mellitus"}
	if c.Label() != "Type 2 Diabetes Mellitus" {
		t.Errorf("Label = %q, want the display form", c.Label())
	}
	// The verbatim input must remain retrievable: the engine never substitutes.
	if c.Text != "dm2" {
		t.Errorf("Text = %q, want the original input preserved", c.Text)
	}

	bare := Coding{Text: "unknownterm"}
	if bare.Label() != "unknownterm" {
		t.Errorf("Label = %q, want the text when no display exists", bare.Label())
	}
}

// The typed accessor replaces `SpecialtyData any` with its repeated unchecked
// assertions, where a failed assertion silently discarded the write.
func TestExtensionIsTypedAndSafe(t *testing.T) {
	type ObGyn struct{ GA int }

	var e Encounter
	SetExtension(&e, "obgyn", &ObGyn{GA: 34})

	got, ok := Extension[*ObGyn](&e, "obgyn")
	if !ok || got.GA != 34 {
		t.Fatalf("Extension returned (%+v, %v), want GA 34", got, ok)
	}

	// A wrong type reports failure instead of panicking.
	if _, ok := Extension[*Timing](&e, "obgyn"); ok {
		t.Error("Extension should reject a mismatched type")
	}
	// An absent profile reports failure.
	if _, ok := Extension[*ObGyn](&e, "peds"); ok {
		t.Error("Extension should report absent profiles")
	}
	// A nil encounter is safe.
	if _, ok := Extension[*ObGyn](nil, "obgyn"); ok {
		t.Error("Extension on nil should report failure")
	}
}

// Multiple profiles must coexist. The previous engine stored a map[string]any
// for multi-profile documents, so every single-type assertion failed at once
// and all five OB/GYN commands silently no-opped under "@profile obgyn+peds".
func TestMultipleProfileExtensionsCoexist(t *testing.T) {
	type ObGyn struct{ GA int }
	type Peds struct{ WeightCentile int }

	var e Encounter
	SetExtension(&e, "obgyn", &ObGyn{GA: 34})
	SetExtension(&e, "peds", &Peds{WeightCentile: 50})

	og, ok := Extension[*ObGyn](&e, "obgyn")
	if !ok || og.GA != 34 {
		t.Error("obgyn extension lost when a second profile is active")
	}
	pd, ok := Extension[*Peds](&e, "peds")
	if !ok || pd.WeightCentile != 50 {
		t.Error("peds extension lost when a second profile is active")
	}
}

func TestPrimaryCreatesAnEncounter(t *testing.T) {
	var c Case
	e := c.Primary()
	if e == nil {
		t.Fatal("Primary returned nil")
	}
	if len(c.Encounters) != 1 {
		t.Errorf("Primary did not attach the encounter, got %d", len(c.Encounters))
	}
	// Repeated calls return the same encounter, not a new one each time.
	e.Context = "day 3"
	if c.Primary().Context != "day 3" {
		t.Error("Primary returned a different encounter on the second call")
	}
}

func TestDerivedValuesAreMarked(t *testing.T) {
	// BMI is computed, not recorded. The previous model made it
	// indistinguishable from a measured value.
	bmi := Observation{
		Code:      Coding{Text: "bmi", Display: "Body mass index"},
		Value:     Value{Kind: ValueQuantity, Quantity: &Quantity{Value: 24.8}},
		Derived:   true,
		DerivedAs: "weight / height²",
	}
	measured := Observation{
		Code:  Coding{Text: "wt"},
		Value: Value{Kind: ValueQuantity, Quantity: &Quantity{Value: 65, Unit: "kg"}},
	}

	if !bmi.Derived {
		t.Error("BMI must be marked derived")
	}
	if measured.Derived {
		t.Error("a recorded value must not be marked derived")
	}
}

func TestEncounterQueries(t *testing.T) {
	e := Encounter{
		Observations: []Observation{
			{Code: Coding{Text: "hr"}, Group: "vitals"},
			{Code: Coding{Text: "hb"}, Group: "lab"},
			{Code: Coding{Text: "bp"}, Group: "vitals"},
		},
		Orders: []Order{
			{Kind: OrderMedication, Code: Coding{Text: "amoxicillin"}},
			{Kind: OrderLaboratory, Code: Coding{Text: "fbc"}},
		},
		Narratives: map[string]Narrative{"cc": {Text: "chest pain"}},
	}

	if got := len(e.ObservationsIn("vitals")); got != 2 {
		t.Errorf("ObservationsIn(vitals) = %d, want 2", got)
	}
	if got := len(e.OrdersOf(OrderMedication)); got != 1 {
		t.Errorf("OrdersOf(medication) = %d, want 1", got)
	}
	if n, ok := e.Narrative("cc"); !ok || n.Text != "chest pain" {
		t.Errorf("Narrative(cc) = (%+v, %v)", n, ok)
	}
	if _, ok := e.Narrative("hpi"); ok {
		t.Error("Narrative should report absent sections")
	}
}

func TestOrderKindMarshalsByName(t *testing.T) {
	b, err := json.Marshal(Order{Kind: OrderMedication, Code: Coding{Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"medication"`) {
		t.Errorf("order kind did not marshal by name: %s", b)
	}
}

func TestProvenanceIsCarried(t *testing.T) {
	span := source.Span{Start: 12, End: 20}
	o := Observation{Code: Coding{Text: "hb", Origin: span}, Origin: span}
	if o.Origin != span || o.Code.Origin != span {
		t.Error("origin spans must survive on both the node and its coding")
	}
}

// ClinLang records and renders; it does not evaluate. No IR type may gain a
// field that ranks, rates, or interprets a clinical value. This test fails if
// someone adds one, which is the enforcement the architecture review asked for.
func TestIRCannotExpressClinicalJudgement(t *testing.T) {
	banned := []string{
		"severity", "critical", "abnormal", "flag", "alert", "risk",
		"referencerange", "normalrange", "low", "high", "interpretation",
		"rank", "score", "urgency", "action",
	}

	types := []reflect.Type{
		reflect.TypeOf(Coding{}),
		reflect.TypeOf(Quantity{}),
		reflect.TypeOf(Ratio{}),
		reflect.TypeOf(Duration{}),
		reflect.TypeOf(Value{}),
		reflect.TypeOf(Grade{}),
		reflect.TypeOf(Observation{}),
		reflect.TypeOf(Finding{}),
		reflect.TypeOf(Timing{}),
		reflect.TypeOf(Order{}),
		reflect.TypeOf(Narrative{}),
		reflect.TypeOf(Subject{}),
		reflect.TypeOf(Encounter{}),
		reflect.TypeOf(Case{}),
	}

	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, b := range banned {
				if name == b {
					t.Errorf("%s.%s: ClinLang must not evaluate clinical values. "+
						"Recording a value is in scope; rating it is not.",
						typ.Name(), typ.Field(i).Name)
				}
			}
		}
	}
}

// Grade records what was typed and what it is called, and nothing that orders
// one grade above another.
func TestGradeCarriesNoRanking(t *testing.T) {
	typ := reflect.TypeOf(Grade{})
	allowed := map[string]bool{"Symbol": true, "Label": true, "Origin": true}
	for i := 0; i < typ.NumField(); i++ {
		if !allowed[typ.Field(i).Name] {
			t.Errorf("Grade gained field %q; grades must carry no ordering or rank",
				typ.Field(i).Name)
		}
	}
}

// Anything that marshals by name must also unmarshal, or the engine cannot
// read back its own output and any integration that round-trips a case through
// storage breaks silently.
func TestNamedEnumsRoundTrip(t *testing.T) {
	kinds := []OrderKind{
		OrderUnknown, OrderMedication, OrderLaboratory,
		OrderImaging, OrderReferral, OrderProcedure,
	}
	for _, want := range kinds {
		b, err := json.Marshal(Order{Kind: want, Code: Coding{Text: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		var got Order
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("%v did not unmarshal: %v", want, err)
		}
		if got.Kind != want {
			t.Errorf("round trip gave %v, want %v", got.Kind, want)
		}
	}
}

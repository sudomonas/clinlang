package backend

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/parser"
	"clinlang/pkg/sema"
	"clinlang/pkg/source"
	"clinlang/pkg/vocab"
)

var update = flag.Bool("update", false, "rewrite golden output files")

// build runs the whole pipeline so backend tests exercise real IR rather than
// hand-built structs that could drift from what sema actually produces.
func build(t *testing.T, src string) (*ir.Case, []diag.Diagnostic) {
	t.Helper()
	lex := vocab.Lexicon()
	f := source.NewFile("test.cln", src)
	bag := diag.NewBag()
	g := parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys())
	doc := parser.Parse(f, g, bag)
	return sema.Analyze(f, doc, bag, sema.Options{Lexicon: lex}), bag.Sorted()
}

// note is the reference document used for the golden files. It exercises every
// section, both value shapes, a denied finding, a derived value and the
// as-needed prescription that the previous engine rendered wrongly.
const note = `pt 58M wt82kg ht1.78m bed12 unit:CCU
id PT-REF-0001
day Day 2 post-admission
alg penicillin rash, sulfa anaphylaxis
cc chest pain and breathlessness since 4h
hpi sudden onset central chest pain radiating to left arm, with diaphoresis
pmh dm2 htn
sh smoker 40py
sx chestpain+++4h sob++ nausea+
ros fever cough syncope
pe cvs:s1s2 no murmurs rs:bibasal crackles abd:soft non-tender
vitals bp160/100 hr98 spo292 temp37.4c rr22
lab hb13.2 wbc11000 trop0.8 na138 k4.1 hiv-
ix cxr:patchy consolidation left lower lobe ecg:st elevation inferior leads
rx aspirin 300mg po stat
rx paracetamol 1g qds prn po
rx amoxicillin 500mg tds po x7d
dx inferior STEMI for primary PCI
ddx pericarditis, aortic dissection
`

func TestGoldenOutput(t *testing.T) {
	c, _ := build(t, note)

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			b, ok := Get(name)
			if !ok {
				t.Fatalf("backend %q not registered", name)
			}
			got, diags, err := b.Emit(c, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(diags) != 0 {
				t.Errorf("rendering produced diagnostics: %v", diags)
			}

			golden := filepath.Join("testdata", goldenName(name))
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated %s", golden)
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run: go test ./pkg/backend/ -update): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("output changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

func goldenName(backend string) string {
	switch backend {
	case "json":
		return "reference.json"
	case "markdown":
		return "reference.md"
	default:
		return "reference." + backend + ".txt"
	}
}

// The defect the previous engine had: "qds prn" printed only "As needed",
// dropping the four-times-daily ceiling from the note entirely.
func TestPRNRendersBothFacts(t *testing.T) {
	c, _ := build(t, "rx paracetamol 1g qds prn po\n")

	for _, name := range []string{"soap", "plain", "markdown"} {
		b, _ := Get(name)
		out, _, _ := b.Emit(c, Options{})
		s := string(out)
		if !strings.Contains(s, "four times daily") {
			t.Errorf("%s: dosing frequency missing from output:\n%s", name, s)
		}
		if !strings.Contains(s, "as needed") {
			t.Errorf("%s: as-needed missing from output:\n%s", name, s)
		}
	}
}

// Every text backend renders a prescription identically, because they share one
// implementation. In the previous engine the block was duplicated across two
// formatters and had already drifted between them.
func TestPrescriptionWordingIsShared(t *testing.T) {
	c, _ := build(t, "rx amoxicillin 500mg tds po x7d\n")
	want := "Amoxicillin 500 mg Orally, three times daily for 7 days"

	for _, name := range []string{"soap", "plain", "markdown"} {
		b, _ := Get(name)
		out, _, _ := b.Emit(c, Options{})
		if !strings.Contains(string(out), want) {
			t.Errorf("%s does not contain the shared wording %q:\n%s", name, want, out)
		}
	}
}

// A denied symptom must read as denied, never as merely absent from the list.
func TestDeniedFindingsAreStated(t *testing.T) {
	c, _ := build(t, "sx cough++\nros fever sob\n")
	b, _ := Get("soap")
	out, _, _ := b.Emit(c, Options{})
	s := string(out)

	// Denials get their own line: a list of positives reads as the
	// presentation, a list of denials as the review of systems, and
	// interleaving them makes a reader check every entry for a suffix.
	if !strings.Contains(s, "Symptoms       : Cough (severe)") {
		t.Errorf("present symptoms not rendered:\n%s", s)
	}
	if !strings.Contains(s, "Denies         : Fever; Shortness of breath") {
		t.Errorf("denials not rendered on their own line:\n%s", s)
	}
	// A denial must never appear among the positives.
	symptomLine := lineContaining(s, "Symptoms       :")
	if strings.Contains(strings.ToLower(symptomLine), "fever") {
		t.Errorf("a denied symptom leaked into the positive list: %q", symptomLine)
	}
}

func lineContaining(s, prefix string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, prefix) {
			return line
		}
	}
	return ""
}

func TestVerbatimOptionSuppressesExpansion(t *testing.T) {
	c, _ := build(t, "pmh dm2 htn\n")
	b, _ := Get("soap")

	expanded, _, _ := b.Emit(c, Options{})
	if !strings.Contains(string(expanded), "Type 2 Diabetes Mellitus") {
		t.Error("default rendering should expand abbreviations")
	}

	verbatim, _, _ := b.Emit(c, Options{Verbatim: true})
	if !strings.Contains(string(verbatim), "dm2 htn") {
		t.Error("verbatim rendering should keep the input as typed")
	}
	if strings.Contains(string(verbatim), "Type 2 Diabetes") {
		t.Error("verbatim rendering must not expand")
	}
}

func TestHideDerived(t *testing.T) {
	c, _ := build(t, "pt 34M wt70kg ht170cm\n")
	b, _ := Get("soap")

	shown, _, _ := b.Emit(c, Options{})
	if !strings.Contains(string(shown), "bmi") && !strings.Contains(string(shown), "Body mass") {
		t.Error("derived values should render by default")
	}
	hidden, _, _ := b.Emit(c, Options{HideDerived: true})
	if strings.Contains(string(hidden), "Body mass index") {
		t.Error("HideDerived should omit derived observations")
	}
}

// Backends are pure: identical input must produce byte-identical output, every
// time. Narratives live in a map, so a backend that ranged over it instead of
// using the fixed section order would fail here.
func TestBackendsAreDeterministic(t *testing.T) {
	c, diags := build(t, note)

	for _, name := range Names() {
		b, _ := Get(name)
		first, _, err := b.Emit(c, Options{Diagnostics: diags})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 40; i++ {
			got, _, err := b.Emit(c, Options{Diagnostics: diags})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(first) {
				t.Fatalf("%s: emit %d differed from the first", name, i)
			}
		}
	}
}

// Emit must not mutate the case it is given, or two backends run in sequence
// would disagree.
func TestEmitDoesNotMutateTheCase(t *testing.T) {
	c, _ := build(t, note)
	before, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range Names() {
		b, _ := Get(name)
		if _, _, err := b.Emit(c, Options{}); err != nil {
			t.Fatal(err)
		}
	}

	after, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a backend mutated the case")
	}
}

// Clinical text contains <, > and &. A chief complaint of "sats <90 on air"
// must survive JSON encoding as written.
func TestJSONDoesNotEscapeClinicalText(t *testing.T) {
	c, _ := build(t, "cc sats <90 on air & rising RR\n")
	b, _ := Get("json")
	out, _, _ := b.Emit(c, Options{})

	if !strings.Contains(string(out), "<90 on air & rising RR") {
		t.Errorf("JSON escaped clinical text:\n%s", out)
	}
}

func TestJSONRoundTrips(t *testing.T) {
	c, _ := build(t, note)
	b, _ := Get("json")
	out, _, err := b.Emit(c, Options{})
	if err != nil {
		t.Fatal(err)
	}

	var back ir.Case
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("emitted JSON does not parse: %v", err)
	}
	if back.Subject.ID != c.Subject.ID {
		t.Error("round trip lost the subject id")
	}
	if len(back.Encounters) != len(c.Encounters) {
		t.Error("round trip lost encounters")
	}
}

func TestEmptyCaseStillRenders(t *testing.T) {
	c, _ := build(t, "")
	for _, name := range Names() {
		b, _ := Get(name)
		out, _, err := b.Emit(c, Options{})
		if err != nil {
			t.Errorf("%s failed on an empty case: %v", name, err)
		}
		if len(out) == 0 {
			t.Errorf("%s produced no output for an empty case", name)
		}
	}
}

func TestUnknownBackend(t *testing.T) {
	if _, _, err := Render("fhir", &ir.Case{}, Options{}); err == nil {
		t.Error("expected an error for an unregistered backend")
	}
	if _, ok := Get("soap"); !ok {
		t.Error("soap should be registered")
	}
}

// Ordering is source order everywhere. Sorting findings or observations by
// anything else — grade, value, name — would be the engine deciding what
// matters, which is outside what ClinLang does.
func TestOrderingFollowsTheSource(t *testing.T) {
	c, _ := build(t, "sx zebra+ alpha+++ middle++\n")
	b, _ := Get("soap")
	out, _, _ := b.Emit(c, Options{})
	s := string(out)

	zi, ai, mi := strings.Index(s, "zebra"), strings.Index(s, "alpha"), strings.Index(s, "middle")
	if zi < 0 || ai < 0 || mi < 0 {
		t.Fatalf("findings missing from output:\n%s", s)
	}
	if !(zi < ai && ai < mi) {
		t.Error("findings were reordered; output must follow the source, " +
			"since any other order would rank them")
	}
}

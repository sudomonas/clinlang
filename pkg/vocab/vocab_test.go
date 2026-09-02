package vocab

import (
	"encoding/json"
	"strings"
	"testing"

	"clinlang/pkg/lexicon"
)

// The vocabulary loads and carries the terms the language deliberately omits.
func TestVocabularyLoads(t *testing.T) {
	l := Lexicon()

	if l.Drugs().Len() < 1000 {
		t.Errorf("drug list has %d entries, want the full formulary", l.Drugs().Len())
	}
	for _, key := range []string{"hb", "pco2", "hba1c", "bp"} {
		if _, ok := l.Measurement(key); !ok {
			t.Errorf("measurement %q missing", key)
		}
	}
	if _, ok := l.Abbreviation("dm2"); !ok {
		t.Error("abbreviation dm2 missing")
	}
	if _, ok := l.Finding("chest-pain"); !ok {
		t.Error("finding chest-pain missing")
	}
}

func TestEveryVocabularyFileIsValidJSON(t *testing.T) {
	for _, name := range FileNames {
		data := JSON(name)
		if len(data) == 0 {
			t.Errorf("%s: no embedded data", name)
			continue
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Errorf("%s: invalid JSON: %v", name, err)
		}
	}
}

func TestDrugTrieLongestMatch(t *testing.T) {
	l := Lexicon()
	tests := []struct {
		words []string
		want  string
		n     int
	}{
		{[]string{"amoxicillin", "500mg", "tds"}, "amoxicillin", 1},
		{[]string{"vitamin", "b12", "500mcg"}, "vitamin b12", 2},
		{[]string{"metoprolol", "25mg"}, "metoprolol", 1},
		{[]string{"notadrugatall", "5mg"}, "", 0},
	}
	for _, tc := range tests {
		got, n := l.MatchDrug(tc.words)
		if got != tc.want || n != tc.n {
			t.Errorf("MatchDrug(%v) = (%q, %d), want (%q, %d)", tc.words, got, n, tc.want, tc.n)
		}
	}
}

// A workspace layer takes precedence over the shipped vocabulary for the terms
// it defines, while still inheriting everything it does not.
func TestWorkspaceLayerOverridesVocabulary(t *testing.T) {
	custom := lexicon.Sources{
		Abbreviations: []byte(`{"dm2":"Local diabetes wording"}`),
	}
	l, err := lexicon.Load(custom, Sources())
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := l.Abbreviation("dm2"); got != "Local diabetes wording" {
		t.Errorf("dm2 = %q, want the workspace wording to win", got)
	}
	// Terms the override does not mention are still inherited.
	if got, _ := l.Abbreviation("htn"); got != "Hypertension" {
		t.Errorf("htn = %q, want the shipped wording", got)
	}
	if l.Drugs().Len() == 0 {
		t.Error("overriding abbreviations should not drop the drug list")
	}
}

func TestMeasurementKeysAreLengthSorted(t *testing.T) {
	keys := Lexicon().MeasurementKeys()
	if len(keys) == 0 {
		t.Fatal("no measurement keys")
	}
	for i := 1; i < len(keys); i++ {
		if len(keys[i]) > len(keys[i-1]) {
			t.Fatalf("keys are not length-sorted: %q before %q", keys[i-1], keys[i])
		}
	}
}

// Comment keys documenting the data files must never become vocabulary.
func TestCommentKeysAreIgnored(t *testing.T) {
	l := Lexicon()
	for _, k := range l.MeasurementKeys() {
		if strings.HasPrefix(k, "_") {
			t.Errorf("comment key %q leaked into the split keys", k)
		}
	}
	if _, ok := l.Finding("_run_together_comment"); ok {
		t.Error("a comment key leaked into the findings table")
	}
}

// The vocabulary names things. It must not describe what they should be.
func TestVocabularyCarriesNoClinicalJudgement(t *testing.T) {
	for _, name := range FileNames {
		body := strings.ToLower(string(JSON(name)))
		for _, banned := range []string{`"low":`, `"high":`, `"normal":`, `"critical":`,
			`"interaction`, `"contraindicat`, `"max_dose`, `"severity":`} {
			if strings.Contains(body, banned) {
				t.Errorf("%s contains %s: this is vocabulary, not clinical knowledge", name, banned)
			}
		}
	}
}

func BenchmarkDrugMatch(b *testing.B) {
	l := Lexicon()
	words := []string{"amoxicillin", "500mg", "tds", "po"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.MatchDrug(words)
	}
}

// Concept identity over the shipped vocabulary. These live here rather than in
// pkg/lexicon because the language ships no clinical terms to identify.
func TestVocabularyConceptsAreIdentified(t *testing.T) {
	l := Lexicon()

	expected := map[string]lexicon.ConceptKind{
		"measurement.hb":    lexicon.KindMeasurement,
		"measurement.bp":    lexicon.KindMeasurement,
		"measurement.pco2":  lexicon.KindMeasurement,
		"measurement.hba1c": lexicon.KindMeasurement,
		"abbr.dm2":          lexicon.KindAbbreviation,
		"finding.chestpain": lexicon.KindFinding,
	}
	for id, kind := range expected {
		c, ok := l.ConceptByID(id)
		if !ok {
			t.Errorf("concept %q does not exist", id)
			continue
		}
		if c.Kind != kind {
			t.Errorf("%q has kind %s, want %s", id, c.Kind, kind)
		}
	}
	if _, ok := l.ConceptByID("drug.vitamin_b12"); !ok {
		t.Error("multi-word drug did not produce a single-token ID")
	}
}

func TestResolvePhraseMatchesMultiWordDrugs(t *testing.T) {
	l := Lexicon()

	c, n := l.ResolvePhrase([]string{"vitamin", "b12", "500mcg"}, lexicon.KindDrug)
	if n != 2 || c.ID != "drug.vitamin_b12" {
		t.Errorf("ResolvePhrase = (%q, %d), want (drug.vitamin_b12, 2)", c.ID, n)
	}
	if _, n := l.ResolvePhrase([]string{"notadrug"}, lexicon.KindDrug); n != 0 {
		t.Error("unknown drug should consume no words")
	}
}

// Attributes travel with the concept, so a consumer holding the identity needs
// no second table for the detail.
func TestConceptAttributes(t *testing.T) {
	hb, ok := Lexicon().ConceptByID("measurement.hb")
	if !ok {
		t.Fatal("measurement.hb missing")
	}
	if hb.Attr("group") != "lab" || hb.Attr("unit") != "g/dL" {
		t.Errorf("hb attributes = %+v", hb.Attrs)
	}
	if hb.Display != "Haemoglobin" {
		t.Errorf("hb display = %q", hb.Display)
	}
}

// External terminology codes are absent by construction. The field exists so a
// licensed pack can populate it later without re-modelling anything.
func TestNoExternalCodesArePresent(t *testing.T) {
	for _, c := range Lexicon().Concepts() {
		if len(c.Codes) != 0 {
			t.Errorf("concept %q ships terminology codes %v; SNOMED, LOINC and "+
				"RxNorm are separately licensed and must not be embedded", c.ID, c.Codes)
		}
	}
}

package lexicon

import (
	"encoding/json"
	"strings"
	"testing"
)

// The language ships notation and no clinical vocabulary. This is the test
// that fails if medicine is ever added back to the core: a drug, an analyte or
// an abbreviation appearing here means the language has started to know
// medicine again.
func TestCoreShipsNotationAndNoMedicine(t *testing.T) {
	l, err := Load()
	if err != nil {
		t.Fatalf("core lexicon failed to load: %v", err)
	}

	// Notation is the language's own and must be present.
	if _, ok := l.Unit("kg"); !ok {
		t.Error("units are notation and belong to the language")
	}
	if _, ok := l.DurationUnit("wk"); !ok {
		t.Error("duration units are notation and belong to the language")
	}
	if _, ok := l.Intensity("+++"); !ok {
		t.Error("intensity markers are notation and belong to the language")
	}
	if _, ok := l.Frequency("tds"); !ok {
		t.Error("dosing notation belongs to the language")
	}
	if _, ok := l.Route("po"); !ok {
		t.Error("route notation belongs to the language")
	}

	// Clinical vocabulary must not be.
	if n := l.drugs.Len(); n != 0 {
		t.Errorf("core ships %d drug names; medicine belongs in a plugin", n)
	}
	if n := len(l.measurements); n != 0 {
		t.Errorf("core ships %d analytes; medicine belongs in a plugin", n)
	}
	if n := len(l.findings); n != 0 {
		t.Errorf("core ships %d findings; medicine belongs in a plugin", n)
	}
	if n := len(l.abbreviations); n != 0 {
		t.Errorf("core ships %d abbreviations; medicine belongs in a plugin", n)
	}
}

// Every embedded file must be valid JSON, since Default panics otherwise and
// that panic would take down a server on first parse.
func TestEveryEmbeddedFileIsValidJSON(t *testing.T) {
	for _, name := range FileNames {
		data := DefaultJSON(name)
		if len(data) == 0 {
			continue // supplied by a plugin, not by the language
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Errorf("%s: invalid JSON: %v", name, err)
		}
	}
}

// PRN carries as-needed-ness and no schedule; TDS carries a schedule and is not
// as-needed. Keeping them in separate fields is what stops "qds prn" from
// losing the four-times-daily ceiling the way the previous engine did.
func TestPRNIsNotAFrequency(t *testing.T) {
	l := Default()

	prn, ok := l.Frequency("prn")
	if !ok {
		t.Fatal("prn did not resolve")
	}
	if !prn.AsNeeded {
		t.Error("prn must set AsNeeded")
	}
	if prn.HasTiming() {
		t.Error("prn must not carry a schedule that could overwrite a frequency")
	}

	tds, ok := l.Frequency("tds")
	if !ok {
		t.Fatal("tds did not resolve")
	}
	if tds.AsNeeded {
		t.Error("tds must not set AsNeeded")
	}
	if tds.Count != 3 || tds.Period != 1 || tds.Unit != "d" {
		t.Errorf("tds timing = %d per %g%s, want 3 per 1d", tds.Count, tds.Period, tds.Unit)
	}
}

func TestFrequencyAliases(t *testing.T) {
	l := Default()
	pairs := map[string]string{
		"tds": "TDS", "tid": "TDS",
		"bd": "BD", "bid": "BD", "twice": "BD",
		"qds": "QDS", "qid": "QDS",
		"od": "OD", "qd": "OD", "daily": "OD",
		"nocte": "Nocte", "hs": "Nocte",
		"stat": "STAT",
	}
	for alias, code := range pairs {
		f, ok := l.Frequency(alias)
		if !ok {
			t.Errorf("%q did not resolve", alias)
			continue
		}
		if f.Code != code {
			t.Errorf("%q resolved to %q, want %q", alias, f.Code, code)
		}
	}
}

// The previous route tables were asymmetric: some aliases produced a code and
// others a display string, leaving unreachable expansion entries. Every alias
// must now reach a code, and every code must have a display form.
func TestRoutesAreSymmetric(t *testing.T) {
	l := Default()

	for _, alias := range l.RouteAliases() {
		r, ok := l.Route(alias)
		if !ok {
			t.Errorf("alias %q did not resolve", alias)
			continue
		}
		if r.Code == "" || r.Display == "" {
			t.Errorf("alias %q resolved to an incomplete route %+v", alias, r)
			continue
		}
		// Round trip: the code the alias produced must itself resolve.
		back, ok := l.RouteByCode(r.Code)
		if !ok {
			t.Errorf("code %q from alias %q does not resolve", r.Code, alias)
			continue
		}
		if back.Display != r.Display {
			t.Errorf("code %q display %q disagrees with alias %q display %q",
				r.Code, back.Display, alias, r.Display)
		}
	}

	// The specific case that used to work only by accident.
	if r, _ := l.Route("top"); r.Code != "TOP" || r.Display != "Topically" {
		t.Errorf("top resolved to %+v, want TOP/Topically", r)
	}
}

// Units replace character trimming. The old code ran
// strings.TrimRight(numPart, "cmCM"), which strips characters in a set rather
// than a suffix, so "ht1.7m" became a height of 1.7 cm.
func TestUnitsReplaceCharacterTrimming(t *testing.T) {
	l := Default()

	m, ok := l.Unit("m")
	if !ok {
		t.Fatal("metre did not resolve")
	}
	if m.Dimension != "length" {
		t.Errorf("m dimension = %q, want length", m.Dimension)
	}
	if got := m.ToBase(1.7); got != 170 {
		t.Errorf("1.7m converted to %g cm, want 170", got)
	}

	kg, _ := l.Unit("kg")
	if got := kg.ToBase(65); got != 65 {
		t.Errorf("65kg converted to %g, want 65", got)
	}

	g, _ := l.Unit("g")
	if got := g.ToBase(1000); got != 1 {
		t.Errorf("1000g converted to %g kg, want 1", got)
	}
}

func TestTemperatureConversion(t *testing.T) {
	l := Default()
	f, ok := l.Unit("f")
	if !ok {
		t.Fatal("fahrenheit did not resolve")
	}
	got := f.ToBase(98.6)
	if got < 36.9 || got > 37.1 {
		t.Errorf("98.6F converted to %g C, want about 37", got)
	}
}

func TestCompoundUnits(t *testing.T) {
	l := Default()
	for alias, want := range map[string]string{
		"g/dl": "g/dL", "mmol/l": "mmol/L", "u/l": "U/L", "mg/kg": "mg/kg",
	} {
		u, ok := l.Unit(alias)
		if !ok {
			t.Errorf("%q did not resolve", alias)
			continue
		}
		if u.Canonical != want {
			t.Errorf("%q resolved to %q, want %q", alias, u.Canonical, want)
		}
	}
}

func TestTrieBasics(t *testing.T) {
	tr := NewWordTrie()
	tr.InsertAll([]string{"vitamin b12", "vitamin d", "vitamin", "aspirin"})

	if tr.Len() != 4 {
		t.Errorf("Len = %d, want 4", tr.Len())
	}
	// Longest match, not first match.
	if v, n := tr.LongestMatch([]string{"vitamin", "b12", "500mcg"}); v != "vitamin b12" || n != 2 {
		t.Errorf("LongestMatch = (%q, %d), want (vitamin b12, 2)", v, n)
	}
	// Falls back to the shorter entry when the longer does not match.
	if v, n := tr.LongestMatch([]string{"vitamin", "c"}); v != "vitamin" || n != 1 {
		t.Errorf("LongestMatch = (%q, %d), want (vitamin, 1)", v, n)
	}
	// Whole words only: a trie holding "vitamin" must not match "vitaminx".
	if _, n := tr.LongestMatch([]string{"vitaminx"}); n != 0 {
		t.Error("trie matched a partial word")
	}
	if !tr.Contains("Vitamin B12") {
		t.Error("Contains should be case-insensitive")
	}
	if tr.Contains("vitamin b") {
		t.Error("Contains should require an exact phrase")
	}
}

func TestTriePrefixSearchIsDeterministic(t *testing.T) {
	tr := NewWordTrie()
	tr.InsertAll([]string{
		"amoxicillin", "amoxicillin clavulanate", "amlodipine", "amiodarone",
		"atorvastatin", "aspirin",
	})

	first := tr.PrefixSearch("am", 10)
	if len(first) != 4 {
		t.Fatalf("PrefixSearch(am) returned %v, want 4 results", first)
	}
	for i := 0; i < 50; i++ {
		got := tr.PrefixSearch("am", 10)
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("PrefixSearch is not deterministic: %v vs %v", got, first)
		}
	}

	if got := tr.PrefixSearch("am", 2); len(got) != 2 {
		t.Errorf("limit not honoured: %v", got)
	}
}

// A broken override is an error, not a silent fallback. A clinic whose
// drugs.json is malformed must be told rather than quietly given stock
// behaviour while believing its own list is active.
func TestMalformedOverrideIsAnError(t *testing.T) {
	if _, err := Load(Sources{Drugs: []byte(`{"not":"an array"}`)}); err == nil {
		t.Error("expected an error for malformed drugs.json")
	}
	if _, err := Load(Sources{Frequencies: []byte(`{{{`)}); err == nil {
		t.Error("expected an error for malformed frequencies.json")
	}
}

// The engine records values and never evaluates them. Nothing in the lexicon
// may describe what a value ought to be.
func TestNoReferenceRangeDataExists(t *testing.T) {
	for _, name := range FileNames {
		body := strings.ToLower(string(DefaultJSON(name)))
		for _, banned := range []string{`"low"`, `"high"`, `"normal_range"`, `"reference_range"`, `"critical"`} {
			// The explanatory comments mention that ranges are absent, so only
			// flag these as JSON keys carrying values.
			if strings.Contains(body, banned+":") {
				t.Errorf("%s contains %s: the engine must not evaluate clinical values", name, banned)
			}
		}
	}
}

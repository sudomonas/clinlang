// Package lexicon holds ClinLang's clinical vocabulary and the lookups over it.
//
// It is the single place that knows medicine. The lexer knows shapes and the
// parser knows grammar; both are vocabulary-free, so neither can accumulate the
// hardcoded escape lists that the previous engine grew. Everything a stage
// needs to interpret clinical text — abbreviations, drug names, dosing
// frequencies, routes, units, measurement keys — is loaded from here.
//
// # Authoring format and runtime form
//
// The data is authored as JSON and embedded in the binary, keeping the
// single-file, zero-dependency deployment intact. At load time it is compiled
// into the forms each lookup needs: a word trie for phrase matching, flattened
// alias maps for single-token lookup, and a length-sorted key list for
// deterministic longest-prefix splitting. Users override any file through the
// existing .config mechanism.
//
// # What is deliberately absent
//
// There are no reference ranges and no notion of a normal, high or low value.
// ClinLang records and renders what a clinician typed; it does not evaluate it.
// There is also no terminology binding — no SNOMED, LOINC or RxNorm codes —
// because those are separately licensed and cannot be embedded in an
// MIT-licensed binary. The IR carries the fields for them so a future
// terminology pack is additive rather than a redesign.
package lexicon

import (
	"strings"
)

// Frequency is a dosing frequency with its display form and structured timing.
//
// AsNeeded is a distinct field, not a frequency value. That separation is the
// fix for the previous engine's PRN defect: "qds prn" set QDS and then
// overwrote it with PRN, so the four-times-daily ceiling never reached the
// note. Frequency and as-needed-ness are orthogonal, and a prescription can
// carry both.
type Frequency struct {
	Code     string   // canonical code, e.g. "TDS"
	Display  string   // human form, e.g. "Three times daily"
	Count    int      // administrations per period; 0 when unspecified
	Period   float64  // length of the period
	Unit     string   // period unit: h, d, wk, mo
	When     []string // time-of-day markers, e.g. NIGHT
	Priority string   // "stat" for immediate orders
	AsNeeded bool     // true only for PRN and its aliases
}

// HasTiming reports whether the entry carries a structured schedule.
func (f Frequency) HasTiming() bool { return f.Count > 0 && f.Period > 0 }

// Route is a route of administration.
type Route struct {
	Code    string // canonical code, e.g. "PO"
	Display string // human form, e.g. "Orally"
}

// Unit is a unit of measure within a dimension.
//
// Factor and Offset convert a value to the dimension's base unit. Conversion is
// offered to callers but never applied silently: a temperature entered in
// Fahrenheit stays Fahrenheit in both the record and the note.
type Unit struct {
	Canonical string
	Dimension string
	Factor    float64
	Offset    float64
}

// ToBase converts v, expressed in this unit, to the dimension's base unit.
func (u Unit) ToBase(v float64) float64 { return v*u.Factor + u.Offset }

// Measurement describes a known measurement key.
//
// Unit is the unit conventionally implied when the clinician writes none. It
// exists so a note can render "13.5 g/dL" instead of a bare number; it is not
// a validation rule and nothing checks the value against anything.
type Measurement struct {
	Key         string
	Display     string
	Group       string // vitals, patient, lab
	Shape       string // "", "ratio", "integer"
	Unit        string
	Dimension   string
	DefaultUnit string
}

// DurationUnit is a time unit with its display words.
type DurationUnit struct {
	Short   string
	Word    string
	Aliases []string
}

// Lexicon is the loaded vocabulary.
//
// A Lexicon is immutable once built and safe for concurrent reads. Each parse
// takes the one it was given rather than reaching for a process global, which
// is what makes concurrent parses in the hosted server safe — the previous
// engine's package-level Abbreviations map was shared mutable state.
type Lexicon struct {
	abbreviations map[string]string
	intensities   map[string]string
	serology      map[string]string
	findings      map[string]string
	radKeys       []string

	frequencies map[string]Frequency // alias -> frequency
	freqByCode  map[string]Frequency
	routes      map[string]Route // alias -> route
	routeByCode map[string]Route

	units     map[string]Unit // alias -> unit
	compound  map[string]string
	durations map[string]DurationUnit // alias -> unit

	measurements map[string]Measurement
	splitKeys    []string // length-sorted, for deterministic longest match

	drugs *WordTrie

	// concepts is the symbol table: one identity per clinical thing, which
	// every later stage refers to instead of passing bare strings around.
	concepts *conceptTable
}

// Abbreviation returns the expansion of an abbreviation.
//
// The engine stores what the clinician typed and expands only at render time,
// so this is called by backends, never by the parser.
func (l *Lexicon) Abbreviation(s string) (string, bool) {
	v, ok := l.abbreviations[strings.ToLower(s)]
	return v, ok
}

// Intensity returns the symptom label for a qualifier run such as "+++".
func (l *Lexicon) Intensity(qualifier string) (string, bool) {
	v, ok := l.intensities[qualifier]
	return v, ok
}

// Finding returns the display label for a symptom or examination term.
//
// Absence is not an error: unlisted terms are accepted and rendered as typed.
func (l *Lexicon) Finding(term string) (string, bool) {
	v, ok := l.findings[strings.ToLower(term)]
	return v, ok
}

// Serology returns the investigation label for a qualifier run.
//
// The same run means different things in different statements: on a symptom
// "-" is a trend and reads "improving", while on a result it is an outcome and
// reads "negative". Applying the symptom vocabulary to a lab line would render
// "HIV improving", which is both wrong and alarming, so the caller chooses the
// vocabulary from the statement it is in.
func (l *Lexicon) Serology(qualifier string) (string, bool) {
	v, ok := l.serology[qualifier]
	return v, ok
}

// Frequency resolves a dosing alias.
func (l *Lexicon) Frequency(alias string) (Frequency, bool) {
	v, ok := l.frequencies[strings.ToLower(alias)]
	return v, ok
}

// FrequencyByCode resolves a canonical frequency code.
func (l *Lexicon) FrequencyByCode(code string) (Frequency, bool) {
	v, ok := l.freqByCode[strings.ToUpper(code)]
	if !ok {
		// Codes are mixed case in the source data, e.g. "Nocte".
		v, ok = l.freqByCode[code]
	}
	return v, ok
}

// Route resolves a route alias.
func (l *Lexicon) Route(alias string) (Route, bool) {
	v, ok := l.routes[strings.ToLower(alias)]
	return v, ok
}

// RouteByCode resolves a canonical route code.
func (l *Lexicon) RouteByCode(code string) (Route, bool) {
	v, ok := l.routeByCode[strings.ToUpper(code)]
	return v, ok
}

// Unit resolves a unit alias, including compound forms such as mg/dL.
func (l *Lexicon) Unit(alias string) (Unit, bool) {
	lower := strings.ToLower(alias)
	if u, ok := l.units[lower]; ok {
		return u, true
	}
	if canon, ok := l.compound[lower]; ok {
		return Unit{Canonical: canon, Dimension: "compound", Factor: 1}, true
	}
	return Unit{}, false
}

// DurationUnit resolves a duration alias such as "w" or "weeks".
func (l *Lexicon) DurationUnit(alias string) (DurationUnit, bool) {
	v, ok := l.durations[strings.ToLower(alias)]
	return v, ok
}

// Measurement resolves a measurement key.
func (l *Lexicon) Measurement(key string) (Measurement, bool) {
	v, ok := l.measurements[strings.ToLower(key)]
	return v, ok
}

// IsRadiologyKey reports whether a key routes to imaging rather than labs.
func (l *Lexicon) IsRadiologyKey(key string) bool {
	lower := strings.ToLower(key)
	for _, r := range l.radKeys {
		if strings.HasPrefix(lower, r) {
			return true
		}
	}
	return false
}

// Drugs returns the drug-name trie.
func (l *Lexicon) Drugs() *WordTrie { return l.drugs }

// MatchDrug finds the longest drug name at the start of words, returning the
// canonical name and how many words it consumed.
func (l *Lexicon) MatchDrug(words []string) (string, int) {
	return l.drugs.LongestMatch(words)
}

// MeasurementKeys returns every known key, length-sorted longest first.
//
// The lexicon supplies key data; the rule for applying it lives in the parser's
// Grammar, which is its single implementation. Two copies of a splitting rule
// is how "cl" came to split "clopidogrel" while the lexicon's own copy declined
// to — the defect class this engine exists to remove.
func (l *Lexicon) MeasurementKeys() []string {
	out := make([]string, len(l.splitKeys))
	copy(out, l.splitKeys)
	return out
}

// AbbreviationKeys returns every abbreviation, sorted, for completions.
func (l *Lexicon) AbbreviationKeys() []string {
	return sortedKeys(l.abbreviations)
}

// FrequencyAliases returns every dosing alias, sorted, for completions.
func (l *Lexicon) FrequencyAliases() []string { return sortedKeys(l.frequencies) }

// RouteAliases returns every route alias, sorted, for completions.
func (l *Lexicon) RouteAliases() []string { return sortedKeys(l.routes) }

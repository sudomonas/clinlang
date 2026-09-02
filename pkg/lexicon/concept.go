package lexicon

import (
	"sort"
	"strconv"
	"strings"
)

// ConceptKind classifies what a concept is.
//
// Kind is what makes resolution context-sensitive without being ambiguous: the
// word "po" is a route in a prescription and could be something else elsewhere,
// so callers resolve with the kinds that are valid where they stand rather than
// searching one flat namespace.
type ConceptKind uint8

const (
	KindUnknown ConceptKind = iota
	KindDrug
	KindMeasurement // an analyte or vital sign: hb, bp, spo2
	KindRoute
	KindFrequency
	KindAbbreviation // expandable shorthand: dm2, htn
	KindImaging
	KindIntensity // a qualifier run: +++, --
	KindFinding   // a symptom or examination finding: chest-pain, crepitations
)

var kindNames = map[ConceptKind]string{
	KindUnknown:      "unknown",
	KindDrug:         "drug",
	KindMeasurement:  "measurement",
	KindRoute:        "route",
	KindFrequency:    "freq",
	KindAbbreviation: "abbr",
	KindImaging:      "imaging",
	KindIntensity:    "intensity",
	KindFinding:      "finding",
}

// String returns the kind's short name, which is also its ID prefix.
func (k ConceptKind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	return "unknown"
}

// Concept is a single clinical thing with a stable identity.
//
// The ID is ClinLang's own, not a terminology code: "drug.metformin",
// "measurement.hb", "route.po". It is stable across releases, greppable, and
// independent of any external vocabulary, which matters because the external
// vocabularies are separately licensed and may never be present.
//
// Identity is what the previous design lacked. Storing the string "metformin"
// gave no way to know that "met" and "glucophage" name the same thing, no join
// key for a plugin to attach data to, and nowhere for a future SNOMED or RxNorm
// code to attach without re-modelling. A concept gives all three.
//
// # Relationship to external codes
//
// Codes maps a terminology system to its code for this concept and is empty in
// every shipped build. When a licensed pack is eventually loaded, it populates
// this map; nothing else in the engine changes, because every consumer already
// refers to concepts by ID.
type Concept struct {
	// ID is the stable identifier, formed as "<kind>.<canonical>".
	ID string

	Kind ConceptKind

	// Canonical is the preferred lowercase form used to build the ID.
	Canonical string

	// Display is the human-readable form rendered in notes. Empty means render
	// the text the clinician typed.
	Display string

	// Aliases are every accepted input form, lowercased, including Canonical.
	Aliases []string

	// Codes holds external terminology codes, keyed by system. Always empty
	// today. See the type documentation.
	Codes map[string]string

	// Attrs carries kind-specific detail: a measurement's unit and group, a
	// frequency's schedule, a route's code.
	Attrs map[string]string
}

// IsZero reports whether c is the empty concept.
func (c Concept) IsZero() bool { return c.ID == "" }

// Label returns Display when set, otherwise Canonical.
func (c Concept) Label() string {
	if c.Display != "" {
		return c.Display
	}
	return c.Canonical
}

// Attr returns a kind-specific attribute.
func (c Concept) Attr(key string) string { return c.Attrs[key] }

// makeID builds the stable identifier for a concept.
//
// Whitespace becomes underscores so multi-word drug names produce a single
// token: "vitamin b12" yields "drug.vitamin_b12".
func makeID(kind ConceptKind, canonical string) string {
	return kind.String() + "." + strings.ReplaceAll(strings.ToLower(canonical), " ", "_")
}

// ─────────────────────────────────────────────────────────────────────────────
// Symbol table
// ─────────────────────────────────────────────────────────────────────────────

// concepts is the lexicon's symbol table.
//
// Two indexes: by ID for direct reference, and by (kind, alias) for resolution
// from input text. Resolution is deliberately not one flat namespace — "m" is a
// duration unit and could be a route abbreviation, and a flat table would make
// which one wins depend on load order.
type conceptTable struct {
	byID    map[string]Concept
	byAlias map[ConceptKind]map[string]string // kind -> alias -> ID
	ordered []string                          // IDs, sorted, for deterministic listing
}

func newConceptTable() *conceptTable {
	return &conceptTable{
		byID:    make(map[string]Concept),
		byAlias: make(map[ConceptKind]map[string]string),
	}
}

// add registers a concept, keeping the first registration of any ID.
//
// First-wins lets a user override load before the defaults and take precedence
// without a separate merge pass.
func (t *conceptTable) add(c Concept) {
	if c.ID == "" {
		return
	}
	if _, exists := t.byID[c.ID]; exists {
		return
	}
	t.byID[c.ID] = c
	t.ordered = append(t.ordered, c.ID)

	m, ok := t.byAlias[c.Kind]
	if !ok {
		m = make(map[string]string)
		t.byAlias[c.Kind] = m
	}
	for _, a := range c.Aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if _, taken := m[a]; !taken {
			m[a] = c.ID
		}
	}
}

func (t *conceptTable) finish() { sort.Strings(t.ordered) }

// ─────────────────────────────────────────────────────────────────────────────
// Lexicon API
// ─────────────────────────────────────────────────────────────────────────────

// ConceptByID returns a concept by its stable identifier.
func (l *Lexicon) ConceptByID(id string) (Concept, bool) {
	if l == nil || l.concepts == nil {
		return Concept{}, false
	}
	c, ok := l.concepts.byID[id]
	return c, ok
}

// Resolve looks up input text as a concept of one of the given kinds.
//
// Kinds are tried in the order supplied, so the caller expresses precedence for
// its own context: a prescription resolves a bare word as a frequency before a
// route before a drug, because that is the order in which the ambiguity should
// be settled there. Passing no kinds searches every kind in a fixed order.
//
// This is the symbol-table lookup that keeps clinical knowledge out of the
// parser. The parser records that "tds" is a word; sema asks what it means.
func (l *Lexicon) Resolve(text string, kinds ...ConceptKind) (Concept, bool) {
	if l == nil || l.concepts == nil {
		return Concept{}, false
	}
	key := strings.ToLower(strings.TrimSpace(text))
	if key == "" {
		return Concept{}, false
	}

	if len(kinds) == 0 {
		kinds = allKinds
	}
	for _, k := range kinds {
		m, ok := l.concepts.byAlias[k]
		if !ok {
			continue
		}
		if id, ok := m[key]; ok {
			return l.concepts.byID[id], true
		}
	}
	return Concept{}, false
}

// ResolvePhrase resolves the longest run of words that names a concept of the
// given kind, returning the concept and how many words it consumed.
//
// Only drugs are indexed as phrases today, since they are the only multi-word
// names in the vocabulary.
func (l *Lexicon) ResolvePhrase(words []string, kind ConceptKind) (Concept, int) {
	if l == nil || len(words) == 0 {
		return Concept{}, 0
	}
	if kind != KindDrug {
		if c, ok := l.Resolve(words[0], kind); ok {
			return c, 1
		}
		return Concept{}, 0
	}

	name, n := l.drugs.LongestMatch(words)
	if n == 0 {
		return Concept{}, 0
	}
	c, ok := l.ConceptByID(makeID(KindDrug, name))
	if !ok {
		return Concept{}, 0
	}
	return c, n
}

// Concepts returns every concept of the given kinds, ordered by ID.
//
// Ordering is stable so completions and documentation never depend on map
// iteration order.
func (l *Lexicon) Concepts(kinds ...ConceptKind) []Concept {
	if l == nil || l.concepts == nil {
		return nil
	}
	want := make(map[ConceptKind]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}

	var out []Concept
	for _, id := range l.concepts.ordered {
		c := l.concepts.byID[id]
		if len(kinds) == 0 || want[c.Kind] {
			out = append(out, c)
		}
	}
	return out
}

// ConceptCount returns the number of registered concepts.
func (l *Lexicon) ConceptCount() int {
	if l == nil || l.concepts == nil {
		return 0
	}
	return len(l.concepts.byID)
}

var allKinds = []ConceptKind{
	KindMeasurement, KindFrequency, KindRoute, KindDrug,
	KindImaging, KindFinding, KindAbbreviation, KindIntensity,
}

// buildConcepts projects the loaded tables into the symbol table.
//
// The JSON files stay the authoring format — they are readable and a clinic can
// edit them — while concepts are the runtime form every later stage uses. One
// authoring source, one queryable identity model.
func (l *Lexicon) buildConcepts() {
	t := newConceptTable()

	for key, m := range l.measurements {
		kind := KindMeasurement
		attrs := map[string]string{"group": m.Group}
		if m.Unit != "" {
			attrs["unit"] = m.Unit
		}
		if m.Dimension != "" {
			attrs["dimension"] = m.Dimension
		}
		if m.DefaultUnit != "" {
			attrs["default_unit"] = m.DefaultUnit
		}
		if m.Shape != "" {
			attrs["shape"] = m.Shape
		}
		t.add(Concept{
			ID:        makeID(kind, key),
			Kind:      kind,
			Canonical: key,
			Display:   m.Display,
			Aliases:   []string{key},
			Attrs:     attrs,
		})
	}

	// Frequencies and routes are keyed by code, so gather each code's aliases.
	freqAliases := invert(l.frequencies, func(f Frequency) string { return f.Code })
	for code, f := range l.freqByCode {
		if code != f.Code {
			continue // freqByCode holds both original and upper-cased keys
		}
		attrs := map[string]string{"code": f.Code}
		if f.AsNeeded {
			attrs["as_needed"] = "true"
		}
		if f.Priority != "" {
			attrs["priority"] = f.Priority
		}
		t.add(Concept{
			ID:        makeID(KindFrequency, f.Code),
			Kind:      KindFrequency,
			Canonical: strings.ToLower(f.Code),
			Display:   f.Display,
			Aliases:   freqAliases[f.Code],
			Attrs:     attrs,
		})
	}

	routeAliases := invert(l.routes, func(r Route) string { return r.Code })
	for code, r := range l.routeByCode {
		if !strings.EqualFold(code, r.Code) {
			continue
		}
		t.add(Concept{
			ID:        makeID(KindRoute, r.Code),
			Kind:      KindRoute,
			Canonical: strings.ToLower(r.Code),
			Display:   r.Display,
			Aliases:   routeAliases[r.Code],
			Attrs:     map[string]string{"code": r.Code},
		})
	}

	for term, display := range l.findings {
		t.add(Concept{
			ID:        makeID(KindFinding, term),
			Kind:      KindFinding,
			Canonical: term,
			Display:   display,
			Aliases:   []string{term},
		})
	}

	for abbr, expansion := range l.abbreviations {
		t.add(Concept{
			ID:        makeID(KindAbbreviation, abbr),
			Kind:      KindAbbreviation,
			Canonical: abbr,
			Display:   expansion,
			Aliases:   []string{abbr},
		})
	}

	for _, key := range l.radKeys {
		t.add(Concept{
			ID:        makeID(KindImaging, key),
			Kind:      KindImaging,
			Canonical: key,
			Aliases:   []string{key},
		})
	}

	for symbol, label := range l.intensities {
		t.add(Concept{
			ID:        makeID(KindIntensity, sanitiseSymbol(symbol)),
			Kind:      KindIntensity,
			Canonical: symbol,
			Display:   label,
			Aliases:   []string{symbol},
		})
	}

	// Drug concepts mirror the trie so a matched name resolves to an identity.
	l.drugs.eachPhrase(func(name string) {
		t.add(Concept{
			ID:        makeID(KindDrug, name),
			Kind:      KindDrug,
			Canonical: strings.ToLower(name),
			Display:   name,
			Aliases:   []string{strings.ToLower(name)},
		})
	})

	t.finish()
	l.concepts = t
}

// sanitiseSymbol turns a qualifier run into an ID-safe token: "+++" becomes
// "plus3" and "--" becomes "minus2".
func sanitiseSymbol(sym string) string {
	if sym == "" {
		return "none"
	}
	name := "plus"
	if sym[0] == '-' {
		name = "minus"
	}
	return name + strconv.Itoa(len(sym))
}

// invert groups alias keys by the canonical code their value carries.
func invert[T any](m map[string]T, code func(T) string) map[string][]string {
	out := make(map[string][]string, len(m))
	for alias, v := range m {
		c := code(v)
		out[c] = append(out[c], alias)
	}
	for c := range out {
		sort.Strings(out[c])
	}
	return out
}

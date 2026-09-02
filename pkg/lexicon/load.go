package lexicon

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed data/symptoms.json
var defaultIntensities []byte

//go:embed data/serology.json
var defaultSerology []byte

//go:embed data/durations.json
var defaultDurations []byte

//go:embed data/frequencies.json
var defaultFrequencies []byte

//go:embed data/routes.json
var defaultRoutes []byte

//go:embed data/units.json
var defaultUnits []byte

// Sources holds raw JSON for each lexicon file.
//
// A nil entry means "use the embedded default". Callers override individual
// files by setting only those fields, which is how the per-workspace .config
// directory works: a clinic supplying its own drug list keeps the stock
// abbreviations.
type Sources struct {
	Abbreviations []byte
	Intensities   []byte
	Serology      []byte
	Findings      []byte
	RadKeys       []byte
	Durations     []byte
	Frequencies   []byte
	Routes        []byte
	Units         []byte
	Measurements  []byte
	Drugs         []byte
}

// FileNames lists the lexicon files a workspace may override.
var FileNames = []string{
	// Notation, owned by the language.
	"symptoms.json",
	"serology.json",
	"durations.json",
	"frequencies.json",
	"routes.json",
	"units.json",

	// Clinical vocabulary, owned by plugins. Listed so a workspace can
	// override what a plugin supplies through the same mechanism.
	"abbreviations.json",
	"findings.json",
	"measurements.json",
	"rad_keys.json",
	"drugs.json",
}

// DefaultJSON returns the embedded contents of a lexicon file, or nil.
//
// The settings UI uses this to show a user what they are overriding.
func DefaultJSON(name string) []byte {
	switch name {
	case "symptoms.json":
		return defaultIntensities
	case "serology.json":
		return defaultSerology
	case "durations.json":
		return defaultDurations
	case "frequencies.json":
		return defaultFrequencies
	case "routes.json":
		return defaultRoutes
	case "units.json":
		return defaultUnits
	}
	return nil
}

// Set assigns raw JSON by file name. Unknown names are ignored.
func (s *Sources) Set(name string, data []byte) {
	switch name {
	case "abbreviations.json":
		s.Abbreviations = data
	case "symptoms.json":
		s.Intensities = data
	case "serology.json":
		s.Serology = data
	case "findings.json":
		s.Findings = data
	case "rad_keys.json":
		s.RadKeys = data
	case "durations.json":
		s.Durations = data
	case "frequencies.json":
		s.Frequencies = data
	case "routes.json":
		s.Routes = data
	case "units.json":
		s.Units = data
	case "measurements.json":
		s.Measurements = data
	case "drugs.json":
		s.Drugs = data
	}
}

// Get returns the raw JSON this layer supplies for a file name, or nil.
func (s Sources) Get(name string) []byte {
	switch name {
	case "abbreviations.json":
		return s.Abbreviations
	case "symptoms.json":
		return s.Intensities
	case "serology.json":
		return s.Serology
	case "findings.json":
		return s.Findings
	case "rad_keys.json":
		return s.RadKeys
	case "durations.json":
		return s.Durations
	case "frequencies.json":
		return s.Frequencies
	case "routes.json":
		return s.Routes
	case "units.json":
		return s.Units
	case "measurements.json":
		return s.Measurements
	case "drugs.json":
		return s.Drugs
	}
	return nil
}

var (
	defaultOnce sync.Once
	defaultLex  *Lexicon
	defaultErr  error
)

// Default returns the lexicon built from the embedded data.
//
// It is built once and shared. The result is immutable, so sharing it across
// concurrent parses is safe — unlike the previous engine's mutable package-level
// Abbreviations map.
func Default() *Lexicon {
	defaultOnce.Do(func() {
		defaultLex, defaultErr = Load(Sources{})
	})
	if defaultErr != nil {
		// Embedded data is validated by a test; reaching here means the binary
		// was built with corrupt assets.
		panic("lexicon: embedded data is invalid: " + defaultErr.Error())
	}
	return defaultLex
}

// Load builds a Lexicon, using embedded defaults for any absent source.
//
// A malformed override is an error rather than a silent fallback: a clinic that
// has broken its drugs.json should be told, not quietly given stock behaviour
// while believing its own list is active.
func Load(layers ...Sources) (*Lexicon, error) {
	l := &Lexicon{
		abbreviations: map[string]string{},
		intensities:   map[string]string{},
		serology:      map[string]string{},
		findings:      map[string]string{},
		frequencies:   map[string]Frequency{},
		freqByCode:    map[string]Frequency{},
		routes:        map[string]Route{},
		routeByCode:   map[string]Route{},
		units:         map[string]Unit{},
		compound:      map[string]string{},
		durations:     map[string]DurationUnit{},
		measurements:  map[string]Measurement{},
		drugs:         NewWordTrie(),
	}

	// Layers are applied in order and merge additively: a later layer adds
	// entries and, where a key collides, the earlier layer wins. That is what
	// lets several specialty plugins each contribute vocabulary without any of
	// them replacing another's.
	pick := func(get func(Sources) []byte, fallback []byte) [][]byte {
		var out [][]byte
		if len(fallback) > 0 {
			out = append(out, fallback)
		}
		for _, l := range layers {
			if data := get(l); len(data) > 0 {
				out = append(out, data)
			}
		}
		return out
	}

	each := func(datas [][]byte, load func([]byte) error) error {
		for _, d := range datas {
			if err := load(d); err != nil {
				return err
			}
		}
		return nil
	}

	type step struct {
		get      func(Sources) []byte
		fallback []byte
		load     func([]byte) error
	}
	steps := []step{
		// Notation: shipped by the language itself.
		{func(s Sources) []byte { return s.Intensities }, defaultIntensities, l.loadIntensities},
		{func(s Sources) []byte { return s.Serology }, defaultSerology, l.loadSerology},
		{func(s Sources) []byte { return s.Durations }, defaultDurations, l.loadDurations},
		{func(s Sources) []byte { return s.Frequencies }, defaultFrequencies, l.loadFrequencies},
		{func(s Sources) []byte { return s.Routes }, defaultRoutes, l.loadRoutes},
		{func(s Sources) []byte { return s.Units }, defaultUnits, l.loadUnits},

		// Clinical vocabulary: no fallback, because the language ships none.
		{func(s Sources) []byte { return s.Abbreviations }, nil, l.loadAbbreviations},
		{func(s Sources) []byte { return s.Findings }, nil, l.loadFindings},
		{func(s Sources) []byte { return s.Measurements }, nil, l.loadMeasurements},
		{func(s Sources) []byte { return s.RadKeys }, nil, l.loadRadKeys},
		{func(s Sources) []byte { return s.Drugs }, nil, l.loadDrugs},
	}
	for _, st := range steps {
		if err := each(pick(st.get, st.fallback), st.load); err != nil {
			return nil, err
		}
	}

	l.buildSplitKeys()
	l.buildConcepts()
	return l, nil
}

func (l *Lexicon) loadAbbreviations(data []byte) error {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("abbreviations.json: %w", err)
	}
	for k, v := range m {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if _, taken := l.abbreviations[strings.ToLower(k)]; !taken {
			l.abbreviations[strings.ToLower(k)] = v
		}
	}
	return nil
}

func (l *Lexicon) loadIntensities(data []byte) error {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("symptoms.json: %w", err)
	}
	for k, v := range m {
		if strings.HasPrefix(k, "_") {
			continue
		}
		l.intensities[k] = v
	}
	return nil
}

func (l *Lexicon) loadSerology(data []byte) error {
	var doc struct {
		Grades map[string]string `json:"grades"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("serology.json: %w", err)
	}
	for k, v := range doc.Grades {
		if strings.HasPrefix(k, "_") {
			continue
		}
		l.serology[k] = v
	}
	return nil
}

func (l *Lexicon) loadFindings(data []byte) error {
	var doc struct {
		Findings map[string]string `json:"findings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("findings.json: %w", err)
	}
	for k, v := range doc.Findings {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if _, taken := l.findings[strings.ToLower(k)]; !taken {
			l.findings[strings.ToLower(k)] = v
		}
	}
	return nil
}

func (l *Lexicon) loadRadKeys(data []byte) error {
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("rad_keys.json: %w", err)
	}
	for _, k := range keys {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			l.radKeys = append(l.radKeys, k)
		}
	}
	sort.Strings(l.radKeys)
	return nil
}

func (l *Lexicon) loadDurations(data []byte) error {
	var m map[string]struct {
		Aliases []string `json:"aliases"`
		Word    string   `json:"word"`
		Short   string   `json:"short"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("durations.json: %w", err)
	}
	for key, du := range m {
		if strings.HasPrefix(key, "_") {
			continue
		}
		unit := DurationUnit{Short: du.Short, Word: du.Word, Aliases: du.Aliases}
		if unit.Short == "" {
			unit.Short = key
		}
		for _, a := range append(du.Aliases, key) {
			if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
				l.durations[a] = unit
			}
		}
	}
	return nil
}

type freqEntry struct {
	Display  string   `json:"display"`
	Count    int      `json:"count"`
	Period   float64  `json:"period"`
	Unit     string   `json:"period_unit"`
	When     []string `json:"when"`
	Priority string   `json:"priority"`
	AsNeeded bool     `json:"as_needed"`
	Aliases  []string `json:"aliases"`
}

func (l *Lexicon) loadFrequencies(data []byte) error {
	var doc struct {
		Frequencies map[string]freqEntry `json:"frequencies"`
		Modifiers   map[string]freqEntry `json:"modifiers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("frequencies.json: %w", err)
	}

	add := func(code string, e freqEntry) {
		if strings.HasPrefix(code, "_") {
			return
		}
		f := Frequency{
			Code:     code,
			Display:  e.Display,
			Count:    e.Count,
			Period:   e.Period,
			Unit:     e.Unit,
			When:     e.When,
			Priority: e.Priority,
			AsNeeded: e.AsNeeded,
		}
		l.freqByCode[code] = f
		l.freqByCode[strings.ToUpper(code)] = f
		for _, a := range append(e.Aliases, code) {
			if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
				l.frequencies[a] = f
			}
		}
	}

	for code, e := range doc.Frequencies {
		add(code, e)
	}
	// Modifiers such as PRN share the alias namespace but carry no schedule,
	// so resolving "prn" yields AsNeeded without a Count that could overwrite
	// a real frequency.
	for code, e := range doc.Modifiers {
		add(code, e)
	}
	return nil
}

func (l *Lexicon) loadRoutes(data []byte) error {
	var doc struct {
		Routes map[string]struct {
			Display string   `json:"display"`
			Aliases []string `json:"aliases"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("routes.json: %w", err)
	}

	for code, e := range doc.Routes {
		if strings.HasPrefix(code, "_") {
			continue
		}
		r := Route{Code: code, Display: e.Display}
		l.routeByCode[strings.ToUpper(code)] = r
		for _, a := range append(e.Aliases, code) {
			if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
				l.routes[a] = r
			}
		}
	}
	return nil
}

func (l *Lexicon) loadUnits(data []byte) error {
	var doc struct {
		Dimensions map[string]struct {
			Base  string `json:"base"`
			Units map[string]struct {
				Factor  float64  `json:"factor"`
				Offset  float64  `json:"offset"`
				Aliases []string `json:"aliases"`
			} `json:"units"`
		} `json:"dimensions"`
		Compound struct {
			Aliases map[string]string `json:"aliases"`
		} `json:"compound"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("units.json: %w", err)
	}

	for dim, group := range doc.Dimensions {
		if strings.HasPrefix(dim, "_") {
			continue
		}
		for canon, u := range group.Units {
			unit := Unit{
				Canonical: canon,
				Dimension: dim,
				Factor:    u.Factor,
				Offset:    u.Offset,
			}
			if unit.Factor == 0 {
				unit.Factor = 1
			}
			for _, a := range append(u.Aliases, canon) {
				if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
					l.units[a] = unit
				}
			}
		}
	}
	for alias, canon := range doc.Compound.Aliases {
		if strings.HasPrefix(alias, "_") {
			continue
		}
		l.compound[strings.ToLower(alias)] = canon
	}
	return nil
}

func (l *Lexicon) loadMeasurements(data []byte) error {
	// Decoded one entry at a time so that inline "_comment" strings sitting
	// beside real entries do not fail the whole file. The data files are meant
	// to be read and edited by people, so they carry explanatory prose.
	var doc struct {
		Measurements map[string]json.RawMessage `json:"measurements"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("measurements.json: %w", err)
	}

	for key, raw := range doc.Measurements {
		if strings.HasPrefix(key, "_") {
			continue
		}
		var m struct {
			Display     string `json:"display"`
			Group       string `json:"group"`
			Shape       string `json:"shape"`
			Unit        string `json:"unit"`
			Dimension   string `json:"dimension"`
			DefaultUnit string `json:"default_unit"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("measurements.json: entry %q: %w", key, err)
		}
		lower := strings.ToLower(key)
		if _, taken := l.measurements[lower]; taken {
			continue
		}
		l.measurements[lower] = Measurement{
			Key:         lower,
			Display:     m.Display,
			Group:       m.Group,
			Shape:       m.Shape,
			Unit:        m.Unit,
			Dimension:   m.Dimension,
			DefaultUnit: m.DefaultUnit,
		}
	}
	return nil
}

func (l *Lexicon) loadDrugs(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return fmt.Errorf("drugs.json: %w", err)
	}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			l.drugs.Insert(n, n)
		}
	}
	return nil
}

// buildSplitKeys produces the length-sorted key list used by SplitKey.
//
// Sorting longest-first makes longest-match a linear scan with a first-hit
// return, and the secondary lexicographic sort makes the result independent of
// map iteration order. Deterministic parsing is a product promise, and the
// previous engine broke it by ranging over a map in its token dispatcher.
func (l *Lexicon) buildSplitKeys() {
	l.splitKeys = make([]string, 0, len(l.measurements))
	for k := range l.measurements {
		l.splitKeys = append(l.splitKeys, k)
	}
	sort.Slice(l.splitKeys, func(i, j int) bool {
		if len(l.splitKeys[i]) != len(l.splitKeys[j]) {
			return len(l.splitKeys[i]) > len(l.splitKeys[j])
		}
		return l.splitKeys[i] < l.splitKeys[j]
	})
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

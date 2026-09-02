package parser

import (
	"sort"
	"strings"
)

// Shape describes how a command's arguments are parsed.
//
// This is grammar, not medicine: it says whether a command takes tokenized
// arguments or swallows the rest of the line as prose. Which commands exist and
// what they mean belongs to the semantic layer and to plugins.
type Shape uint8

const (
	// Structured parses arguments into measurement, concept and quantity nodes.
	Structured Shape = iota

	// FreeText captures the remainder of the line verbatim as a single node.
	// Prose fields such as chief complaint must not be tokenized: splitting
	// them would destroy the clinician's phrasing, which the note has to
	// reproduce exactly.
	FreeText

	// Sectioned parses "key: value" runs where a value continues until the
	// next key or the end of the line.
	//
	// This is what lets a finding be several words without gluing them
	// together: "ix cxr:patchy consolidation left lower lobe" records one
	// imaging finding rather than four separate flags. Tokens appearing before
	// the first key are parsed structurally, so numeric results and descriptive
	// findings coexist on one line, and a line containing no key at all is
	// parsed entirely structurally.
	Sectioned

	// SectionedProse is Sectioned with a prose fallback: a line with no key is
	// captured verbatim rather than tokenized.
	//
	// Physical examination is written both ways. "pe chest clear bilaterally"
	// is a sentence; "pe cvs:rr s1s2 rs:nvb" is a per-system record. The
	// presence of a key is what distinguishes them.
	SectionedProse
)

// sectioned reports whether the shape supports key-delimited sections.
func (s Shape) sectioned() bool { return s == Sectioned || s == SectionedProse }

// Grammar supplies the two pieces of vocabulary the parser cannot derive from
// the token stream alone.
//
// Keeping this an interface means the parser depends on shapes and a key
// lexicon, not on a hardcoded clinical vocabulary, and lets plugins extend both
// without the core knowing what they added.
type Grammar interface {
	// Shape reports how the named command's arguments are parsed. Unknown
	// commands are Structured, so an unrecognised command still yields useful
	// structure rather than a blob of text.
	Shape(command string) Shape

	// SplitKey returns the byte length of the longest lexicon key that
	// prefixes word, compared case-insensitively, or 0 when none matches.
	//
	// This is what resolves the pco2-versus-hb13 ambiguity. Both are a letter
	// run followed by a digit run and no context-free rule separates them, so
	// the lexicon decides: "pco2" is a registered key and consumes the whole
	// word, while "hb13" is not and falls back to splitting at the first digit.
	// Adding an analyte is therefore a data change, not a code change.
	SplitKey(word string) int
}

// StaticGrammar is a Grammar backed by fixed tables.
//
// The keys are stored sorted by descending length so that longest-match is a
// linear scan with an early exit, and the result never depends on map iteration
// order — the nondeterminism that made the previous engine's plugin dispatch
// vary between runs.
type StaticGrammar struct {
	shapes map[string]Shape
	keys   []string
}

// NewStaticGrammar builds a Grammar from a command-shape table and a key list.
//
// Both are copied, so later mutation of the caller's maps cannot change parsing
// behaviour mid-flight.
func NewStaticGrammar(shapes map[string]Shape, keys []string) *StaticGrammar {
	g := &StaticGrammar{
		shapes: make(map[string]Shape, len(shapes)),
		keys:   make([]string, 0, len(keys)),
	}
	for k, v := range shapes {
		g.shapes[strings.ToLower(k)] = v
	}

	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		lower := strings.ToLower(strings.TrimSpace(k))
		if lower == "" {
			continue
		}
		if _, dup := seen[lower]; dup {
			continue
		}
		seen[lower] = struct{}{}
		g.keys = append(g.keys, lower)
	}

	// Longest first, then lexicographic, so matching is deterministic.
	sort.Slice(g.keys, func(i, j int) bool {
		if len(g.keys[i]) != len(g.keys[j]) {
			return len(g.keys[i]) > len(g.keys[j])
		}
		return g.keys[i] < g.keys[j]
	})
	return g
}

// Shape implements Grammar.
func (g *StaticGrammar) Shape(command string) Shape {
	if g == nil {
		return Structured
	}
	if s, ok := g.shapes[strings.ToLower(command)]; ok {
		return s
	}
	return Structured
}

// SplitKey implements Grammar.
//
// A key claims a word only when it consumes the whole word or is followed by a
// digit. That second condition is essential and easy to omit: without it the
// chloride key "cl" splits "clopidogrel" into "cl" and "opidogrel", and the
// route alias "in" splits "insulin". A key is a key only when a value follows
// it.
//
// This is the single implementation of the rule. The lexicon supplies the key
// data and nothing else, because two copies of a splitting rule is exactly the
// defect class this engine was rewritten to remove.
func (g *StaticGrammar) SplitKey(word string) int {
	if g == nil || len(g.keys) == 0 {
		return 0
	}
	lower := strings.ToLower(word)

	for _, k := range g.keys {
		if len(k) > len(lower) || !strings.HasPrefix(lower, k) {
			continue
		}
		if len(k) == len(lower) {
			return len(k) // the key is the whole word
		}
		if c := lower[len(k)]; c >= '0' && c <= '9' {
			return len(k) // a value follows
		}
	}
	return 0
}

// CoreShapes is the shape of every built-in command.
//
// It serves two purposes: it tells the parser how to read each command's
// arguments, and it is the definitive set of core command names, which is what
// the plugin registry checks a contributed command against. A command absent
// from this map is not a core command and parses as Structured.
var CoreShapes = map[string]Shape{
	// Structured commands are listed explicitly even though Structured is the
	// default, because this map doubles as the authoritative list of core
	// command names. Leaving them out let a plugin claim "rx" without being
	// detected as a conflict, since the collision check had nothing to compare
	// against.
	"pt":     Structured,
	"vitals": Structured,
	"sx":     Structured,
	"ros":    Structured,
	"rx":     Structured,

	// Prose. Splitting these would destroy the clinician's phrasing.
	"cc":      FreeText,
	"hpi":     FreeText,
	"pmh":     FreeText,
	"sh":      FreeText,
	"fh":      FreeText,
	"dx":      FreeText,
	"ddx":     FreeText,
	"day":     FreeText,
	"alg":     FreeText,
	"allergy": FreeText,
	"rhx":     FreeText,
	"id":      FreeText,

	// Examination is written as prose or as a per-system record, and which one
	// is decided by whether the line contains a key.
	"pe":   SectionedProse,
	"oe":   SectionedProse,
	"exam": SectionedProse,

	// Investigations mix numeric results with descriptive findings, often on
	// the same line: "ix hb9.2 cxr:patchy consolidation left lower lobe".
	"ix":   Sectioned,
	"lab":  Sectioned,
	"labs": Sectioned,
	"rad":  Sectioned,
}

// DefaultGrammar returns a Grammar with the core command shapes and no key
// lexicon, so glued words split at the first digit.
//
// Production callers pass a Grammar backed by the loaded lexicon instead; this
// exists for tests and for parsing without a configured workspace.
func DefaultGrammar() *StaticGrammar {
	return NewStaticGrammar(CoreShapes, nil)
}

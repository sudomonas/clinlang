// Package vocab holds ClinLang's general clinical vocabulary.
//
// It is data and nothing else: drug names, analytes and vital signs, symptoms
// and examination findings, abbreviations, and imaging modalities. The core
// language ships none of these, because a language for clinical documentation
// is not a clinical database.
//
// # Why this is separate from the plugin that serves it
//
// pkg/plugins/medicine wraps this as a plugin, and that wrapper depends on the
// plugin machinery, which in turn depends on the parser and the semantic layer.
// Keeping the data in a leaf package means every layer of the engine — and
// every test at every layer — can load the vocabulary without importing the
// stack above it.
//
// # This is vocabulary, not knowledge
//
// Nothing here says what a value should be, which drug suits a condition, or
// what a finding implies. There are no reference ranges, no interactions, no
// contraindications and no dose limits. Every entry is a name and a label.
package vocab

import (
	_ "embed"

	"clinlang/pkg/lexicon"
)

//go:embed data/drugs.json
var drugs []byte

//go:embed data/findings.json
var findings []byte

//go:embed data/abbreviations.json
var abbreviations []byte

//go:embed data/measurements.json
var measurements []byte

//go:embed data/rad_keys.json
var radKeys []byte

// Sources returns the vocabulary as a lexicon layer.
func Sources() lexicon.Sources {
	return lexicon.Sources{
		Drugs:         drugs,
		Findings:      findings,
		Abbreviations: abbreviations,
		Measurements:  measurements,
		RadKeys:       radKeys,
	}
}

// Lexicon builds a lexicon of the core notation plus this vocabulary.
//
// This is what most callers want, and what the CLI and server produce once the
// medicine plugin is registered.
func Lexicon() *lexicon.Lexicon {
	lex, err := lexicon.Load(Sources())
	if err != nil {
		panic("vocab: embedded vocabulary is invalid: " + err.Error())
	}
	return lex
}

// JSON returns the embedded contents of a vocabulary file, or nil.
func JSON(name string) []byte {
	switch name {
	case "drugs.json":
		return drugs
	case "findings.json":
		return findings
	case "abbreviations.json":
		return abbreviations
	case "measurements.json":
		return measurements
	case "rad_keys.json":
		return radKeys
	}
	return nil
}

// FileNames lists the vocabulary files this package supplies.
var FileNames = []string{
	"abbreviations.json",
	"findings.json",
	"measurements.json",
	"rad_keys.json",
	"drugs.json",
}

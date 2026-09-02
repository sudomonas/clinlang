// Package medicine registers the general clinical vocabulary as a plugin.
//
// It is a thin wrapper over pkg/vocab, which holds the data. The wrapper exists
// so the vocabulary arrives through the same capability interface a specialty
// plugin uses: the language has no privileged source of clinical terms, and
// medicine is not a special case in the engine.
//
// A note parses to exactly the same tree with this plugin absent. What changes
// is display — an analyte renders as "hb" rather than "Haemoglobin", and a drug
// carries no concept identity. Nothing fails and no statement changes meaning,
// which is the property that makes the separation real rather than nominal.
package medicine

import (
	"clinlang/pkg/lexicon"
	"clinlang/pkg/plugin"
	"clinlang/pkg/vocab"
)

// Plugin supplies the general clinical vocabulary.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "medicine",
		Version:     "1.0.0",
		EngineRange: ">=2.0.0 <3.0.0",
		Description: "General clinical vocabulary: drugs, analytes, findings, abbreviations",
	}
}

// Vocabulary implements plugin.VocabularyProvider.
func (p *Plugin) Vocabulary() lexicon.Sources { return vocab.Sources() }

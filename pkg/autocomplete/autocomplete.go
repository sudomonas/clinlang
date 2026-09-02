// Package autocomplete answers editor completion requests.
//
// Every suggestion comes from the loaded lexicon or from an active plugin.
// There is no suggestion list of its own, deliberately: the previous version
// kept a hardcoded map of symptoms, analytes and diagnoses, which made it a
// third source of clinical vocabulary alongside the JSON lexicons and the
// parser tables. It drifted from both — it offered "chest_pain" and "abd_pain"
// with underscores that the language no longer needs, and analytes that the
// measurement table had since renamed.
//
// Suggestions being derived means adding a drug to drugs.json makes it
// completable, and a plugin's vocabulary appears the moment its profile is
// active.
package autocomplete

import (
	"sort"
	"strings"

	"clinlang/pkg/lexicon"
	"clinlang/pkg/parser"
	"clinlang/pkg/plugin"
)

// DefaultLimit caps how many suggestions one request returns.
const DefaultLimit = 12

// Suggestion is a single completion for the editor.
type Suggestion struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// Provider answers completion requests from a lexicon and a plugin registry.
//
// Held explicitly rather than in a package global so a server can serve
// per-user vocabularies, matching how parsing already works.
type Provider struct {
	lex   *lexicon.Lexicon
	plugs *plugin.Registry
	limit int
}

// New returns a provider. A nil lexicon uses the embedded default and a nil
// registry means no plugin vocabulary.
func New(lex *lexicon.Lexicon, plugs *plugin.Registry) *Provider {
	if lex == nil {
		lex = lexicon.Default()
	}
	return &Provider{lex: lex, plugs: plugs, limit: DefaultLimit}
}

// WithLimit returns a copy of p capped at n suggestions.
func (p *Provider) WithLimit(n int) *Provider {
	if n <= 0 {
		n = DefaultLimit
	}
	return &Provider{lex: p.lex, plugs: p.plugs, limit: n}
}

// Suggest returns completions for a partial argument to the given command.
//
// An empty command completes command names themselves, which is what the
// editor asks for at the start of a line.
func (p *Provider) Suggest(command, query string) []Suggestion {
	command = strings.ToLower(strings.TrimSpace(command))
	query = strings.ToLower(strings.TrimSpace(query))

	var out []Suggestion
	switch {
	case command == "":
		out = p.commands(query)
	case command == "rx":
		out = p.drugs(query)
	case isMeasurementCommand(command):
		out = p.measurements(query, command)
	case command == "sx" || command == "ros" || command == "pe" || command == "oe" || command == "exam":
		out = p.concepts(query, command, lexicon.KindFinding, lexicon.KindAbbreviation)
	case isNarrativeCommand(command):
		out = p.concepts(query, command, lexicon.KindAbbreviation, lexicon.KindFinding)
	default:
		// An unknown command is very likely a plugin's, so offer whatever the
		// plugins contribute rather than nothing.
		out = p.pluginTokens(query)
	}

	if len(out) > p.limit {
		out = out[:p.limit]
	}
	if out == nil {
		out = []Suggestion{}
	}
	return out
}

// commands completes command names, core first then plugin-contributed.
func (p *Provider) commands(query string) []Suggestion {
	var out []Suggestion
	for name := range parser.CoreShapes {
		if strings.HasPrefix(name, query) {
			out = append(out, Suggestion{
				Label: name, Value: name,
				Description: "core command", Category: "command",
			})
		}
	}
	sortSuggestions(out)

	var fromPlugins []Suggestion
	if p.plugs != nil {
		for _, m := range p.plugs.Manifests() {
			for cmd, help := range m.Commands {
				if strings.HasPrefix(strings.ToLower(cmd), query) {
					fromPlugins = append(fromPlugins, Suggestion{
						Label: cmd, Value: cmd,
						Description: help, Category: m.Name,
					})
				}
			}
		}
		sortSuggestions(fromPlugins)
	}
	return append(out, fromPlugins...)
}

// drugs completes medication names from the trie, so lookup cost does not grow
// with the size of the drug list.
func (p *Provider) drugs(query string) []Suggestion {
	if query == "" {
		return nil // the whole formulary is not a useful suggestion
	}
	var out []Suggestion
	for _, name := range p.lex.Drugs().PrefixSearch(query, p.limit) {
		out = append(out, Suggestion{
			Label: name, Value: name,
			Description: "medication", Category: "rx",
		})
	}
	return out
}

// measurements completes analyte and vital-sign keys, with their display names.
func (p *Provider) measurements(query, category string) []Suggestion {
	var out []Suggestion
	for _, c := range p.lex.Concepts(lexicon.KindMeasurement, lexicon.KindImaging) {
		if !strings.HasPrefix(c.Canonical, query) {
			continue
		}
		value := c.Canonical
		// Imaging findings are descriptive, so offer the colon that starts a
		// sectioned value.
		if c.Kind == lexicon.KindImaging {
			value += ":"
		}
		out = append(out, Suggestion{
			Label: value, Value: value,
			Description: c.Label(), Category: category,
		})
	}
	sortSuggestions(out)
	return out
}

// concepts completes from the given lexicon kinds, in the order supplied.
//
// Kind order is the caller's precedence: a symptom line offers findings before
// abbreviations, and a history line the reverse.
func (p *Provider) concepts(query, category string, kinds ...lexicon.ConceptKind) []Suggestion {
	seen := make(map[string]bool)
	var out []Suggestion

	for _, kind := range kinds {
		var group []Suggestion
		for _, c := range p.lex.Concepts(kind) {
			if !strings.HasPrefix(c.Canonical, query) || seen[c.Canonical] {
				continue
			}
			seen[c.Canonical] = true
			group = append(group, Suggestion{
				Label: c.Canonical, Value: c.Canonical,
				Description: c.Display, Category: category,
			})
		}
		sortSuggestions(group)
		out = append(out, group...)
	}
	return out
}

// pluginTokens returns completions contributed by active plugins.
func (p *Provider) pluginTokens(query string) []Suggestion {
	if p.plugs == nil {
		return nil
	}
	var out []Suggestion
	for _, c := range p.plugs.Completions() {
		if !strings.HasPrefix(strings.ToLower(c.Text), query) {
			continue
		}
		out = append(out, Suggestion{
			Label: c.Text, Value: c.Text,
			Description: c.Detail, Category: c.Kind,
		})
	}
	return out
}

// sortSuggestions orders by length then alphabetically, so the shortest match
// — usually the one being typed — comes first, and the order never depends on
// map iteration.
func sortSuggestions(s []Suggestion) {
	sort.Slice(s, func(i, j int) bool {
		if len(s[i].Label) != len(s[j].Label) {
			return len(s[i].Label) < len(s[j].Label)
		}
		return s[i].Label < s[j].Label
	})
}

func isMeasurementCommand(cmd string) bool {
	switch cmd {
	case "ix", "lab", "labs", "rad", "vitals":
		return true
	}
	return false
}

func isNarrativeCommand(cmd string) bool {
	switch cmd {
	case "pmh", "hpi", "cc", "dx", "ddx", "sh", "fh", "pe", "oe", "exam", "alg", "allergy":
		return true
	}
	return false
}

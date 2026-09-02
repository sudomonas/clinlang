package main

import (
	"os"
	"path/filepath"

	"clinlang/pkg/lexicon"
	"clinlang/pkg/plugin"
)

// configDirName is the per-project directory holding lexicon overrides.
const configDirName = ".clinlang"

// findConfigDir locates the lexicon override directory, or returns "".
//
// Resolution order, first hit wins:
//
//  1. CLINLANG_CONFIG, when set.
//  2. A .clinlang directory beside the file being compiled, or in any
//     ancestor of it. This is how a clinic or a shared case repository
//     carries its own vocabulary alongside the notes, and it is why the
//     search walks upward the way version-control tools do.
//  3. The user's own configuration directory.
//
// Absent configuration is the normal case and never an error. The shipped
// vocabulary is complete; overrides are for clinics with local terms.
func findConfigDir(notePath string) string {
	if dir := os.Getenv("CLINLANG_CONFIG"); dir != "" {
		if isDir(dir) {
			return dir
		}
		return ""
	}

	if abs, err := filepath.Abs(notePath); err == nil {
		for dir := filepath.Dir(abs); ; {
			if candidate := filepath.Join(dir, configDirName); isDir(candidate) {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	if home, err := os.UserConfigDir(); err == nil {
		if candidate := filepath.Join(home, "clinlang"); isDir(candidate) {
			return candidate
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// loadLexicon builds the vocabulary for a compilation.
//
// Layering is override-first: a term a workspace defines wins, and every term
// it does not mention is inherited from the plugins. That is what lets a clinic
// add local formulations without restating the whole formulary.
//
// A malformed override is reported by the caller rather than silently ignored:
// a clinic whose drugs.json is broken should be told, not quietly given stock
// behaviour while believing its own list is active.
func loadLexicon(notePath string, reg *plugin.Registry) (*lexicon.Lexicon, error) {
	layers := []lexicon.Sources{}

	if dir := findConfigDir(notePath); dir != "" {
		var overrides lexicon.Sources
		found := false
		for _, name := range lexicon.FileNames {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			overrides.Set(name, data)
			found = true
		}
		if found {
			layers = append(layers, overrides)
		}
	}

	layers = append(layers, reg.Vocabulary()...)
	return lexicon.Load(layers...)
}

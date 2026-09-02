// Command clinlang compiles ClinLang source into clinical notes.
//
// It is a compiler, in the shape every compiler has: read a file, produce
// diagnostics, emit an artifact. Notes go to stdout and diagnostics to stderr,
// so output can be piped to a file without the engine's messages mixing into
// the clinical text.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"clinlang/pkg/autocomplete"
	"clinlang/pkg/backend"
	"clinlang/pkg/clinlang"
	"clinlang/pkg/diag"
	"clinlang/pkg/plugin"
	"clinlang/pkg/plugins/medicine"
	"clinlang/pkg/plugins/obgyn"
)

// disclaimer is shown in the usage banner.
// Keep in sync with DISCLAIMER.md at the repository root.
const disclaimer = "ClinLang records clinical documentation. It is not a medical device: " +
	"no diagnosis, no dosing, no decision support."

// languageVersion is the ClinLang language version this build implements.
//
// A note may pin it with "@clinlang 1.0". Any change to how existing valid
// input parses requires a major bump and an explicit opt-in, because notes are
// archival and must render identically years later.
const languageVersion = "1.0"

// registry builds the plugin set for this build.
//
// Plugins register explicitly rather than through init side effects, so an
// incompatible plugin is reported here instead of being silently missing. The
// general clinical vocabulary is itself a plugin: the language ships no drugs,
// analytes or findings of its own.
func registry() (*plugin.Registry, error) {
	reg := plugin.NewRegistry()
	for _, p := range []plugin.Plugin{medicine.New(), obgyn.New()} {
		if err := reg.Register(p); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func main() {
	args := os.Args
	if len(args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch cmd := strings.ToLower(args[1]); cmd {
	case "help", "--help", "-h":
		printUsage()

	case "version", "--version":
		fmt.Printf("clinlang %s (plugin ABI %s)\n", languageVersion, plugin.EngineVersion)

	case "backends":
		for _, name := range clinlang.Backends() {
			fmt.Println(name)
		}

	case "plugins":
		listPlugins()

	case "complete":
		complete(args[2:])

	default:
		compile(cmd, args[2:])
	}
}

// compile reads a file, runs the pipeline, and emits the requested format.
func compile(sub string, rest []string) {
	rest, verbatim := extractFlag(rest, "--verbatim")
	rest, quiet := extractFlag(rest, "--quiet")

	format, ok := map[string]string{
		"run":      "plain",
		"plain":    "plain",
		"soap":     "soap",
		"markdown": "markdown",
		"md":       "markdown",
		"json":     "json",
		"check":    "", // diagnostics only
	}[sub]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", sub)
		printUsage()
		os.Exit(1)
	}
	if len(rest) < 1 {
		fmt.Fprintf(os.Stderr, "%s: no input file\n", sub)
		os.Exit(1)
	}

	path := rest[0]
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	reg, err := registry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin error:", err)
		os.Exit(2)
	}
	lex, err := loadLexicon(path, reg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lexicon error:", err)
		os.Exit(2)
	}

	result, err := clinlang.Parse(context.Background(), path, string(content),
		clinlang.Options{Lexicon: lex, Plugins: reg})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if format == "" {
		if n := report(result, diag.Hint, true); n == 0 {
			fmt.Println("No diagnostics.")
		}
		// Only a genuine error is a failure. A warning is information, and the
		// note still renders.
		if result.HasErrors() {
			os.Exit(1)
		}
		return
	}

	out, err := clinlang.Render(result, format, backend.Options{Verbatim: verbatim})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)

	if !quiet {
		report(result, diag.Warning, false)
	}
}

// report writes diagnostics at or above min to stderr.
func report(r *clinlang.Result, min diag.Severity, showSource bool) int {
	ds := r.Filter(min)
	for _, d := range ds {
		fmt.Fprintln(os.Stderr, diag.Render(r.File, d, diag.RenderOptions{ShowSource: showSource}))
	}
	return len(ds)
}

// complete prints completions for a command and a partial argument.
//
// This is the language-service surface until a language server exists; an LSP
// implementation will call the same package.
func complete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: clinlang complete <command> [partial]")
		os.Exit(1)
	}
	query := ""
	if len(args) > 1 {
		query = args[1]
	}

	reg, err := registry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin error:", err)
		os.Exit(2)
	}
	lex, err := loadLexicon(".", reg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lexicon error:", err)
		os.Exit(2)
	}

	for _, s := range autocomplete.New(lex, reg).Suggest(args[0], query) {
		if s.Description != "" {
			fmt.Printf("%-24s %s\n", s.Value, s.Description)
			continue
		}
		fmt.Println(s.Value)
	}
}

func listPlugins() {
	reg, err := registry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin error:", err)
		os.Exit(2)
	}
	for _, m := range reg.Manifests() {
		fmt.Printf("%-10s %-8s %s\n", m.Name, m.Version, m.Description)
		for cmd, help := range m.Commands {
			fmt.Printf("           %-14s %s\n", cmd, help)
		}
	}
}

// extractFlag removes every occurrence of flag and reports whether it appeared.
func extractFlag(args []string, flag string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func printUsage() {
	fmt.Println(disclaimer)
	fmt.Println()
	fmt.Println(`clinlang — a language for clinical documentation

Usage:
  clinlang <format> <file.cln> [flags]

Formats:
  run, plain        Plain clinical note
  soap              SOAP-structured note
  markdown, md      Markdown
  json              The clinical IR, structured

Other commands:
  check <file.cln>  Diagnostics only, with source context
  complete <cmd> [partial]
                    Completions for an argument to <cmd>
  backends          List output formats
  plugins           List loaded plugins and their commands
  version           Language and plugin ABI versions

Flags:
  --verbatim        Render as typed, without expanding abbreviations
  --quiet           Suppress diagnostics

Configuration:
  Vocabulary overrides are read from a .clinlang directory beside the note or
  in any ancestor of it, from $CLINLANG_CONFIG, or from the user config
  directory. Overrides layer over the shipped vocabulary rather than replacing
  it. None is required.

Notes go to stdout, diagnostics to stderr.`)
}

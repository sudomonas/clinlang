package sema

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"clinlang/pkg/diag"
	"clinlang/pkg/parser"
	"clinlang/pkg/source"
	"clinlang/pkg/vocab"
)

var update = flag.Bool("update", false, "rewrite the corpus diagnostic snapshot")

// TestCorpusDiagnostics runs every example note through the whole pipeline and
// compares the diagnostics against a checked-in snapshot.
//
// Unit tests assert what the author expected; this asserts what real notes
// actually produce. Every bug found here so far had passing unit tests beside
// it — the symptom intensity table being applied to a lab result, the chloride
// key splitting "clopidogrel", a course length being read as a second dose. The
// snapshot makes any change in behaviour across the corpus visible in a diff.
//
// Run with -update to accept a change after reviewing it.
func TestCorpusDiagnostics(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.cln"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no corpus files found")
	}
	sort.Strings(paths)

	lex := vocab.Lexicon()
	var sb strings.Builder

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		name := filepath.Base(path)
		f := source.NewFile(name, string(content))
		bag := diag.NewBag()
		g := parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys())
		doc := parser.Parse(f, g, bag)
		Analyze(f, doc, bag, Options{Lexicon: lex})

		fmt.Fprintf(&sb, "=== %s\n", name)
		for _, d := range bag.Sorted() {
			p := f.Position(d.Span.Start)
			fmt.Fprintf(&sb, "%d:%d %s %s %s\n",
				p.Line, p.Col, d.Severity, d.Code, d.Message)
		}
	}

	got := sb.String()
	golden := filepath.Join("testdata", "corpus_diagnostics.txt")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing snapshot %s (run: go test ./pkg/sema/ -update): %v", golden, err)
	}
	if got != string(want) {
		t.Errorf("corpus diagnostics changed.\n--- want ---\n%s\n--- got ---\n%s\n"+
			"If the change is intended, review it and run: go test ./pkg/sema/ -update",
			want, got)
	}
}

// TestCorpusIsDeterministic parses every example repeatedly and asserts the
// diagnostics never vary. Map iteration order is the classic source of
// non-reproducible output, and reproducibility is the product promise.
func TestCorpusIsDeterministic(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.cln"))
	if len(paths) == 0 {
		t.Skip("no corpus files found")
	}
	lex := vocab.Lexicon()

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		run := func() string {
			f := source.NewFile(filepath.Base(path), string(content))
			bag := diag.NewBag()
			g := parser.NewStaticGrammar(parser.CoreShapes, lex.MeasurementKeys())
			doc := parser.Parse(f, g, bag)
			Analyze(f, doc, bag, Options{Lexicon: lex})

			var sb strings.Builder
			for _, d := range bag.Sorted() {
				fmt.Fprintf(&sb, "%d|%s|%s\n", d.Span.Start, d.Code, d.Message)
			}
			return sb.String()
		}

		first := run()
		for i := 0; i < 10; i++ {
			if got := run(); got != first {
				t.Fatalf("%s: run %d differed from the first", filepath.Base(path), i)
			}
		}
	}
}

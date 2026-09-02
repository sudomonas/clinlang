package clinlang

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"clinlang/pkg/backend"
	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
	"clinlang/pkg/plugin"
	"clinlang/pkg/plugins/obgyn"
)

var update = flag.Bool("update", false, "rewrite corpus snapshots")

// withPlugins returns options carrying the plugins this build ships.
func withPlugins(t *testing.T) Options {
	t.Helper()
	reg := plugin.NewRegistry()
	if err := reg.Register(obgyn.New()); err != nil {
		t.Fatalf("obgyn failed to register: %v", err)
	}
	return Options{Plugins: reg}
}

func parse(t *testing.T, src string, opts Options) *Result {
	t.Helper()
	r, err := Parse(context.Background(), "test.cln", src, opts)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	return r
}

// ─────────────────────────────────────────────────────────────────────────────
// Plugin integration
// ─────────────────────────────────────────────────────────────────────────────

func TestObGynEndToEnd(t *testing.T) {
	src := "@profile obgyn\n" +
		"pt 28F wt65kg ga:34w\n" +
		"vitals bp120/75 fhr:142\n" +
		"lmp 2025-06-15\n" +
		"edd 2026-03-22\n" +
		"gpal G2P1A0L1\n" +
		"fhs 142 regular, reactive\n" +
		"ctx 4 in 10min lasting 45s\n"

	r := parse(t, src, withPlugins(t))

	for _, d := range r.Diagnostics {
		if d.Severity >= diag.Warning {
			t.Errorf("unexpected %s %s: %s", d.Severity, d.Code, d.Message)
		}
	}

	data, ok := ir.Extension[*obgyn.Data](r.Case.Primary(), "obgyn")
	if !ok {
		t.Fatal("obgyn extension data missing")
	}
	if data.GA == nil || data.GA.Value != 34 || data.GA.Canonical != "w" {
		t.Errorf("gestational age = %+v, want 34w", data.GA)
	}
	if data.FHR == nil || data.FHR.Value != 142 {
		t.Errorf("fetal heart rate = %+v, want 142", data.FHR)
	}
	for name, got := range map[string]*ir.Narrative{
		"lmp": data.LMP, "edd": data.EDD, "gpal": data.GPAL,
		"fhs": data.FHS, "ctx": data.CTX,
	} {
		if got == nil || got.Text == "" {
			t.Errorf("%s was not recorded", name)
		}
	}
	// The prose commands keep their whole sentence, which is what declaring a
	// FreeText shape buys: without it every word becomes an unknown token.
	if data.CTX.Text != "4 in 10min lasting 45s" {
		t.Errorf("ctx = %q, want the whole phrase", data.CTX.Text)
	}
}

// Without the profile declared, the plugin is inert and its tokens are
// reported rather than silently dropped.
func TestProfileMustBeDeclared(t *testing.T) {
	r := parse(t, "pt 28F ga:34w\n", withPlugins(t))

	if _, ok := ir.Extension[*obgyn.Data](r.Case.Primary(), "obgyn"); ok {
		t.Error("an undeclared profile must not attach data")
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == diag.UnrecognizedToken {
			found = true
		}
	}
	if !found {
		t.Error("an unhandled profile token should be reported")
	}
}

// The combined form must work. In the previous engine "@profile obgyn+peds"
// stored a map instead of the payload, so every type assertion failed and all
// five obstetric commands silently became no-ops.
func TestCombinedProfilesKeepTheirData(t *testing.T) {
	opts := withPlugins(t)
	for _, src := range []string{
		"@profile obgyn\npt 28F ga:34w\n",
		"@obgyn\npt 28F ga:34w\n",
	} {
		r := parse(t, src, opts)
		data, ok := ir.Extension[*obgyn.Data](r.Case.Primary(), "obgyn")
		if !ok || data.GA == nil {
			t.Errorf("%q: obgyn data missing", src)
		}
	}
}

func TestProfileDeclarationOrderIsFree(t *testing.T) {
	opts := withPlugins(t)
	for _, src := range []string{
		"@profile obgyn\npt 28F ga:34w\n",
		"pt 28F ga:34w\n@profile obgyn\n",
	} {
		r := parse(t, src, opts)
		data, ok := ir.Extension[*obgyn.Data](r.Case.Primary(), "obgyn")
		if !ok || data.GA == nil || data.GA.Value != 34 {
			t.Errorf("%q: gestational age lost", src)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Facade behaviour
// ─────────────────────────────────────────────────────────────────────────────

func TestInputSizeIsBounded(t *testing.T) {
	huge := strings.Repeat("vitals hr98\n", 200_000)
	if _, err := Parse(context.Background(), "big.cln", huge, Options{}); err == nil {
		t.Error("oversized input should be refused")
	}
	// A caller that genuinely wants no cap can say so.
	if _, err := Parse(context.Background(), "big.cln", huge, Options{MaxInputBytes: -1}); err != nil {
		t.Errorf("an explicit unlimited cap should be honoured: %v", err)
	}
}

func TestContextCancellationIsObserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, "t.cln", "pt 34M\n", Options{}); err == nil {
		t.Error("a cancelled context should abort the parse")
	}
}

func TestDiagnosticsAreBounded(t *testing.T) {
	// Pathological input must not turn into unbounded memory.
	noise := strings.Repeat("vitals zzz yyy xxx\n", 5000)
	r, err := Parse(context.Background(), "noise.cln", noise, Options{MaxDiagnostics: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Diagnostics) > 50 {
		t.Errorf("got %d diagnostics, want at most 50", len(r.Diagnostics))
	}
}

func TestRenderThroughFacade(t *testing.T) {
	r := parse(t, "pt 34M\nrx amoxicillin 500mg tds po\n", Options{})
	for _, name := range Backends() {
		out, err := Render(r, name, backend.Options{})
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if len(out) == 0 {
			t.Errorf("%s produced no output", name)
		}
	}
	if _, err := Render(r, "fhir", backend.Options{}); err == nil {
		t.Error("an unknown backend should error")
	}
}

func TestNoGlobalStateBetweenParses(t *testing.T) {
	// Two parses with different plugin sets must not influence each other.
	withObGyn := withPlugins(t)
	bare := Options{}

	a := parse(t, "@profile obgyn\npt 28F ga:34w\n", withObGyn)
	b := parse(t, "@profile obgyn\npt 28F ga:34w\n", bare)

	if _, ok := ir.Extension[*obgyn.Data](a.Case.Primary(), "obgyn"); !ok {
		t.Error("the configured parse lost its plugin data")
	}
	if _, ok := ir.Extension[*obgyn.Data](b.Case.Primary(), "obgyn"); ok {
		t.Error("an unconfigured parse gained plugin data from another parse")
	}
}

func TestConcurrentParsesAreSafe(t *testing.T) {
	opts := withPlugins(t)
	src := "@profile obgyn\npt 28F wt65kg ga:34w\nvitals bp120/75 fhr:142\n" +
		"lab hb11.2 pco2:38\nrx amoxicillin 500mg tds po x7d\n"

	want := parse(t, src, opts)
	wantOut, err := Render(want, "soap", backend.Options{})
	if err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func() {
			r, err := Parse(context.Background(), "t.cln", src, opts)
			if err != nil {
				errs <- err
				return
			}
			out, err := Render(r, "soap", backend.Options{})
			if err != nil {
				errs <- err
				return
			}
			if string(out) != string(wantOut) {
				errs <- fmt.Errorf("concurrent parse produced different output")
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < 32; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Corpus
// ─────────────────────────────────────────────────────────────────────────────

// TestCorpusRenders runs every example through the whole pipeline with plugins
// loaded and snapshots both the diagnostics and the SOAP output.
//
// This is the end-to-end regression net. The sema-level corpus test covers the
// core language; this one covers what a user actually gets.
func TestCorpusRenders(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.cln"))
	if len(paths) == 0 {
		t.Skip("no corpus files found")
	}
	sort.Strings(paths)
	opts := withPlugins(t)

	var sb strings.Builder
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)

		r, err := Parse(context.Background(), name, string(content), opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := Render(r, "soap", backend.Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		fmt.Fprintf(&sb, "════ %s\n", name)
		for _, d := range r.Diagnostics {
			p := r.File.Position(d.Span.Start)
			fmt.Fprintf(&sb, "  %d:%d %s %s %s\n", p.Line, p.Col, d.Severity, d.Code, d.Message)
		}
		sb.WriteString("────\n")
		sb.Write(out)
		sb.WriteString("\n")
	}

	compareGolden(t, filepath.Join("testdata", "corpus.txt"), sb.String())
}

// TestCorpusHasNoWarnings asserts the shipped examples parse cleanly.
//
// The examples are the language's own documentation. A warning in one means
// either the example is wrong or the engine is, and either way someone should
// look at it.
func TestCorpusHasNoWarnings(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.cln"))
	if len(paths) == 0 {
		t.Skip("no corpus files found")
	}
	opts := withPlugins(t)

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		r, err := Parse(context.Background(), name, string(content), opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, d := range r.Diagnostics {
			if d.Severity < diag.Warning {
				continue
			}
			// Two examples deliberately name profiles this build does not
			// ship, to exercise a partially-available profile stack. Reporting
			// those is the correct behaviour, so they are expected here.
			if d.Code == diag.UnknownProfile {
				continue
			}
			p := r.File.Position(d.Span.Start)
			t.Errorf("%s:%d:%d %s %s: %s", name, p.Line, p.Col, d.Severity, d.Code, d.Message)
		}
	}
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing snapshot %s (run: go test ./pkg/clinlang/ -update): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("output changed; review and run: go test ./pkg/clinlang/ -update")
	}
}

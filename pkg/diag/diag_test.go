package diag

import (
	"encoding/json"
	"strings"
	"testing"

	"clinlang/pkg/source"
)

func TestSortedIsDeterministic(t *testing.T) {
	// Recorded out of order, as passes running in different orders would.
	b := NewBag()
	b.Warn(UnknownUnit, source.Span{Start: 40, End: 45}, "third")
	b.Warn(UnrecognizedToken, source.Span{Start: 10, End: 15}, "first")
	b.Hint(UnknownAbbreviation, source.Span{Start: 40, End: 45}, "also third")
	b.Error(MalformedRatio, source.Span{Start: 22, End: 30}, "second")

	got := b.Sorted()
	// The two entries at span {40,45} tie on position and break on code:
	// UnknownUnit is CLN3003, UnknownAbbreviation is CLN3008.
	want := []string{"first", "second", "third", "also third"}
	if len(got) != len(want) {
		t.Fatalf("got %d diagnostics, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Message != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i].Message, want[i])
		}
	}

	// Sorting must be stable across repeated calls, since golden files and the
	// determinism test compare this output byte for byte.
	for i := 0; i < 20; i++ {
		again := b.Sorted()
		for j := range again {
			if again[j].Message != got[j].Message {
				t.Fatalf("Sorted() not stable at iteration %d position %d", i, j)
			}
		}
	}
}

// Diagnostics at the same span are ordered by code so that two passes emitting
// at one token do not swap places between runs.
func TestSortedTieBreaksOnCode(t *testing.T) {
	span := source.Span{Start: 5, End: 9}
	b := NewBag()
	b.Warn(UnknownUnit, span, "z")
	b.Warn(MissingUnit, span, "a")

	got := b.Sorted()
	if got[0].Code != MissingUnit || got[1].Code != UnknownUnit {
		t.Errorf("codes = %s, %s; want %s, %s",
			got[0].Code, got[1].Code, MissingUnit, UnknownUnit)
	}
}

func TestBagLimitTruncates(t *testing.T) {
	b := NewBagWithLimit(3)
	for i := 0; i < 100; i++ {
		b.Warn(UnrecognizedToken, source.Span{Start: i, End: i + 1}, "noise")
	}
	if b.Len() != 3 {
		t.Errorf("Len = %d, want 3", b.Len())
	}
	if !b.Truncated() {
		t.Error("Truncated should report true once the limit is reached")
	}

	unlimited := NewBag()
	for i := 0; i < 10; i++ {
		unlimited.Warn(UnrecognizedToken, source.Span{Start: i, End: i + 1}, "noise")
	}
	if unlimited.Truncated() {
		t.Error("an unlimited bag must never report truncation")
	}
}

func TestFilterBySeverity(t *testing.T) {
	b := NewBag()
	b.Hint(UnknownAbbreviation, source.Span{Start: 1, End: 2}, "hint")
	b.Info(DeprecatedSyntax, source.Span{Start: 3, End: 4}, "info")
	b.Warn(UnrecognizedToken, source.Span{Start: 5, End: 6}, "warn")
	b.Error(MalformedRatio, source.Span{Start: 7, End: 8}, "err")

	if got := len(b.Filter(Warning)); got != 2 {
		t.Errorf("Filter(Warning) returned %d, want 2", got)
	}
	if got := len(b.Filter(Hint)); got != 4 {
		t.Errorf("Filter(Hint) returned %d, want 4", got)
	}
	if !b.HasErrors() {
		t.Error("HasErrors should be true")
	}
}

func TestNilBagIsSafe(t *testing.T) {
	// Passes receive a *Bag and should not have to nil-check it at each call.
	var b *Bag
	b.Warn(UnrecognizedToken, source.NoSpan, "ignored")
	b.Add(Diagnostic{})
	if b.Len() != 0 || b.HasErrors() || b.Truncated() || b.Items() != nil {
		t.Error("nil bag should behave as a no-op sink")
	}
	if got := b.Summary(); got != "" {
		t.Errorf("Summary on nil bag = %q, want empty", got)
	}
}

func TestMergePreservesLimit(t *testing.T) {
	dst := NewBagWithLimit(2)
	src := NewBag()
	for i := 0; i < 5; i++ {
		src.Warn(UnrecognizedToken, source.Span{Start: i, End: i + 1}, "x")
	}
	dst.Merge(src)
	if dst.Len() != 2 {
		t.Errorf("Len after Merge = %d, want 2", dst.Len())
	}
	dst.Merge(nil)
	if dst.Len() != 2 {
		t.Errorf("merging nil changed the bag")
	}
}

func TestSummary(t *testing.T) {
	b := NewBag()
	b.Warn(UnrecognizedToken, source.Span{Start: 1, End: 2}, "a")
	b.Warn(UnrecognizedToken, source.Span{Start: 3, End: 4}, "b")
	b.Hint(UnknownAbbreviation, source.Span{Start: 5, End: 6}, "c")

	if got, want := b.Summary(), "2 warnings, 1 hint"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	if got := NewBag().Summary(); got != "" {
		t.Errorf("empty bag Summary = %q, want empty", got)
	}
}

func TestImmutableBuilders(t *testing.T) {
	base := New(MissingUnit, Warning, source.Span{Start: 1, End: 2}, "no unit")
	withRel := base.WithRelated(source.Span{Start: 5, End: 6}, "declared here")
	withFix := base.WithFix(QuickFix{Title: "add mg", Confidence: 0.8})

	if len(base.Related) != 0 || len(base.Fixes) != 0 {
		t.Error("builders must not mutate the receiver")
	}
	if len(withRel.Related) != 1 {
		t.Error("WithRelated did not attach")
	}
	if len(withFix.Fixes) != 1 {
		t.Error("WithFix did not attach")
	}
}

// Severity marshals as a name so API consumers and golden files are readable
// and stable if the constant values are ever renumbered.
func TestSeverityJSONRoundTrip(t *testing.T) {
	d := Diagnostic{
		Code:     MissingFrequency,
		Severity: Warning,
		Span:     source.Span{Start: 3, End: 11},
		Message:  "frequency not specified",
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"severity":"warning"`) {
		t.Errorf("severity did not marshal as a name: %s", b)
	}

	var back Diagnostic
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Code != d.Code || back.Severity != d.Severity ||
		back.Span != d.Span || back.Message != d.Message {
		t.Errorf("round trip = %+v, want %+v", back, d)
	}
}

// Every code the engine can emit must be declared in the registry, so each one
// is documentable and greppable. This is the test that fails when someone adds
// a code constant without registering it.
func TestEveryCodeIsRegistered(t *testing.T) {
	all := Codes()
	if len(all) == 0 {
		t.Fatal("registry is empty")
	}
	seen := make(map[Code]bool, len(all))
	for _, c := range all {
		if seen[c] {
			t.Errorf("duplicate code in registry: %s", c)
		}
		seen[c] = true

		layer, summary, ok := Describe(c)
		if !ok {
			t.Errorf("Describe(%s) reported unknown", c)
		}
		if layer == "" || summary == "" {
			t.Errorf("code %s has an incomplete registry entry", c)
		}
		if !strings.HasPrefix(string(c), "CLN") || len(c) != 7 {
			t.Errorf("code %q does not match the CLNxxxx format", c)
		}
	}

	if _, _, ok := Describe("CLN4001"); ok {
		t.Error("CLN4xxx (terminology) must stay unallocated: ClinLang does not validate clinical content")
	}
	if _, _, ok := Describe("CLN5001"); ok {
		t.Error("CLN5xxx (data sanity) must stay unallocated: ClinLang does not judge clinical values")
	}
}

func TestRenderOneLine(t *testing.T) {
	f := source.NewFile("note.cln", "pt 34M\nvitals bpabc\n")
	d := New(MalformedRatio, Warning, source.Span{Start: 14, End: 19}, "blood pressure is not a ratio")

	got := Render(f, d, RenderOptions{})
	want := "note.cln:2:8: warning: blood pressure is not a ratio [CLN1004]"
	if got != want {
		t.Errorf("Render =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderWithSourceSnippet(t *testing.T) {
	f := source.NewFile("note.cln", "vitals bpabc\n")
	d := New(MalformedRatio, Warning, source.Span{Start: 7, End: 12}, "not a ratio")

	got := Render(f, d, RenderOptions{ShowSource: true})
	wantCaret := "        ^^^^^" // gutter "1 | " is 4 wide, plus 7 columns of "vitals "
	if !strings.Contains(got, "1 | vitals bpabc") {
		t.Errorf("snippet missing source line:\n%s", got)
	}
	if !strings.Contains(got, wantCaret) {
		t.Errorf("caret misaligned:\n%s\nwant a line containing %q", got, wantCaret)
	}
}

func TestRenderWithoutFileOrSpan(t *testing.T) {
	d := New(InternalError, Error, source.NoSpan, "engine invariant violated")
	got := Render(nil, d, RenderOptions{ShowSource: true})
	want := "error: engine invariant violated [CLN9001]"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRenderRelatedAndFixes(t *testing.T) {
	f := source.NewFile("note.cln", "pt 34M\npt 40F\n")
	d := New(DuplicateStatement, Warning, source.Span{Start: 7, End: 9}, "pt already given").
		WithRelated(source.Span{Start: 0, End: 2}, "first pt here").
		WithFix(QuickFix{Title: "remove this line", Confidence: 0.95})

	got := Render(f, d, RenderOptions{})
	for _, want := range []string{
		"note.cln:2:1: warning: pt already given [CLN2007]",
		"note.cln:1:1: note: first pt here",
		"help: remove this line",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render output missing %q:\n%s", want, got)
		}
	}
}

// A span reaching past its line must not draw carets into the next line.
func TestRenderClampsOverlongSpan(t *testing.T) {
	f := source.NewFile("", "ab\ncdefgh\n")
	d := New(UnexpectedToken, Warning, source.Span{Start: 0, End: 8}, "runaway")

	got := Render(f, d, RenderOptions{ShowSource: true})
	lines := strings.Split(got, "\n")
	last := lines[len(lines)-1]
	if strings.Count(last, "^") != 2 {
		t.Errorf("expected the underline clamped to 2 carets, got %q", last)
	}
}

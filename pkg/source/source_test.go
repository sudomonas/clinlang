package source

import (
	"strings"
	"testing"
)

func TestPositionASCII(t *testing.T) {
	f := NewFile("note.cln", "pt 34M\nvitals hr98\n")

	tests := []struct {
		offset    int
		line, col int
	}{
		{0, 1, 1},  // 'p'
		{3, 1, 4},  // '3'
		{6, 1, 7},  // the newline itself, at end of line 1
		{7, 2, 1},  // 'v' on line 2
		{14, 2, 8}, // 'h' of hr98
		{19, 3, 1}, // past the trailing newline: an empty third line
	}
	for _, tc := range tests {
		got := f.Position(tc.offset)
		if got.Line != tc.line || got.Col != tc.col {
			t.Errorf("Position(%d) = line %d col %d, want line %d col %d",
				tc.offset, got.Line, got.Col, tc.line, tc.col)
		}
	}
}

// A file with no trailing newline still reports its final partial line, and an
// empty file has exactly one line. Off-by-one here would misplace every
// diagnostic on the last line of a note, which is where clinicians type.
func TestLineCount(t *testing.T) {
	tests := []struct {
		content string
		want    int
	}{
		{"", 1},
		{"a", 1},
		{"a\n", 2},
		{"a\nb", 2},
		{"a\nb\n", 3},
		{"\n\n", 3},
	}
	for _, tc := range tests {
		if got := NewFile("", tc.content).LineCount(); got != tc.want {
			t.Errorf("LineCount(%q) = %d, want %d", tc.content, got, tc.want)
		}
	}
}

// Non-ASCII text is the case the previous engine's hand-rolled string helpers
// got wrong. A micro sign is two bytes but one column, and an emoji is four
// bytes, one rune, but two UTF-16 units — which is what an editor needs.
func TestPositionUnicode(t *testing.T) {
	f := NewFile("", "dose 5µg\n")

	// 'µ' is at byte offset 6 and is 2 bytes wide, so 'g' is at byte 8.
	p := f.Position(8)
	if p.Line != 1 {
		t.Fatalf("line = %d, want 1", p.Line)
	}
	if p.Col != 8 {
		t.Errorf("Col = %d, want 8 (runes, 1-based)", p.Col)
	}
	if p.UTF16Col != 7 {
		t.Errorf("UTF16Col = %d, want 7 (0-based)", p.UTF16Col)
	}
}

func TestPositionAstralPlane(t *testing.T) {
	// A rune above U+FFFF occupies two UTF-16 code units.
	f := NewFile("", "x🩺y")
	p := f.Position(len("x🩺")) // at 'y'
	if p.Col != 3 {
		t.Errorf("Col = %d, want 3", p.Col)
	}
	if p.UTF16Col != 3 {
		t.Errorf("UTF16Col = %d, want 3 (x=1 + surrogate pair=2)", p.UTF16Col)
	}
}

func TestPositionClampsOutOfRange(t *testing.T) {
	f := NewFile("", "abc")
	if got := f.Position(-5); got.Offset != 0 || got.Line != 1 || got.Col != 1 {
		t.Errorf("Position(-5) = %+v, want start of file", got)
	}
	if got := f.Position(999); got.Offset != 3 || got.Line != 1 || got.Col != 4 {
		t.Errorf("Position(999) = %+v, want end of file", got)
	}
}

func TestLineSpanStripsLineEndings(t *testing.T) {
	f := NewFile("", "alpha\r\nbeta\ngamma")

	if got := f.LineText(1); got != "alpha" {
		t.Errorf("LineText(1) = %q, want %q", got, "alpha")
	}
	if got := f.LineText(2); got != "beta" {
		t.Errorf("LineText(2) = %q, want %q", got, "beta")
	}
	if got := f.LineText(3); got != "gamma" {
		t.Errorf("LineText(3) = %q, want %q", got, "gamma")
	}
	if got := f.LineSpan(0); got != NoSpan {
		t.Errorf("LineSpan(0) = %+v, want NoSpan", got)
	}
	if got := f.LineSpan(99); got != NoSpan {
		t.Errorf("LineSpan(99) = %+v, want NoSpan", got)
	}
}

func TestTextClampsRatherThanPanics(t *testing.T) {
	f := NewFile("", "abcdef")
	cases := []Span{
		{Start: 2, End: 4},
		{Start: -10, End: 3},
		{Start: 4, End: 999},
		{Start: 5, End: 1}, // inverted
		{Start: 100, End: 200},
	}
	want := []string{"cd", "abc", "ef", "", ""}
	for i, s := range cases {
		if got := f.Text(s); got != want[i] {
			t.Errorf("Text(%+v) = %q, want %q", s, got, want[i])
		}
	}
}

func TestOffsetRoundTrips(t *testing.T) {
	content := "pt 34M\nvitals bp120/80 hr98\nrx amoxicillin 500mg\n"
	f := NewFile("", content)

	for off := 0; off <= len(content); off++ {
		p := f.Position(off)
		if got := f.Offset(p.Line, p.Col); got != off {
			t.Fatalf("Offset(Position(%d)) = %d, want %d", off, got, off)
		}
	}
}

func TestSpanMerge(t *testing.T) {
	a := Span{Start: 5, End: 10}
	b := Span{Start: 8, End: 20}

	if got := Merge(a, b); got != (Span{Start: 5, End: 20}) {
		t.Errorf("Merge = %+v, want {5 20}", got)
	}
	// NoSpan is absorbed, so folding over synthesised children still works.
	if got := Merge(a, NoSpan); got != a {
		t.Errorf("Merge(a, NoSpan) = %+v, want %+v", got, a)
	}
	if got := Merge(NoSpan, b); got != b {
		t.Errorf("Merge(NoSpan, b) = %+v, want %+v", got, b)
	}
	if got := Merge(NoSpan, NoSpan); got != NoSpan {
		t.Errorf("Merge(NoSpan, NoSpan) = %+v, want NoSpan", got)
	}
	if got := MergeAll(NoSpan, b, a, NoSpan); got != (Span{Start: 5, End: 20}) {
		t.Errorf("MergeAll = %+v, want {5 20}", got)
	}
}

func TestSpanPredicates(t *testing.T) {
	s := Span{Start: 4, End: 8}

	if !s.Contains(4) || !s.Contains(7) {
		t.Error("Contains should include Start and the byte before End")
	}
	if s.Contains(8) {
		t.Error("Contains should exclude End for a non-empty span")
	}
	if s.Contains(3) {
		t.Error("Contains should exclude offsets before Start")
	}

	// An empty span is an insertion point and contains exactly its offset,
	// so "missing token here" diagnostics are hit-testable in an editor.
	empty := Span{Start: 4, End: 4}
	if !empty.Contains(4) {
		t.Error("empty span should contain its own offset")
	}
	if empty.Contains(5) {
		t.Error("empty span should contain only its own offset")
	}

	if !s.Overlaps(Span{Start: 7, End: 9}) {
		t.Error("expected overlap")
	}
	if s.Overlaps(Span{Start: 8, End: 12}) {
		t.Error("adjacent spans should not overlap")
	}

	if NoSpan.IsValid() {
		t.Error("NoSpan must not be valid")
	}
	if !(Span{Start: 3, End: 3}).IsValid() {
		t.Error("a zero-length span at a real offset must be valid")
	}
}

func TestLargeFileIndexing(t *testing.T) {
	// Guards against the line index degrading on realistic multi-line input.
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("vitals hr98 bp120/80\n")
	}
	f := NewFile("big.cln", sb.String())

	if f.LineCount() != 5001 {
		t.Fatalf("LineCount = %d, want 5001", f.LineCount())
	}
	p := f.Position(f.Offset(2500, 8))
	if p.Line != 2500 || p.Col != 8 {
		t.Errorf("round trip at line 2500 gave line %d col %d", p.Line, p.Col)
	}
}

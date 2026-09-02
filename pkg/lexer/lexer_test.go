package lexer

import (
	"strings"
	"testing"

	"clinlang/pkg/diag"
	"clinlang/pkg/source"
)

// lex is a test helper returning tokens without the trailing EOF.
func lex(t *testing.T, src string) ([]Token, *diag.Bag) {
	t.Helper()
	f := source.NewFile("test.cln", src)
	bag := diag.NewBag()
	toks := Lex(f, bag)
	if len(toks) == 0 || toks[len(toks)-1].Kind != EOF {
		t.Fatalf("stream must end with exactly one EOF, got %v", toks)
	}
	return toks[:len(toks)-1], bag
}

// summarize renders a token stream compactly: Kind(text) per token, with a
// leading dot marking adjacency to the previous token.
func summarize(toks []Token) string {
	var parts []string
	for _, tk := range toks {
		s := tk.Kind.String()
		if tk.Kind != Newline {
			s += "(" + tk.Text + ")"
		}
		if !tk.SpaceBefore {
			s = "." + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func TestLexBasicShapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The case five different recognizers answered five ways.
			name: "glued key and value",
			src:  "hb13.5",
			want: "Word(hb13.5)",
		},
		{
			name: "separated key and value",
			src:  "hb 13.5",
			want: "Word(hb) Number(13.5)",
		},
		{
			// Adjacency is the whole distinction, and it is recorded.
			name: "blood pressure ratio",
			src:  "bp140/90",
			want: "Word(bp140) .Slash(/) .Number(90)",
		},
		{
			name: "explicit colon key",
			src:  "ga:34w",
			want: "Word(ga) .Colon(:) .Number(34) .Word(w)",
		},
		{
			name: "dose with unit",
			src:  "500mg",
			want: "Number(500) .Word(mg)",
		},
		{
			name: "intensity run then duration",
			src:  "chestpain+++4h",
			want: "Word(chestpain) .Qualifier(+++) .Number(4) .Word(h)",
		},
		{
			name: "age and sex",
			src:  "34M",
			want: "Number(34) .Word(M)",
		},
		{
			name: "percentage",
			src:  "98%",
			want: "Number(98) .Percent(%)",
		},
		{
			name: "pragma",
			src:  "@profile obgyn",
			want: "At(@) .Word(profile) Word(obgyn)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toks, bag := lex(t, tc.src)
			if got := summarize(toks); got != tc.want {
				t.Errorf("lex(%q)\n got: %s\nwant: %s", tc.src, got, tc.want)
			}
			if bag.Len() != 0 {
				t.Errorf("unexpected diagnostics: %v", bag.Items())
			}
		})
	}
}

// pco2 and hb13 are lexically identical — letters then digits — so the lexer
// keeps both whole and defers the split to the lexicon-aware parser. This is
// the honest form of the fix: the escape list becomes data, not nothing.
func TestAnalyteNamesStayWhole(t *testing.T) {
	for _, src := range []string{"pco2", "hba1c", "b12", "fio2", "spo2", "t3", "co2"} {
		toks, bag := lex(t, src)
		if len(toks) != 1 || toks[0].Kind != Word || toks[0].Text != src {
			t.Errorf("lex(%q) = %s, want a single Word", src, summarize(toks))
		}
		if bag.Len() != 0 {
			t.Errorf("lex(%q) produced diagnostics: %v", src, bag.Items())
		}
	}
}

// The hyphen rules are what fix the mangling of "non-productive-cough", which
// the old parseSymptomToken reduced to name="non", intensity="improving".
func TestHyphenRules(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		// Hyphen before a letter continues the word.
		{"non-productive-cough", "Word(non-productive-cough)"},
		{"x-ray", "Word(x-ray)"},
		// Hyphen before a digit does not, so trailing intensity still reads.
		{"pain-3d", "Word(pain) .Qualifier(-) .Number(3) .Word(d)"},
		{"pain--3d", "Word(pain) .Qualifier(--) .Number(3) .Word(d)"},
		{"dengue-", "Word(dengue) .Qualifier(-)"},
		{"crp+++", "Word(crp) .Qualifier(+++)"},
		// Mixed runs never merge: a typo stays visible.
		{"a+-", "Word(a) .Qualifier(+) .Qualifier(-)"},
	}
	for _, tc := range tests {
		toks, _ := lex(t, tc.src)
		if got := summarize(toks); got != tc.want {
			t.Errorf("lex(%q)\n got: %s\nwant: %s", tc.src, got, tc.want)
		}
	}
}

func TestDotRules(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		// A dot between digits is part of the number.
		{"hb13.5", "Word(hb13.5)"},
		{"0.5", "Number(0.5)"},
		// A sentence-ending dot is inert punctuation, never a diagnostic.
		{"fever.", "Word(fever) .Punct(.)"},
		{"cough. fever", "Word(cough) .Punct(.) Word(fever)"},
		// Only one decimal point is absorbed, so a typo does not become a
		// silently wrong value.
		{"1.2.3", "Number(1.2) .Punct(.) .Number(3)"},
	}
	for _, tc := range tests {
		toks, bag := lex(t, tc.src)
		if got := summarize(toks); got != tc.want {
			t.Errorf("lex(%q)\n got: %s\nwant: %s", tc.src, got, tc.want)
		}
		if bag.Len() != 0 {
			t.Errorf("lex(%q) produced diagnostics: %v", tc.src, bag.Items())
		}
	}
}

func TestISODateVersusRatio(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"2025-06-15", "Date(2025-06-15)"},
		// Not a date: trailing digit means it is something else.
		{"2025-06-155", "Number(2025) .Qualifier(-) .Number(06) .Qualifier(-) .Number(155)"},
		// Slash dates stay ratios: bp140/90 is far more common than 12/25.
		{"12/25/2025", "Number(12) .Slash(/) .Number(25) .Slash(/) .Number(2025)"},
	}
	for _, tc := range tests {
		toks, _ := lex(t, tc.src)
		if got := summarize(toks); got != tc.want {
			t.Errorf("lex(%q)\n got: %s\nwant: %s", tc.src, got, tc.want)
		}
	}
}

func TestComments(t *testing.T) {
	toks, bag := lex(t, "pt 34M # a hash comment\nvitals hr98 // a slash comment\n")
	got := summarize(toks)
	// The newline is adjacent to the comment: nothing separates them.
	want := "Word(pt) Number(34) .Word(M) Comment(# a hash comment) .Newline " +
		"Word(vitals) Word(hr98) Comment(// a slash comment) .Newline"
	if got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

func TestStrings(t *testing.T) {
	toks, bag := lex(t, `rx "vitamin b12" 500mcg`)
	want := `Word(rx) String("vitamin b12") Number(500) .Word(mcg)`
	if got := summarize(toks); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

// An unterminated quote stops at the newline rather than swallowing the note.
func TestUnterminatedStringStopsAtNewline(t *testing.T) {
	toks, bag := lex(t, "cc \"chest pain\nvitals hr98\n")

	if bag.Len() != 1 || bag.Items()[0].Code != diag.UnterminatedString {
		t.Fatalf("want one UnterminatedString diagnostic, got %v", bag.Items())
	}
	// The following line must still lex normally.
	if !strings.Contains(summarize(toks), "Word(vitals)") {
		t.Errorf("lexing did not recover on the next line: %s", summarize(toks))
	}
}

// Unicode-correct word handling. The previous engine reimplemented ToLower and
// Fields over ASCII byte ranges, which corrupted every one of these.
func TestUnicodeWords(t *testing.T) {
	for _, src := range []string{"µg", "Löfgren", "Ménière", "Ωcm", "мкг"} {
		toks, bag := lex(t, src)
		if len(toks) != 1 || toks[0].Kind != Word || toks[0].Text != src {
			t.Errorf("lex(%q) = %s, want a single Word", src, summarize(toks))
		}
		if bag.Len() != 0 {
			t.Errorf("lex(%q) produced diagnostics: %v", src, bag.Items())
		}
	}
}

func TestControlCharacterDiagnosed(t *testing.T) {
	_, bag := lex(t, "pt 34M\x00\n")
	if bag.Len() != 1 || bag.Items()[0].Code != diag.InvalidCharacter {
		t.Fatalf("want one InvalidCharacter diagnostic, got %v", bag.Items())
	}
}

func TestSpansAreExactAndContiguous(t *testing.T) {
	src := "pt 34M wt65kg\nvitals bp140/90\n"
	f := source.NewFile("test.cln", src)
	toks := Lex(f, nil)

	prevEnd := 0
	for _, tk := range toks {
		if tk.Kind == EOF {
			if tk.Span.Start != len(src) {
				t.Errorf("EOF span starts at %d, want %d", tk.Span.Start, len(src))
			}
			continue
		}
		// Text must be exactly what the span covers.
		if got := f.Text(tk.Span); got != tk.Text {
			t.Errorf("token %v text %q does not match its span text %q", tk.Kind, tk.Text, got)
		}
		// Gaps are only ever whitespace.
		if gap := src[prevEnd:tk.Span.Start]; strings.TrimSpace(gap) != "" {
			t.Errorf("non-whitespace gap %q before token %v", gap, tk)
		}
		prevEnd = tk.Span.End
	}
}

// Concatenating token text plus the whitespace between spans must reproduce
// the input exactly. This underwrites lossless AST printing and `clinlang fmt`.
func TestLosslessRoundTrip(t *testing.T) {
	inputs := []string{
		"",
		"\n",
		"pt 34M\n",
		"pt 34M wt65kg ht170cm\nvitals bp140/90 hr98 temp37.2c\nrx amoxicillin 500mg tds po x7d\n",
		"@profile obgyn\npt 28F ga:34w\n# comment\n\n  indented   spacing\t\ttabs\n",
		"sx non-productive-cough+++ pain--3d\nlab hb13.5 pco2:45 hiv-\n",
		"cc \"chest pain\" radiating.\n",
		"µg Löfgren 2025-06-15\r\ntrailing no newline",
	}

	for _, src := range inputs {
		f := source.NewFile("rt.cln", src)
		toks := Lex(f, nil)

		var sb strings.Builder
		prevEnd := 0
		for _, tk := range toks {
			sb.WriteString(src[prevEnd:tk.Span.Start]) // whitespace between tokens
			sb.WriteString(f.Text(tk.Span))
			prevEnd = tk.Span.End
		}
		sb.WriteString(src[prevEnd:])

		if got := sb.String(); got != src {
			t.Errorf("round trip failed\n src: %q\n got: %q", src, got)
		}
	}
}

func TestAdjacencyAcrossLines(t *testing.T) {
	toks, _ := lex(t, "pt\nvitals")
	// The command starting line 2 must not be reported as adjacent to the
	// newline before it.
	for _, tk := range toks {
		if tk.Text == "vitals" && !tk.SpaceBefore {
			t.Error("first token of a line must report SpaceBefore")
		}
	}
}

func TestEmptyAndWhitespaceOnly(t *testing.T) {
	for _, src := range []string{"", "   ", "\t\t", "   \t  "} {
		f := source.NewFile("", src)
		toks := Lex(f, nil)
		if len(toks) != 1 || toks[0].Kind != EOF {
			t.Errorf("lex(%q) = %v, want only EOF", src, toks)
		}
	}
}

func TestRealisticNote(t *testing.T) {
	src := "@profile obgyn\n" +
		"pt 28F wt65 ga:34w\n" +
		"vitals bp120/75 fhr:142\n" +
		"sx nausea++ back-pain+ 3d\n" +
		"lab hb11.2 pco2:38 hiv-\n" +
		"rx amoxicillin 500mg tds po x7d\n"

	toks, bag := lex(t, src)
	if bag.Len() != 0 {
		t.Fatalf("realistic input produced diagnostics: %v", bag.Items())
	}

	// Spot-check the shapes that used to be mangled.
	var found []string
	for _, tk := range toks {
		if tk.Kind == Word {
			found = append(found, tk.Text)
		}
	}
	joined := strings.Join(found, " ")
	for _, want := range []string{"back-pain", "hb11.2", "pco2", "amoxicillin"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected word %q in %q", want, joined)
		}
	}
}

// FuzzLex asserts the lexer terminates, never panics, always emits exactly one
// trailing EOF, and always produces contiguous non-overlapping spans.
func FuzzLex(f *testing.F) {
	seeds := []string{
		"", "pt 34M", "hb13.5", "bp140/90", "@profile obgyn", "ga:34w",
		"chestpain+++4h", "\"unterminated", "1.2.3", "2025-06-15",
		"µg", "\x00\x01", "---", "+++", "//", "#", "a-", "-a", "..", "::",
		"999999999999999999999999.99999", strings.Repeat("a", 1000),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		file := source.NewFile("fuzz.cln", src)
		toks := Lex(file, diag.NewBag())

		if len(toks) == 0 {
			t.Fatal("no tokens emitted")
		}
		eofCount := 0
		prevEnd := 0
		for i, tk := range toks {
			if tk.Kind == EOF {
				eofCount++
				if i != len(toks)-1 {
					t.Fatalf("EOF at position %d is not last", i)
				}
			}
			if tk.Span.Start < prevEnd {
				t.Fatalf("token %d span %+v overlaps previous end %d", i, tk.Span, prevEnd)
			}
			if tk.Span.End < tk.Span.Start {
				t.Fatalf("token %d has inverted span %+v", i, tk.Span)
			}
			if tk.Span.End > len(src) {
				t.Fatalf("token %d span %+v exceeds input length %d", i, tk.Span, len(src))
			}
			if tk.Kind != EOF && tk.Span.Len() == 0 {
				t.Fatalf("token %d (%v) is zero-length", i, tk.Kind)
			}
			prevEnd = tk.Span.End
		}
		if eofCount != 1 {
			t.Fatalf("got %d EOF tokens, want exactly 1", eofCount)
		}
	})
}

// A timestamp must lex as a single token. Were "14:20" three tokens, an
// encounter line would look to the parser like a keyed argument.
func TestISOTimestamps(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"2026-08-01", "Date(2026-08-01)"},
		{"2026-08-01T14:20", "Date(2026-08-01T14:20)"},
		{"2026-08-01T14:20:05", "Date(2026-08-01T14:20:05)"},
		// A malformed time is not absorbed: the date still stands alone.
		{"2026-08-01T14", "Number(2026) .Qualifier(-) .Number(08) .Qualifier(-) .Number(01) .Word(T14)"},
	}
	for _, tc := range tests {
		toks, bag := lex(t, tc.src)
		if got := summarize(toks); got != tc.want {
			t.Errorf("lex(%q)\n got: %s\nwant: %s", tc.src, got, tc.want)
		}
		if bag.Len() != 0 {
			t.Errorf("lex(%q) produced diagnostics: %v", tc.src, bag.Items())
		}
	}
}

// The colon inside a time must not read as a key delimiter.
func TestTimestampColonIsNotAKey(t *testing.T) {
	toks, _ := lex(t, "enc 2026-08-01T14:20 er unit:casualty")
	got := summarize(toks)
	if !strings.Contains(got, "Date(2026-08-01T14:20)") {
		t.Errorf("timestamp did not lex as one token: %s", got)
	}
	// Exactly one colon token, belonging to unit:.
	if n := strings.Count(got, "Colon"); n != 1 {
		t.Errorf("expected 1 colon token (unit:), got %d: %s", n, got)
	}
}

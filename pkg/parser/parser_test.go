package parser

import (
	"strings"
	"testing"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/source"
)

func parse(t *testing.T, src string, keys ...string) (*ast.Document, *diag.Bag) {
	t.Helper()
	f := source.NewFile("test.cln", src)
	bag := diag.NewBag()
	g := NewStaticGrammar(CoreShapes, keys)
	return Parse(f, g, bag), bag
}

// dumpArgs renders one statement's arguments for compact comparison.
func dumpArgs(t *testing.T, doc *ast.Document) string {
	t.Helper()
	if len(doc.Statements) != 1 {
		t.Fatalf("expected exactly 1 statement, got %d", len(doc.Statements))
	}
	var sb strings.Builder
	for _, a := range doc.Statements[0].Args {
		sb.WriteString(ast.Dump(a))
	}
	return sb.String()
}

func TestGluedMeasurement(t *testing.T) {
	doc, bag := parse(t, "lab hb13.5")
	want := `Measurement
  Ident "hb"
  Number "13.5"
`
	if got := dumpArgs(t, doc); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

// Blood pressure becomes a structured ratio at parse time. The previous engine
// stored the string "140/90" with no validation and re-parsed it in a different
// layer, which is the FHIR blocker in miniature and also let "bpabc" through.
func TestBloodPressureIsStructured(t *testing.T) {
	doc, bag := parse(t, "vitals bp140/90")
	want := `Measurement
  Ident "bp"
  Ratio
    Number "140"
    Number "90"
`
	if got := dumpArgs(t, doc); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

func TestMalformedRatioIsDiagnosedNotSwallowed(t *testing.T) {
	doc, bag := parse(t, "vitals bp140/ hr98")

	if bag.Len() != 1 || bag.Items()[0].Code != diag.MalformedRatio {
		t.Fatalf("want one MalformedRatio diagnostic, got %v", bag.Items())
	}
	// Recovery: the rest of the line still parses.
	if n := len(doc.Statements[0].Args); n != 2 {
		t.Errorf("expected 2 arguments after recovery, got %d", n)
	}
}

// "bpabc" used to be stored happily as a blood pressure. Now the tail is
// recorded as text rather than silently accepted as a reading, so the semantic
// layer can reject it with a position.
// A key claims a word only when a value follows it, so "bpabc" is not a blood
// pressure with a nonsense reading; it is simply an unrecognised word, and the
// semantic layer reports it with a position.
func TestKeyRequiresAValueToClaimAWord(t *testing.T) {
	for _, keys := range [][]string{{"bp"}, nil} {
		doc, _ := parse(t, "vitals bpabc", keys...)
		if !strings.Contains(dumpArgs(t, doc), `Ident "bpabc"`) {
			t.Errorf("keys=%v: expected an unsplit concept, got:\n%s", keys, dumpArgs(t, doc))
		}
	}

	// With a digit following, the same key does claim the word.
	doc, _ := parse(t, "vitals bp140", "bp")
	if !strings.Contains(dumpArgs(t, doc), `Ident "bp"`) {
		t.Errorf("expected bp to split before a digit, got:\n%s", dumpArgs(t, doc))
	}
}

// The rule that a key must be followed by a digit is what stops the chloride
// key "cl" from splitting "clopidogrel" and the route alias "in" from splitting
// "insulin". Real drug names from the corpus, all of which begin with a
// registered measurement key.
func TestSplitRuleDoesNotCorruptDrugNames(t *testing.T) {
	keys := []string{"cl", "cr", "k", "na", "hb", "ph", "alb", "in", "alt", "ast"}
	names := []string{
		"clopidogrel", "clarithromycin", "creatinine", "ketamine",
		"naloxone", "haloperidol", "phenytoin", "albendazole",
		"insulin", "alteplase", "astemizole",
	}
	for _, name := range names {
		doc, _ := parse(t, "rx "+name+" 5mg", keys...)
		if !strings.Contains(dumpArgs(t, doc), `Ident "`+name+`"`) {
			t.Errorf("%q was split by a measurement key:\n%s", name, dumpArgs(t, doc))
		}
	}
}

func TestGrammarSplitKeyRule(t *testing.T) {
	g := NewStaticGrammar(nil, []string{"b12", "hb", "hba1c", "a1c", "pco2", "cl", "in"})

	tests := []struct {
		word string
		want int
		why  string
	}{
		{"pco2", 4, "key consumes the whole word"},
		{"pco245", 4, "key followed by a value"},
		{"hba1c6.2", 5, "longest key wins over hb"},
		{"hb13.5", 2, "key followed by a digit"},
		{"clopidogrel", 0, "cl is not followed by a digit"},
		{"insulin", 0, "in is not followed by a digit"},
		{"wbc12", 0, "no registered key"},
	}
	for _, tc := range tests {
		if got := g.SplitKey(tc.word); got != tc.want {
			t.Errorf("SplitKey(%q) = %d, want %d (%s)", tc.word, got, tc.want, tc.why)
		}
	}
}

// Rates and per-unit doses are one unit, not a ratio: a ratio's sides are
// numbers, and these are words.
func TestCompoundUnits(t *testing.T) {
	tests := map[string]string{
		"rx fluids 100ml/hr":   "ml/hr",
		"rx gentamicin 5mg/kg": "mg/kg",
		"rx drug 30mg/day":     "mg/day",
	}
	for src, wantUnit := range tests {
		doc, bag := parse(t, src)
		got := dumpArgs(t, doc)
		if !strings.Contains(got, `Unit "`+wantUnit+`"`) {
			t.Errorf("parse(%q) did not produce unit %q:\n%s", src, wantUnit, got)
		}
		if bag.Len() != 0 {
			t.Errorf("parse(%q) diagnostics: %v", src, bag.Items())
		}
	}

	// A blood pressure is still a ratio: both sides are numbers.
	doc, _ := parse(t, "vitals bp140/90", "bp")
	if !strings.Contains(dumpArgs(t, doc), "Ratio") {
		t.Errorf("bp140/90 should still parse as a ratio:\n%s", dumpArgs(t, doc))
	}
}

// "@obgyn+peds" is the short form of "@profile obgyn+peds".
func TestStackedProfileShorthand(t *testing.T) {
	doc, bag := parse(t, "@obgyn+peds\npt 28F\n")
	if len(doc.Pragmas) != 1 {
		t.Fatalf("pragmas = %d, want 1", len(doc.Pragmas))
	}
	if got := doc.Pragmas[0].Name.Raw; got != "obgyn+peds" {
		t.Errorf("pragma name = %q, want %q", got, "obgyn+peds")
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

func TestExplicitColonMeasurement(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"pt ga:34w", `Measurement explicit
  Ident "ga"
  Number "34"
  Unit "w"
`},
		{"ix cxr:wnl", `Measurement explicit
  Ident "cxr"
  Text "wnl"
`},
		{"lab pco2:45", `Measurement explicit
  Ident "pco2"
  Number "45"
`},
	}
	for _, tc := range tests {
		doc, bag := parse(t, tc.src)
		if got := dumpArgs(t, doc); got != tc.want {
			t.Errorf("parse(%q)\ngot:\n%s\nwant:\n%s", tc.src, got, tc.want)
		}
		if bag.Len() != 0 {
			t.Errorf("parse(%q) diagnostics: %v", tc.src, bag.Items())
		}
	}
}

// The hyphen defect: parseSymptomToken searched for "-" anywhere in the token,
// so "non-productive-cough" parsed as name "non", intensity "improving", and
// the rest was silently discarded.
func TestHyphenatedConceptSurvives(t *testing.T) {
	doc, bag := parse(t, "sx non-productive-cough")
	want := `ConceptRef
  Ident "non-productive-cough"
`
	if got := dumpArgs(t, doc); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

func TestConceptWithGradeAndDuration(t *testing.T) {
	doc, bag := parse(t, "sx chestpain+++4h")
	want := `ConceptRef
  Ident "chestpain"
  Qualifier "+++"
  Duration
    Number "4"
    Unit "h"
`
	if got := dumpArgs(t, doc); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

// Intensity is a trailing run, so a hyphen inside a name and a hyphen marking
// "improving" no longer collide.
func TestTrailingVersusInternalHyphen(t *testing.T) {
	doc, _ := parse(t, "sx back-pain-- pain-3d")
	args := doc.Statements[0].Args
	if len(args) != 2 {
		t.Fatalf("expected 2 arguments, got %d:\n%s", len(args), ast.Dump(doc))
	}

	first, ok := args[0].(*ast.ConceptRef)
	if !ok || first.Name.Raw != "back-pain" {
		t.Fatalf("first argument = %s, want ConceptRef back-pain", ast.Dump(args[0]))
	}
	if first.Grade == nil || first.Grade.Raw != "--" {
		t.Errorf("expected trailing -- as grade, got %s", ast.Dump(args[0]))
	}

	second, ok := args[1].(*ast.ConceptRef)
	if !ok || second.Name.Raw != "pain" {
		t.Fatalf("second argument = %s, want ConceptRef pain", ast.Dump(args[1]))
	}
	if second.Grade == nil || second.Grade.Raw != "-" {
		t.Errorf("expected - as grade on pain-3d, got %s", ast.Dump(args[1]))
	}
	if second.Duration == nil || second.Duration.Number.Raw != "3" {
		t.Errorf("expected duration 3d, got %s", ast.Dump(args[1]))
	}
}

// The lexicon resolves the pco2-versus-hb13 ambiguity. Without a registered
// key, "pco245" splits at the first digit; with one, the key wins.
func TestLexiconDrivenKeySplit(t *testing.T) {
	t.Run("without lexicon", func(t *testing.T) {
		doc, _ := parse(t, "lab pco245")
		got := dumpArgs(t, doc)
		if !strings.Contains(got, `Ident "pco"`) {
			t.Errorf("expected the default first-digit split, got:\n%s", got)
		}
	})

	t.Run("with lexicon", func(t *testing.T) {
		doc, _ := parse(t, "lab pco245", "pco2", "hba1c", "b12")
		want := `Measurement
  Ident "pco2"
  Number "45"
`
		if got := dumpArgs(t, doc); got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("longest key wins", func(t *testing.T) {
		// "a1c" and "hba1c" both prefix-match nothing here, but "hba1c" must
		// beat a shorter registered key that also matches.
		doc, _ := parse(t, "lab hba1c6.2", "hb", "hba1c")
		want := `Measurement
  Ident "hba1c"
  Number "6.2"
`
		if got := dumpArgs(t, doc); got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("bare lexicon key is a concept", func(t *testing.T) {
		doc, _ := parse(t, "lab pco2", "pco2")
		want := `ConceptRef
  Ident "pco2"
`
		if got := dumpArgs(t, doc); got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
}

func TestQuantityAndDuration(t *testing.T) {
	doc, bag := parse(t, "rx amoxicillin 500mg tds po x7d")
	if bag.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %v", bag.Items())
	}

	args := doc.Statements[0].Args
	if len(args) != 5 {
		t.Fatalf("expected 5 arguments, got %d:\n%s", len(args), ast.Dump(doc))
	}

	// The parser records shapes only. It does not decide that tds is a
	// frequency and po a route — that classification needs the lexicon and
	// belongs to the semantic layer.
	if _, ok := args[0].(*ast.ConceptRef); !ok {
		t.Errorf("drug name should be a ConceptRef, got %T", args[0])
	}
	q, ok := args[1].(*ast.Quantity)
	if !ok || q.Number.Val != 500 || q.Unit.Raw != "mg" {
		t.Errorf("dose should be Quantity 500mg, got %s", ast.Dump(args[1]))
	}
	if _, ok := args[2].(*ast.ConceptRef); !ok {
		t.Errorf("tds should be an unclassified ConceptRef, got %T", args[2])
	}
	if _, ok := args[3].(*ast.ConceptRef); !ok {
		t.Errorf("po should be an unclassified ConceptRef, got %T", args[3])
	}
	d, ok := args[4].(*ast.Duration)
	if !ok || !d.Prefixed || d.Number.Val != 7 || d.Unit.Raw != "d" {
		t.Errorf("x7d should be a prefixed Duration, got %s", ast.Dump(args[4]))
	}
}

func TestFreeTextCommandsArePreservedVerbatim(t *testing.T) {
	src := "cc  severe chest pain, radiating to left arm.  \n"
	doc, bag := parse(t, src)

	st := doc.Statements[0]
	if st.Trailing == nil {
		t.Fatal("expected trailing free text")
	}
	want := "severe chest pain, radiating to left arm."
	if st.Trailing.Raw != want {
		t.Errorf("free text = %q, want %q", st.Trailing.Raw, want)
	}
	if len(st.Args) != 0 {
		t.Errorf("free-text command must not produce structured args, got %d", len(st.Args))
	}
	if bag.Len() != 0 {
		t.Errorf("unexpected diagnostics: %v", bag.Items())
	}
}

func TestFreeTextExcludesTrailingComment(t *testing.T) {
	doc, _ := parse(t, "hpi worsening overnight # reviewed by SHO\n")
	if got := doc.Statements[0].Trailing.Raw; got != "worsening overnight" {
		t.Errorf("free text = %q, want %q", got, "worsening overnight")
	}
	if len(doc.Comments) != 1 {
		t.Errorf("expected the comment to be retained, got %d", len(doc.Comments))
	}
}

func TestPragmas(t *testing.T) {
	doc, bag := parse(t, "@profile obgyn\n@clinlang 1.0\npt 34M\n")
	if bag.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %v", bag.Items())
	}
	if len(doc.Pragmas) != 2 {
		t.Fatalf("expected 2 pragmas, got %d", len(doc.Pragmas))
	}
	if doc.FindPragma("profile") == nil {
		t.Error("FindPragma(profile) returned nil")
	}
	if doc.FindPragma("clinlang") == nil {
		t.Error("FindPragma(clinlang) returned nil")
	}
	// Pragmas are collected separately, so a later pre-pass can build scope
	// before any statement is walked. Order no longer constrains the user.
	if len(doc.Statements) != 1 {
		t.Errorf("expected 1 statement, got %d", len(doc.Statements))
	}
}

func TestErrorRecoveryPerStatement(t *testing.T) {
	// A malformed line must not kill the parse.
	doc, bag := parse(t, "pt 34M\n+++\nvitals hr98\n")

	if len(doc.Statements) != 2 {
		t.Fatalf("expected 2 statements to survive, got %d:\n%s", len(doc.Statements), ast.Dump(doc))
	}
	if doc.Statements[1].Command.Raw != "vitals" {
		t.Errorf("recovery lost the following statement: %s", ast.Dump(doc))
	}
	if bag.Len() == 0 {
		t.Error("expected a diagnostic for the stray qualifier line")
	}
}

func TestAlwaysProducesCompleteDocument(t *testing.T) {
	// Half-typed input is the normal state of a live editor buffer.
	partials := []string{
		"p", "pt ", "pt 34", "vitals bp", "vitals bp140/", "rx amox 500",
		"ga:", "@", "@profile", "sx cough+", `cc "unclosed`,
	}
	for _, src := range partials {
		doc, _ := parse(t, src)
		if doc == nil {
			t.Fatalf("parse(%q) returned nil", src)
		}
		// Must terminate and be walkable without panicking.
		count := 0
		ast.Walk(doc, func(ast.Node) bool { count++; return true })
		if count == 0 {
			t.Errorf("parse(%q) produced an unwalkable tree", src)
		}
	}
}

func TestSpansAreExact(t *testing.T) {
	src := "vitals bp140/90 hr98"
	f := source.NewFile("t.cln", src)
	doc := Parse(f, DefaultGrammar(), nil)

	st := doc.Statements[0]
	if got := f.Text(st.Command.Sp); got != "vitals" {
		t.Errorf("command span covers %q, want %q", got, "vitals")
	}

	m := st.Args[0].(*ast.Measurement)
	if got := f.Text(m.Key.Sp); got != "bp" {
		t.Errorf("key span covers %q, want %q", got, "bp")
	}
	ratio := m.Value.(*ast.Ratio)
	if got := f.Text(ratio.Numerator.Sp); got != "140" {
		t.Errorf("numerator span covers %q, want %q", got, "140")
	}
	if got := f.Text(ratio.Denominator.Sp); got != "90" {
		t.Errorf("denominator span covers %q, want %q", got, "90")
	}

	// Sub-spans inside a glued word must be exact, which is what lets an
	// editor underline just the value.
	hr := st.Args[1].(*ast.Measurement)
	if got := f.Text(hr.Key.Sp); got != "hr" {
		t.Errorf("hr key span covers %q, want %q", got, "hr")
	}
	if got := f.Text(hr.Value.Span()); got != "98" {
		t.Errorf("hr value span covers %q, want %q", got, "98")
	}
}

// Every node's span must lie inside its parent's. A violated containment
// invariant breaks editor hit-testing and range-based quick fixes.
func TestSpanContainment(t *testing.T) {
	src := "@profile obgyn\npt 28F wt65kg ga:34w\nvitals bp120/75 fhr:142\n" +
		"sx nausea++ back-pain+3d\nlab hb11.2 pco2:38 hiv-\n" +
		"rx amoxicillin 500mg tds po x7d\ncc chest pain, worse on exertion\n"
	f := source.NewFile("t.cln", src)
	doc := Parse(f, NewStaticGrammar(CoreShapes, []string{"pco2", "hb"}), diag.NewBag())

	var check func(n ast.Node)
	check = func(n ast.Node) {
		parent := n.Span()
		ast.Walk(n, func(child ast.Node) bool {
			if child == n {
				return true
			}
			c := child.Span()
			if c.Start < parent.Start || c.End > parent.End {
				t.Errorf("%s span %+v escapes parent %s span %+v",
					child.Kind(), c, n.Kind(), parent)
			}
			check(child)
			return false
		})
	}
	for _, st := range doc.Statements {
		check(st)
	}
}

func TestDeterminism(t *testing.T) {
	src := "@profile obgyn\npt 28F wt65kg ga:34w\nvitals bp120/75 hr88\n" +
		"lab hb11.2 na138 k4.1 pco2:38\nrx amoxicillin 500mg tds po x7d\n"

	f := source.NewFile("t.cln", src)
	g := NewStaticGrammar(CoreShapes, []string{"pco2", "hb", "na", "k"})

	first := ast.DumpWithSpans(Parse(f, g, diag.NewBag()))
	for i := 0; i < 50; i++ {
		if got := ast.DumpWithSpans(Parse(f, g, diag.NewBag())); got != first {
			t.Fatalf("parse %d differed from the first", i)
		}
	}
}

func TestGrammarSplitKeyIsDeterministic(t *testing.T) {
	// Built from a map elsewhere in the engine; ordering must not leak in.
	keys := []string{"b12", "hb", "hba1c", "a1c", "pco2", "po2", "o2", "co2"}
	g := NewStaticGrammar(nil, keys)

	for i := 0; i < 100; i++ {
		if n := g.SplitKey("hba1c6.2"); n != 5 {
			t.Fatalf("SplitKey returned %d, want 5 (longest match hba1c)", n)
		}
		if n := g.SplitKey("pco245"); n != 4 {
			t.Fatalf("SplitKey returned %d, want 4 (pco2)", n)
		}
		if n := g.SplitKey("wbc12"); n != 0 {
			t.Fatalf("SplitKey returned %d, want 0 (no registered key)", n)
		}
	}
}

func TestUnknownCommandStillParses(t *testing.T) {
	// An unrecognised command yields structure, not a blob, so a profile that
	// is not loaded still produces a usable tree.
	doc, _ := parse(t, "partogram dil4cm eff80")
	st := doc.Statements[0]
	if st.Command.Raw != "partogram" {
		t.Fatalf("command = %q", st.Command.Raw)
	}
	if len(st.Args) != 2 {
		t.Errorf("expected 2 structured args, got %d:\n%s", len(st.Args), ast.Dump(doc))
	}
}

func TestNoDiagnosticsOnCleanRealisticNote(t *testing.T) {
	src := "@profile obgyn\n" +
		"# routine antenatal review\n" +
		"pt 28F wt65kg ht162cm ga:34w\n" +
		"cc reduced fetal movements since yesterday.\n" +
		"vitals bp118/72 hr84 temp36.8c spo298\n" +
		"sx nausea+ back-pain++ 2d\n" +
		"lab hb11.2 plt210 pco2:38\n" +
		"ix usg:normal cxr:wnl\n" +
		"rx amoxicillin 500mg tds po x7d\n" +
		"dx uncomplicated pregnancy\n"

	_, bag := parse(t, src, "pco2", "hb", "plt", "spo2", "bp", "hr", "temp")
	if bag.Len() != 0 {
		for _, d := range bag.Sorted() {
			t.Errorf("unexpected %s at %+v: %s", d.Code, d.Span, d.Message)
		}
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"", "pt 34M", "vitals bp140/90", "@profile obgyn", "ga:34w",
		"sx chestpain+++4h", "rx amoxicillin 500mg tds po x7d",
		"cc free text here", "lab hb13.5 pco2:45", "+++", "///", ":::",
		"@", "@@", "x7d", "1.2.3", `cc "unterminated`, "\n\n\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	g := NewStaticGrammar(CoreShapes, []string{"pco2", "hba1c", "b12", "hb"})

	f.Fuzz(func(t *testing.T, src string) {
		file := source.NewFile("fuzz.cln", src)
		doc := Parse(file, g, diag.NewBag())
		if doc == nil {
			t.Fatal("Parse returned nil")
		}

		// Every span must stay inside the file and be well formed. A bad span
		// reaching an editor is a crash in the editor, not just a bad message.
		ast.Walk(doc, func(n ast.Node) bool {
			s := n.Span()
			if s.Start < 0 || s.End < s.Start || s.End > len(src) {
				t.Fatalf("%s has invalid span %+v for input of length %d",
					n.Kind(), s, len(src))
			}
			return true
		})
	})
}

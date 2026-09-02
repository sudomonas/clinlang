// Package parser builds an AST from a ClinLang token stream.
//
// The parser answers syntactic questions only: where a statement begins, which
// tokens form one argument, whether a value is a number, a ratio or text. It
// does not decide that "tds" is a dosing frequency or that "hb" names an
// analyte. Those need the lexicon and the active profile, and they happen in
// the semantic layer, which can raise a positioned diagnostic when a lookup
// fails. The previous engine mixed the two, which is why its prescription
// parser had to linear-scan the whole drug list to find where a name ended.
//
// # Error recovery
//
// Parsing never fails and never returns a partial tree. An argument that cannot
// be interpreted becomes an ast.Error node with a diagnostic, and parsing
// continues with the next token in the same statement. A statement that cannot
// be started is skipped to the next newline. The result is always a complete
// Document, which is what makes live editing work: a half-typed line must still
// yield a tree that autocomplete and preview can use.
package parser

import (
	"strconv"
	"strings"

	"clinlang/pkg/ast"
	"clinlang/pkg/diag"
	"clinlang/pkg/lexer"
	"clinlang/pkg/source"
)

// Parse lexes and parses src into a Document.
//
// bag collects diagnostics and may be nil. g supplies command shapes and the
// key lexicon; a nil Grammar behaves as DefaultGrammar.
func Parse(f *source.File, g Grammar, bag *diag.Bag) *ast.Document {
	toks := lexer.Lex(f, bag)
	return ParseTokens(f, toks, g, bag)
}

// ParseTokens parses an already-lexed stream.
//
// Separating this from Parse lets the editor layer lex once and reuse the
// tokens for both parsing and syntax highlighting.
func ParseTokens(f *source.File, toks []lexer.Token, g Grammar, bag *diag.Bag) *ast.Document {
	if g == nil {
		g = DefaultGrammar()
	}
	p := &parser{file: f, toks: toks, gram: g, bag: bag}
	return p.parseDocument()
}

type parser struct {
	file *source.File
	toks []lexer.Token
	gram Grammar
	bag  *diag.Bag
	pos  int
}

// ─────────────────────────────────────────────────────────────────────────────
// Token cursor
// ─────────────────────────────────────────────────────────────────────────────

func (p *parser) cur() lexer.Token {
	if p.pos >= len(p.toks) {
		return lexer.Token{Kind: lexer.EOF, Span: source.Span{Start: p.file.Size(), End: p.file.Size()}}
	}
	return p.toks[p.pos]
}

func (p *parser) peek(n int) lexer.Token {
	i := p.pos + n
	if i >= len(p.toks) {
		return lexer.Token{Kind: lexer.EOF, Span: source.Span{Start: p.file.Size(), End: p.file.Size()}}
	}
	return p.toks[i]
}

func (p *parser) advance() lexer.Token {
	t := p.cur()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *parser) atLineEnd() bool {
	k := p.cur().Kind
	return k == lexer.Newline || k == lexer.EOF || k == lexer.Comment
}

// skipToLineEnd advances to the newline or EOF terminating the current line,
// leaving the terminator itself unconsumed.
func (p *parser) skipToLineEnd() {
	for !p.cur().EndsStatement() {
		p.advance()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Document
// ─────────────────────────────────────────────────────────────────────────────

func (p *parser) parseDocument() *ast.Document {
	doc := &ast.Document{Sp: source.Span{Start: 0, End: p.file.Size()}}

	for {
		switch t := p.cur(); t.Kind {
		case lexer.EOF:
			return doc

		case lexer.Newline:
			p.advance()

		case lexer.Comment:
			doc.Comments = append(doc.Comments, &ast.Comment{Raw: t.Text, Sp: t.Span})
			p.advance()

		case lexer.At:
			if pr := p.parsePragma(); pr != nil {
				doc.Pragmas = append(doc.Pragmas, pr)
			}

		case lexer.Word:
			doc.Statements = append(doc.Statements, p.parseStatement())

		default:
			// A line that does not begin with a command word. Report it, skip
			// the line, and carry on with the document.
			p.bag.Warn(diag.UnknownCommand, t.Span,
				"line does not begin with a command")
			p.skipToLineEnd()
		}
	}
}

// parsePragma parses an @-directive.
//
// Both "@profile obgyn" and the tighter "@obgyn" are accepted; the latter names
// the profile directly, which is how the existing documentation writes it.
func (p *parser) parsePragma() *ast.Pragma {
	at := p.advance() // '@'

	name := p.cur()
	if name.Kind != lexer.Word || name.SpaceBefore {
		p.bag.Warn(diag.MalformedPragma, at.Span, "@ must be followed by a directive name")
		p.skipToLineEnd()
		return nil
	}
	p.advance()

	// The short form stacks profiles with '+': "@obgyn+peds". Absorb the run
	// into the directive name so it reaches the semantic layer as one spec,
	// rather than leaving a stray qualifier the argument parser would report.
	nameText, nameSpan := name.Text, name.Span
	for p.cur().Kind == lexer.Qualifier && p.cur().Text == "+" && p.cur().Adjacent() &&
		p.peek(1).Kind == lexer.Word && p.peek(1).Adjacent() {
		plus := p.advance()
		word := p.advance()
		nameText += plus.Text + word.Text
		nameSpan = source.Merge(nameSpan, word.Span)
	}

	pr := &ast.Pragma{
		Name: &ast.Ident{Raw: nameText, Sp: nameSpan},
		Sp:   source.Merge(at.Span, nameSpan),
	}

	for !p.atLineEnd() {
		before := p.pos
		arg := p.parseArgument()
		if arg != nil {
			pr.Args = append(pr.Args, arg)
			pr.Sp = source.Merge(pr.Sp, arg.Span())
		}
		if p.pos == before {
			p.advance() // guarantee progress
		}
	}
	return pr
}

func (p *parser) parseStatement() *ast.Statement {
	cmd := p.advance() // command word

	st := &ast.Statement{
		Command: &ast.Ident{Raw: cmd.Text, Sp: cmd.Span},
		Sp:      cmd.Span,
	}

	shape := p.gram.Shape(cmd.Text)

	// A sectioned command falls back when the line carries no key at all:
	// "pe chest clear bilaterally" is a sentence, "pe cvs:rr rs:nvb" is not.
	if shape.sectioned() && !p.lineHasKey() {
		if shape == SectionedProse {
			shape = FreeText
		} else {
			shape = Structured
		}
	}

	if shape == FreeText {
		if ft := p.captureFreeText(); ft != nil {
			st.Trailing = ft
			st.Sp = source.Merge(st.Sp, ft.Sp)
		}
		return st
	}

	if shape.sectioned() {
		for !p.atLineEnd() {
			before := p.pos
			var arg ast.Argument
			if p.atKey() {
				arg = p.parseSection()
			} else {
				arg = p.parseArgument()
			}
			if arg != nil {
				st.Args = append(st.Args, arg)
				st.Sp = source.Merge(st.Sp, arg.Span())
			}
			if p.pos == before {
				p.advance()
			}
		}
		return st
	}

	for !p.atLineEnd() {
		before := p.pos
		arg := p.parseArgument()
		if arg != nil {
			st.Args = append(st.Args, arg)
			st.Sp = source.Merge(st.Sp, arg.Span())
		}
		if p.pos == before {
			p.advance() // guarantee progress even if a branch consumed nothing
		}
	}
	return st
}

// captureFreeText consumes the rest of the line as one verbatim node.
//
// The span runs from the first argument token to the last, so leading and
// trailing whitespace is excluded but everything between is preserved exactly,
// including punctuation and internal spacing. A trailing comment is not part of
// the text.
func (p *parser) captureFreeText() *ast.FreeText {
	if p.atLineEnd() {
		return nil
	}
	start := p.cur().Span.Start
	end := start
	for !p.atLineEnd() {
		end = p.cur().Span.End
		p.advance()
	}
	sp := source.Span{Start: start, End: end}
	return &ast.FreeText{Raw: p.file.Text(sp), Sp: sp}
}

// ─────────────────────────────────────────────────────────────────────────────
// Sections
// ─────────────────────────────────────────────────────────────────────────────

// atKey reports whether the cursor sits on a "word:" key marker.
func (p *parser) atKey() bool {
	return p.cur().Kind == lexer.Word &&
		p.peek(1).Kind == lexer.Colon && p.peek(1).Adjacent()
}

// keyAt reports whether a key marker begins at the given absolute token index.
func (p *parser) keyAt(i int) bool {
	if i < 0 || i+1 >= len(p.toks) {
		return false
	}
	return p.toks[i].Kind == lexer.Word &&
		p.toks[i+1].Kind == lexer.Colon && p.toks[i+1].Adjacent()
}

// lineHasKey reports whether the remainder of the current line contains a key.
func (p *parser) lineHasKey() bool {
	for i := p.pos; i < len(p.toks); i++ {
		if p.toks[i].EndsStatement() {
			return false
		}
		if p.keyAt(i) {
			return true
		}
	}
	return false
}

// parseSection reads "key: value" where the value runs to the next key or the
// end of the line.
//
// A single-token value is parsed as a value, so "ga:34w" is still a quantity
// and "widal:1/60" is still a ratio. Only a multi-word value becomes text,
// which is what removes the need to glue findings together with underscores.
func (p *parser) parseSection() ast.Argument {
	keyTok := p.advance()
	colon := p.advance()

	key := &ast.Ident{Raw: keyTok.Text, Sp: keyTok.Span}
	m := &ast.Measurement{
		Key:      key,
		Explicit: true,
		Sp:       source.Merge(keyTok.Span, colon.Span),
	}

	// Find where this section ends: the next key, or the end of the line.
	end := p.pos
	for end < len(p.toks) && !p.toks[end].EndsStatement() &&
		p.toks[end].Kind != lexer.Comment && !p.keyAt(end) {
		end++
	}

	if end == p.pos {
		return m // "cxr:" with nothing after it is a flag
	}

	// A value written as one unbroken run keeps the ordinary value grammar, so
	// "ga:34w" is still a quantity and "widal:1/60" still a ratio. A run broken
	// by a space is prose. Glued means one value, spaced means words — the same
	// distinction the rest of the language already runs on.
	if p.gluedThrough(end) {
		val, unit := p.parseValue()
		m.Value = val
		m.Unit = unit
		if val != nil {
			m.Sp = source.Merge(m.Sp, val.Span())
		}
		if unit != nil {
			m.Sp = source.Merge(m.Sp, unit.Sp)
		}
		for p.pos < end {
			p.advance() // anything the value grammar declined to take
		}
		return m
	}

	sp := source.Span{Start: p.toks[p.pos].Span.Start, End: p.toks[end-1].Span.End}
	for p.pos < end {
		p.advance()
	}
	m.Value = &ast.Text{Raw: p.file.Text(sp), Sp: sp}
	m.Sp = source.Merge(m.Sp, sp)
	return m
}

// gluedThrough reports whether every token from the cursor to end forms one
// unbroken run with no whitespace inside it.
//
// "34w" and "1/60" are glued and are single values; "patchy consolidation" is
// not and is prose.
func (p *parser) gluedThrough(end int) bool {
	for i := p.pos + 1; i < end; i++ {
		if p.toks[i].SpaceBefore {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// Arguments
// ─────────────────────────────────────────────────────────────────────────────

func (p *parser) parseArgument() ast.Argument {
	switch t := p.cur(); t.Kind {
	case lexer.Word:
		return p.parseWordArgument()

	case lexer.Number, lexer.Date:
		return p.parseQuantityArgument()

	case lexer.String:
		p.advance()
		inner := strings.TrimSuffix(strings.TrimPrefix(t.Text, `"`), `"`)
		return &ast.ConceptRef{
			Name: &ast.Ident{Raw: inner, Sp: t.Span},
			Sp:   t.Span,
		}

	case lexer.Qualifier:
		p.advance()
		p.bag.Warn(diag.TrailingQualifier, t.Span,
			"intensity marker has nothing to qualify")
		return &ast.Error{Raw: t.Text, Sp: t.Span}

	case lexer.Punct, lexer.Comma:
		// Inert punctuation inside a structured statement. Skipping it silently
		// keeps a stray comma in a vitals line from generating noise.
		p.advance()
		return nil

	case lexer.Invalid:
		p.advance()
		return &ast.Error{Raw: t.Text, Sp: t.Span}

	default:
		p.advance()
		p.bag.Warn(diag.UnexpectedToken, t.Span,
			"unexpected "+strings.ToLower(t.Kind.String())+" here")
		return &ast.Error{Raw: t.Text, Sp: t.Span}
	}
}

// parseWordArgument handles every argument that begins with a word.
//
// A word may be a bare concept (amoxicillin), a concept with grade and duration
// (chestpain+++4h), a glued measurement (hb13.5, wt65kg, bp140/90), or an
// explicitly delimited one (ga:34w, cxr:wnl).
func (p *parser) parseWordArgument() ast.Argument {
	tok := p.advance()

	// An adjacent colon overrides the splitting heuristic entirely: the
	// clinician has said where the key ends, so the whole word is the key.
	// This is what makes "pco2:45" work without registering pco2 in the
	// lexicon, and it is why the explicit form is the documented escape hatch
	// for any analyte whose name ends in digits.
	if p.cur().Kind == lexer.Colon && p.cur().Adjacent() {
		return p.parseExplicitMeasurement(&ast.Ident{Raw: tok.Text, Sp: tok.Span})
	}

	key, rest, restStart := p.splitWord(tok)

	// The x-prefixed duration is language syntax, not clinical vocabulary:
	// "x7d" means "for seven days" the same way in every command.
	if strings.EqualFold(key.Raw, "x") && rest != "" {
		if d := p.durationFromText(rest, restStart, true, tok.Span); d != nil {
			return d
		}
	}

	if rest != "" {
		return p.parseGluedMeasurement(key, rest, restStart, tok.Span)
	}

	// Bare word: a concept, optionally graded and given a duration.
	return p.parseConceptRef(key)
}

// parseExplicitMeasurement handles key:value.
func (p *parser) parseExplicitMeasurement(key *ast.Ident) ast.Argument {
	colon := p.advance()

	m := &ast.Measurement{
		Key:      key,
		Explicit: true,
		Sp:       source.Merge(key.Sp, colon.Span),
	}

	// "cxr:" with nothing after it is a flag, not an error: the clinician may
	// still be typing, and the statement should stay usable.
	if p.atLineEnd() || p.cur().SpaceBefore {
		return m
	}

	val, unit := p.parseValue()
	m.Value = val
	m.Unit = unit
	if val != nil {
		m.Sp = source.Merge(m.Sp, val.Span())
	}
	if unit != nil {
		m.Sp = source.Merge(m.Sp, unit.Sp)
	}
	if q := p.takeAdjacentQualifier(); q != nil {
		m.Qual = q
		m.Sp = source.Merge(m.Sp, q.Sp)
	}
	return m
}

// parseGluedMeasurement handles a word whose tail is the value: hb13.5, wt65kg,
// bp140 followed by /90.
func (p *parser) parseGluedMeasurement(key *ast.Ident, rest string, restStart int, whole source.Span) ast.Argument {
	num, unit := p.splitNumberUnit(rest, restStart)

	m := &ast.Measurement{Key: key, Sp: whole}

	switch {
	case num == nil:
		// The tail was not numeric, as in a lexicon key followed by letters.
		m.Value = &ast.Text{
			Raw: rest,
			Sp:  source.Span{Start: restStart, End: restStart + len(rest)},
		}

	case p.cur().Kind == lexer.Slash && p.cur().Adjacent():
		// A ratio: bp140/90. Structured here, once, rather than stored as a
		// string and re-parsed by a later layer.
		m.Value = p.parseRatioTail(num)
		m.Sp = source.Merge(m.Sp, m.Value.Span())

	default:
		m.Value = num
		m.Unit = unit
	}

	if q := p.takeAdjacentQualifier(); q != nil {
		m.Qual = q
		m.Sp = source.Merge(m.Sp, q.Sp)
	}
	return m
}

// parseRatioTail consumes "/denominator" after an already-parsed numerator.
func (p *parser) parseRatioTail(num *ast.Number) *ast.Ratio {
	slash := p.advance()
	r := &ast.Ratio{Numerator: num, Sp: source.Merge(num.Sp, slash.Span)}

	t := p.cur()
	if t.Kind != lexer.Number || !t.Adjacent() {
		p.bag.Warn(diag.MalformedRatio, r.Sp, "ratio has no value after the slash")
		return r
	}
	p.advance()
	r.Denominator = p.numberFrom(t)
	r.Sp = source.Merge(r.Sp, t.Span)
	return r
}

// parseConceptRef handles a bare name with optional grade and duration.
func (p *parser) parseConceptRef(name *ast.Ident) ast.Argument {
	c := &ast.ConceptRef{Name: name, Sp: name.Sp}

	if q := p.takeAdjacentQualifier(); q != nil {
		c.Grade = q
		c.Sp = source.Merge(c.Sp, q.Sp)
	}

	// A duration may follow the grade with no space: chestpain+++4h.
	if t := p.cur(); t.Kind == lexer.Number && t.Adjacent() {
		save := p.pos
		p.advance()
		num := p.numberFrom(t)
		if u := p.takeAdjacentUnit(); u != nil {
			c.Duration = &ast.Duration{Number: num, Unit: u, Sp: source.Merge(num.Sp, u.Sp)}
			c.Sp = source.Merge(c.Sp, c.Duration.Sp)
		} else {
			// A number with no unit is not a duration; leave it for the next
			// argument rather than guessing.
			p.pos = save
		}
	}
	return c
}

// parseQuantityArgument handles an argument that begins with a number: 500mg,
// 65, or an ISO date.
func (p *parser) parseQuantityArgument() ast.Argument {
	t := p.advance()

	if t.Kind == lexer.Date {
		return &ast.ConceptRef{
			Name: &ast.Ident{Raw: t.Text, Sp: t.Span},
			Sp:   t.Span,
		}
	}

	num := p.numberFrom(t)

	// A ratio can also be written with the numerator standing alone: 140/90.
	if p.cur().Kind == lexer.Slash && p.cur().Adjacent() {
		ratio := p.parseRatioTail(num)
		return &ast.Measurement{Value: ratio, Sp: ratio.Sp}
	}

	q := &ast.Quantity{Number: num, Sp: num.Sp}
	if u := p.takeAdjacentUnit(); u != nil {
		q.Unit = u
		q.Sp = source.Merge(q.Sp, u.Sp)
	}
	return q
}

// parseValue reads the value part after an explicit colon.
func (p *parser) parseValue() (ast.Value, *ast.Unit) {
	switch t := p.cur(); t.Kind {
	case lexer.Number:
		p.advance()
		num := p.numberFrom(t)
		if p.cur().Kind == lexer.Slash && p.cur().Adjacent() {
			return p.parseRatioTail(num), nil
		}
		return num, p.takeAdjacentUnit()

	case lexer.Word:
		p.advance()
		// A word after a colon is a value, not a new key: cxr:wnl.
		return &ast.Text{Raw: t.Text, Sp: t.Span}, nil

	case lexer.String:
		p.advance()
		inner := strings.TrimSuffix(strings.TrimPrefix(t.Text, `"`), `"`)
		return &ast.Text{Raw: inner, Quoted: true, Sp: t.Span}, nil

	case lexer.Date:
		p.advance()
		return &ast.Text{Raw: t.Text, Sp: t.Span}, nil

	default:
		return nil, nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Word splitting
// ─────────────────────────────────────────────────────────────────────────────

// splitWord divides a word token into a key identifier and a value remainder.
//
// Resolution order:
//
//  1. The lexicon: the longest registered key that prefixes the word wins, so
//     "pco2" and "hba1c6.2" split correctly.
//  2. Otherwise, the first digit, which is the documented default and handles
//     "hb13.5" and "wt65kg".
//  3. Otherwise the whole word is the key and there is no value.
//
// restStart is the byte offset of the remainder, so sub-spans stay exact and a
// diagnostic can point at the value rather than the whole token.
func (p *parser) splitWord(tok lexer.Token) (key *ast.Ident, rest string, restStart int) {
	text := tok.Text
	base := tok.Span.Start

	if n := p.gram.SplitKey(text); n > 0 && n <= len(text) {
		return &ast.Ident{Raw: text[:n], Sp: source.Span{Start: base, End: base + n}},
			text[n:], base + n
	}

	for i := 0; i < len(text); i++ {
		if text[i] >= '0' && text[i] <= '9' {
			if i == 0 {
				break // a word never starts with a digit
			}
			return &ast.Ident{Raw: text[:i], Sp: source.Span{Start: base, End: base + i}},
				text[i:], base + i
		}
	}
	return &ast.Ident{Raw: text, Sp: tok.Span}, "", tok.Span.End
}

// splitNumberUnit divides a value remainder into its numeric and unit parts.
//
// "13.5" yields a number and no unit; "65kg" yields both; "wnl" yields neither.
func (p *parser) splitNumberUnit(rest string, start int) (*ast.Number, *ast.Unit) {
	i := 0
	seenDot := false
	for i < len(rest) {
		c := rest[i]
		if c >= '0' && c <= '9' {
			i++
			continue
		}
		if c == '.' && !seenDot && i+1 < len(rest) && rest[i+1] >= '0' && rest[i+1] <= '9' {
			seenDot = true
			i++
			continue
		}
		break
	}
	if i == 0 {
		return nil, nil
	}

	numText := rest[:i]
	numSpan := source.Span{Start: start, End: start + i}
	val, err := strconv.ParseFloat(numText, 64)
	if err != nil {
		p.bag.Warn(diag.MalformedNumber, numSpan, "could not read "+numText+" as a number")
	}
	num := &ast.Number{Raw: numText, Val: val, Sp: numSpan}

	if i == len(rest) {
		return num, nil
	}
	unitText := rest[i:]
	return num, &ast.Unit{
		Raw: unitText,
		Sp:  source.Span{Start: start + i, End: start + i + len(unitText)},
	}
}

// durationFromText builds a Duration from an x-prefixed remainder such as "7d".
// It returns nil when the remainder is not a number followed by a unit.
func (p *parser) durationFromText(rest string, start int, prefixed bool, whole source.Span) *ast.Duration {
	num, unit := p.splitNumberUnit(rest, start)
	if num == nil || unit == nil {
		return nil
	}
	return &ast.Duration{Number: num, Unit: unit, Prefixed: prefixed, Sp: whole}
}

// ─────────────────────────────────────────────────────────────────────────────
// Small helpers
// ─────────────────────────────────────────────────────────────────────────────

func (p *parser) numberFrom(t lexer.Token) *ast.Number {
	val, err := strconv.ParseFloat(t.Text, 64)
	if err != nil {
		p.bag.Warn(diag.MalformedNumber, t.Span, "could not read "+t.Text+" as a number")
	}
	return &ast.Number{Raw: t.Text, Val: val, Sp: t.Span}
}

// takeAdjacentUnit consumes a word directly attached to the preceding number.
//
// "Unit" here means the trailing word glued to a number, not a validated unit
// of measure. Whether "kg" is a mass, "w" a duration or "F" a demographic
// marker is decided by the semantic layer, which has the tables and the
// statement context to tell them apart.
func (p *parser) takeAdjacentUnit() *ast.Unit {
	t := p.cur()
	if !t.Adjacent() || (t.Kind != lexer.Word && t.Kind != lexer.Percent) {
		return nil
	}
	p.advance()

	unit := &ast.Unit{Raw: t.Text, Sp: t.Span}

	// Rate and per-unit doses are written as one unit: 100ml/hr, 5mg/kg,
	// 30mg/day. The slash here is part of the unit, not a ratio, because a
	// ratio's sides are numbers and these are words.
	if p.cur().Kind == lexer.Slash && p.cur().Adjacent() &&
		p.peek(1).Kind == lexer.Word && p.peek(1).Adjacent() {
		slash := p.advance()
		denom := p.advance()
		unit.Raw += slash.Text + denom.Text
		unit.Sp = source.Merge(unit.Sp, denom.Span)
	}
	return unit
}

// takeAdjacentQualifier consumes a +/- run directly attached to what precedes.
//
// Adjacency and position both matter: the run must trail the name, which is
// what stops "non-productive-cough" from being read as name "non" with
// intensity "improving" the way the previous symptom parser did.
func (p *parser) takeAdjacentQualifier() *ast.Qualifier {
	t := p.cur()
	if t.Kind != lexer.Qualifier || !t.Adjacent() {
		return nil
	}
	p.advance()
	return &ast.Qualifier{Raw: t.Text, Sp: t.Span}
}

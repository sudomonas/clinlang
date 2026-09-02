// Package lexer turns ClinLang source text into a token stream.
//
// This is the single lexer. The previous engine re-derived the same "identifier
// glued to a value" shape in five places — extension_parser.splitKeyValue,
// investigations_parser.parseInvestigationToken, symptom_parser.parseSymptomToken,
// abbreviations.ParseDurationToken and the prescription classification cascade —
// each with a different answer, which is why "hb13.5" produced three different
// results depending on which command it appeared under. There is one answer here.
//
// # What the lexer does and does not decide
//
// The lexer knows shapes: letters form words, digits form numbers, and certain
// characters separate them. It has no unit table, no analyte list and no drug
// list, so it cannot acquire the hardcoded escape lists that the old
// parseInvestigationToken needed. Every clinical judgement — is "mg" a unit, is
// "pco2" one identifier or an identifier plus a number, does "tds" mean three
// times daily — belongs to later stages that hold the lexicon.
//
// One consequence is worth stating plainly, because the architecture review
// overstated it: the escape list does not disappear. "pco2" and "hb13" are
// lexically identical, both a letter run followed by a digit run, and no
// context-free rule tells them apart. The lexer therefore emits the whole run
// as a single Word and leaves the split to the parser, which consults a
// user-editable lexicon. Adding an analyte becomes a data change rather than a
// code change, which is the real improvement.
//
// # Losslessness
//
// Concatenating every token's source text, including comments and whitespace
// recoverable from spans, reproduces the input exactly. That property is what
// lets the AST support formatting-preserving rewrites and a future `clinlang
// fmt`, and it is asserted by a round-trip test.
package lexer

import (
	"unicode"
	"unicode/utf8"

	"clinlang/pkg/diag"
	"clinlang/pkg/source"
)

// Lex tokenizes the whole file and returns the token stream.
//
// The stream always ends with exactly one EOF token. Lexing never fails:
// unusable bytes become Invalid tokens with diagnostics recorded in bag, so a
// malformed paste still yields a usable document. bag may be nil.
func Lex(f *source.File, bag *diag.Bag) []Token {
	s := &scanner{
		src:  f.Content(),
		file: f,
		bag:  bag,
	}
	return s.run()
}

type scanner struct {
	src  string
	file *source.File
	bag  *diag.Bag

	pos          int // byte offset of the next rune to read
	pendingSpace bool
}

func (s *scanner) run() []Token {
	// Most clinical notes average a handful of tokens per line; this keeps the
	// common case to a single allocation.
	out := make([]Token, 0, len(s.src)/4+8)

	for {
		tok, ok := s.next()
		if !ok {
			continue // whitespace produced no token
		}
		out = append(out, tok)
		if tok.Kind == EOF {
			return out
		}
	}
}

// next scans one token. It returns ok=false when it consumed whitespace and
// produced nothing, which the caller loops past.
func (s *scanner) next() (Token, bool) {
	if s.pos >= len(s.src) {
		return s.make(EOF, s.pos, s.pos), true
	}

	start := s.pos
	r, w := utf8.DecodeRuneInString(s.src[s.pos:])

	switch {
	case r == '\n':
		s.pos += w
		tok := s.make(Newline, start, s.pos)
		// The first token of the next line is at a line start, which counts as
		// preceded by whitespace. Without this, the leading command word would
		// report itself adjacent to the newline.
		s.pendingSpace = true
		return tok, true

	case r == ' ' || r == '\t' || r == '\r' || r == '\v' || r == '\f':
		s.pos += w
		s.pendingSpace = true
		return Token{}, false

	case unicode.IsSpace(r):
		// Non-breaking spaces and similar arrive from pasted documents.
		s.pos += w
		s.pendingSpace = true
		return Token{}, false

	case r == '#':
		return s.scanLineComment(start), true

	case r == '/' && s.peekAt(s.pos+w) == '/':
		return s.scanLineComment(start), true

	case r == '"':
		return s.scanString(start), true

	case isWordStart(r):
		return s.scanWord(start), true

	case unicode.IsDigit(r):
		return s.scanNumberOrDate(start), true

	case r == '/':
		s.pos += w
		return s.make(Slash, start, s.pos), true

	case r == ':':
		s.pos += w
		return s.make(Colon, start, s.pos), true

	case r == ',':
		s.pos += w
		return s.make(Comma, start, s.pos), true

	case r == '@':
		s.pos += w
		return s.make(At, start, s.pos), true

	case r == '%':
		s.pos += w
		return s.make(Percent, start, s.pos), true

	case r == '+' || r == '-':
		return s.scanQualifier(start, r), true

	case unicode.IsControl(r):
		s.pos += w
		tok := s.make(Invalid, start, s.pos)
		s.bag.Warn(diag.InvalidCharacter, tok.Span,
			"control character in source text")
		return tok, true

	default:
		// Printable but grammatically inert: prose punctuation, brackets,
		// currency signs, dashes pasted from a word processor.
		s.pos += w
		return s.make(Punct, start, s.pos), true
	}
}

// scanLineComment consumes to end of line, excluding the newline itself.
func (s *scanner) scanLineComment(start int) Token {
	for s.pos < len(s.src) && s.src[s.pos] != '\n' {
		s.pos++
	}
	// A CRLF line ending leaves the \r inside the comment text; trim it so the
	// comment's text matches what the user sees.
	end := s.pos
	if end > start && s.src[end-1] == '\r' {
		end--
	}
	tok := s.make(Comment, start, end)
	return tok
}

// scanString consumes a double-quoted run.
//
// Strings do not span lines: an unterminated quote is far more likely to be a
// typo than a deliberate multi-line literal, and stopping at the newline keeps
// one bad quote from swallowing the rest of the note.
func (s *scanner) scanString(start int) Token {
	s.pos++ // opening quote

	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if c == '"' {
			s.pos++
			return s.make(String, start, s.pos)
		}
		if c == '\n' {
			break
		}
		s.pos++
	}

	tok := s.make(String, start, s.pos)
	s.bag.Warn(diag.UnterminatedString, tok.Span, "quoted text has no closing quote")
	return tok
}

// scanWord consumes a word run.
//
// Continuation rules, and why each exists:
//
//	letters, digits, underscore   always continue: pco2, hba1c, spo2
//	'.' before a digit            continues: hb13.5 stays one word, while the
//	                              full stop ending a sentence does not
//	'-' before a letter           continues: non-productive-cough, x-ray, while
//	                              "pain--3d" stops so the -- reads as intensity
//
// A hyphen before a digit deliberately does not continue, which is what makes
// "pain-3d" parse as pain / improving / 3d. Analyte names that genuinely
// contain one, such as ca19-9 or apo-b100, are lexicon entries — the same
// mechanism that resolves pco2, rather than a second special case.
func (s *scanner) scanWord(start int) Token {
	for s.pos < len(s.src) {
		r, w := utf8.DecodeRuneInString(s.src[s.pos:])

		switch {
		case isWordContinue(r):
			s.pos += w

		case r == '.' && unicode.IsDigit(s.peekAt(s.pos+w)):
			s.pos += w

		case r == '-' && unicode.IsLetter(s.peekAt(s.pos+w)):
			s.pos += w

		default:
			return s.make(Word, start, s.pos)
		}
	}
	return s.make(Word, start, s.pos)
}

// scanNumberOrDate consumes a numeric literal, preferring an ISO date.
func (s *scanner) scanNumberOrDate(start int) Token {
	if end, ok := s.matchISODate(start); ok {
		s.pos = end
		return s.make(Date, start, end)
	}
	return s.scanNumber(start)
}

// matchISODate reports whether an unambiguous ISO timestamp begins at start.
//
// Accepts YYYY-MM-DD and YYYY-MM-DDTHH:MM, with optional seconds.
//
// The time part must lex as part of the same token. If "14:20" were separate
// Number, Colon and Number tokens, a statement containing a timestamp would
// look to the parser exactly like one containing a "key:value" section, and an
// encounter line would be misread as a keyed argument.
//
// Only the ISO form is recognised. Slash-separated dates are not: "12/25" is
// indistinguishable from a ratio, and in a shorthand where "bp140/90" is
// everyday input, reading slashes as dates would be the wrong default.
func (s *scanner) matchISODate(start int) (end int, ok bool) {
	need := func(off, n int) bool {
		if off+n > len(s.src) {
			return false
		}
		for i := off; i < off+n; i++ {
			if s.src[i] < '0' || s.src[i] > '9' {
				return false
			}
		}
		return true
	}
	at := func(off int, c byte) bool { return off < len(s.src) && s.src[off] == c }

	if !need(start, 4) || !at(start+4, '-') || !need(start+5, 2) ||
		!at(start+7, '-') || !need(start+8, 2) {
		return 0, false
	}
	end = start + 10

	// Optional time: T followed by HH:MM, and optionally :SS.
	if at(end, 'T') && need(end+1, 2) && at(end+3, ':') && need(end+4, 2) {
		end += 6
		if at(end, ':') && need(end+1, 2) {
			end += 3
		}
	}

	// Maximal munch: 2025-06-155 is not a date, it is something else entirely.
	if end < len(s.src) {
		r, _ := utf8.DecodeRuneInString(s.src[end:])
		if unicode.IsDigit(r) || isWordStart(r) {
			return 0, false
		}
	}
	return end, true
}

// scanNumber consumes digits with at most one decimal point.
//
// A second point terminates the number rather than being absorbed, so "1.2.3"
// yields Number(1.2), Punct(.), Number(3) instead of a silently wrong value.
func (s *scanner) scanNumber(start int) Token {
	seenDot := false
	for s.pos < len(s.src) {
		r, w := utf8.DecodeRuneInString(s.src[s.pos:])

		if unicode.IsDigit(r) {
			s.pos += w
			continue
		}
		if r == '.' && !seenDot && unicode.IsDigit(s.peekAt(s.pos+w)) {
			seenDot = true
			s.pos += w
			continue
		}
		break
	}
	return s.make(Number, start, s.pos)
}

// scanQualifier consumes a run of one repeated sign.
//
// Runs never mix: "+-" is two tokens, because a mixed run has no meaning and
// silently collapsing it would hide a typo.
func (s *scanner) scanQualifier(start int, sign rune) Token {
	for s.pos < len(s.src) {
		r, w := utf8.DecodeRuneInString(s.src[s.pos:])
		if r != sign {
			break
		}
		s.pos += w
	}
	return s.make(Qualifier, start, s.pos)
}

// make builds a token spanning [start, end) and consumes the pending-space flag.
func (s *scanner) make(kind Kind, start, end int) Token {
	tok := Token{
		Kind:        kind,
		Text:        s.src[start:end],
		Span:        source.Span{Start: start, End: end},
		SpaceBefore: s.pendingSpace || start == 0,
	}
	s.pendingSpace = false
	return tok
}

// peekAt returns the rune at offset, or utf8.RuneError past the end.
func (s *scanner) peekAt(offset int) rune {
	if offset >= len(s.src) {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRuneInString(s.src[offset:])
	return r
}

// isWordStart reports whether r can begin a word.
//
// unicode.IsLetter rather than an ASCII range: clinical text carries µ, Ω,
// Löfgren and Ménière, and the previous engine's hand-rolled ASCII-only
// helpers mangled all of them.
func isWordStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isWordContinue(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

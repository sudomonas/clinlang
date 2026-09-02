// Package source provides the byte-offset model that every later stage of the
// ClinLang pipeline reports positions in.
//
// A single File owns the input text and a precomputed line index. Every token,
// AST node, IR node and diagnostic carries a Span into that File, so a value
// rendered in a SOAP note can always be traced back to the exact characters the
// clinician typed. Nothing in the pipeline is allowed to invent a position or
// format a line number into a message string.
//
// Offsets are byte offsets into the file content. Human- and editor-facing
// coordinates are derived on demand via File.Position, which is the only place
// that knows about lines, columns, and UTF-16.
package source

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Span is a half-open byte range [Start, End) into a File's content.
//
// The zero Span is deliberately not a valid position: it is used by nodes that
// were synthesised rather than parsed (for example a derived BMI value), and
// IsValid reports false for it so callers can decline to render a location.
type Span struct {
	Start int
	End   int
}

// NoSpan is the explicit "this did not come from source text" span.
var NoSpan = Span{}

// IsValid reports whether s refers to a real, non-negative range.
//
// A zero-length span at a real offset (End == Start > 0) is valid: it marks an
// insertion point, which is what a "missing token" diagnostic wants to point at.
func (s Span) IsValid() bool {
	return s.End >= s.Start && (s.Start > 0 || s.End > 0)
}

// Len returns the number of bytes covered by s.
func (s Span) Len() int {
	if s.End < s.Start {
		return 0
	}
	return s.End - s.Start
}

// IsEmpty reports whether s covers no bytes.
func (s Span) IsEmpty() bool { return s.Len() == 0 }

// Contains reports whether offset falls within s. The end offset is excluded,
// except for empty spans, where the single insertion point is considered
// contained so that editor hit-testing finds it.
func (s Span) Contains(offset int) bool {
	if s.IsEmpty() {
		return offset == s.Start
	}
	return offset >= s.Start && offset < s.End
}

// Overlaps reports whether s and other share at least one byte.
func (s Span) Overlaps(other Span) bool {
	return s.Start < other.End && other.Start < s.End
}

// Merge returns the smallest span covering both a and b.
//
// Invalid spans are absorbed rather than propagated: merging a real span with
// NoSpan yields the real span. This lets a parent node compute its extent by
// folding over children without special-casing synthesised ones.
func Merge(a, b Span) Span {
	if !a.IsValid() {
		return b
	}
	if !b.IsValid() {
		return a
	}
	out := a
	if b.Start < out.Start {
		out.Start = b.Start
	}
	if b.End > out.End {
		out.End = b.End
	}
	return out
}

// MergeAll returns the smallest span covering every valid span in spans.
func MergeAll(spans ...Span) Span {
	var out Span
	for _, s := range spans {
		out = Merge(out, s)
	}
	return out
}

// Position is a resolved, human- and editor-facing location.
//
// Line and Col are 1-based because that is what clinicians and CLI output
// expect. UTF16Col is 0-based because that is what the Language Server Protocol
// requires; keeping both means the LSP layer never has to recompute it.
type Position struct {
	Offset   int // byte offset into the file
	Line     int // 1-based line number
	Col      int // 1-based column, counted in runes
	UTF16Col int // 0-based column, counted in UTF-16 code units (LSP)
}

// Range is a resolved Span, carrying both endpoints.
type Range struct {
	Start Position
	End   Position
}

// File is an immutable source document plus its line index.
//
// Construct one with NewFile; the line index is computed once at construction,
// so Position is a binary search rather than a scan. Files are safe for
// concurrent reads.
type File struct {
	name    string
	content string

	// lineStarts[i] is the byte offset of the first character of line i+1.
	// It always begins with 0, so len(lineStarts) is the line count.
	lineStarts []int
}

// NewFile indexes content and returns a File.
//
// name is used only for display in diagnostics; it may be empty for anonymous
// input such as an HTTP request body.
func NewFile(name, content string) *File {
	f := &File{
		name:       name,
		content:    content,
		lineStarts: make([]int, 0, strings.Count(content, "\n")+1),
	}
	f.lineStarts = append(f.lineStarts, 0)
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			f.lineStarts = append(f.lineStarts, i+1)
		}
	}
	return f
}

// Name returns the display name of the file.
func (f *File) Name() string { return f.name }

// Content returns the full source text.
func (f *File) Content() string { return f.content }

// Size returns the length of the source text in bytes.
func (f *File) Size() int { return len(f.content) }

// LineCount returns the number of lines in the file.
//
// A file with no trailing newline still counts its final partial line; an empty
// file has one (empty) line.
func (f *File) LineCount() int { return len(f.lineStarts) }

// Text returns the source text covered by s.
//
// Out-of-range spans are clamped rather than panicking: a malformed span from a
// buggy pass should degrade to a short or empty string, not crash a server
// handling a parse request.
func (f *File) Text(s Span) string {
	start, end := f.clamp(s)
	return f.content[start:end]
}

func (f *File) clamp(s Span) (int, int) {
	start, end := s.Start, s.End
	if start < 0 {
		start = 0
	}
	if start > len(f.content) {
		start = len(f.content)
	}
	if end < start {
		end = start
	}
	if end > len(f.content) {
		end = len(f.content)
	}
	return start, end
}

// Position resolves a byte offset to a line/column Position.
//
// Offsets outside the file are clamped to its bounds.
func (f *File) Position(offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(f.content) {
		offset = len(f.content)
	}

	// Largest i such that lineStarts[i] <= offset.
	line := sort.Search(len(f.lineStarts), func(i int) bool {
		return f.lineStarts[i] > offset
	}) - 1
	line = max(line, 0)

	prefix := f.content[f.lineStarts[line]:offset]
	return Position{
		Offset:   offset,
		Line:     line + 1,
		Col:      utf8.RuneCountInString(prefix) + 1,
		UTF16Col: utf16Len(prefix),
	}
}

// Range resolves both endpoints of s.
func (f *File) Range(s Span) Range {
	start, end := f.clamp(s)
	return Range{
		Start: f.Position(start),
		End:   f.Position(end),
	}
}

// LineSpan returns the span of the given 1-based line, excluding its newline.
//
// An out-of-range line number yields NoSpan.
func (f *File) LineSpan(line int) Span {
	if line < 1 || line > len(f.lineStarts) {
		return NoSpan
	}
	start := f.lineStarts[line-1]
	end := len(f.content)
	if line < len(f.lineStarts) {
		// Back off the newline that begins the next line.
		end = f.lineStarts[line] - 1
		// Also back off a preceding carriage return, so callers rendering a
		// caret under a CRLF line do not count the \r as a column.
		if end > start && f.content[end-1] == '\r' {
			end--
		}
	}
	return Span{Start: start, End: max(end, start)}
}

// LineText returns the text of the given 1-based line, without its line ending.
func (f *File) LineText(line int) string {
	return f.Text(f.LineSpan(line))
}

// Offset converts a 1-based line and rune column back to a byte offset.
//
// It is the inverse of Position and exists for the LSP layer, which receives
// edit positions from the editor in line/column form.
func (f *File) Offset(line, col int) int {
	if line < 1 {
		return 0
	}
	if line > len(f.lineStarts) {
		return len(f.content)
	}
	start := f.lineStarts[line-1]
	lineEnd := f.LineSpan(line).End

	off := start
	for i := 1; i < col && off < lineEnd; i++ {
		_, size := utf8.DecodeRuneInString(f.content[off:])
		if size == 0 {
			break
		}
		off += size
	}
	return off
}

// utf16Len returns the number of UTF-16 code units needed to encode s.
//
// Characters outside the Basic Multilingual Plane count as two. Clinical text
// is mostly ASCII, but micro signs, degree signs and accented names are common
// enough that assuming one unit per rune would misplace editor squiggles.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

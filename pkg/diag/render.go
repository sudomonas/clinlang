package diag

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"clinlang/pkg/source"
)

// RenderOptions controls text rendering of diagnostics.
type RenderOptions struct {
	// ShowSource includes the offending line with a caret underline beneath
	// the span. Off by default so that machine-readable output stays compact.
	ShowSource bool

	// TabWidth is how many columns a tab occupies when aligning the caret.
	// Zero means 4.
	TabWidth int
}

// Render formats a single diagnostic as text.
//
// The one-line form is `file:line:col: severity: message [CODE]`, which is the
// conventional compiler layout that editors and CI log scrapers already parse.
func Render(f *source.File, d Diagnostic, opts RenderOptions) string {
	var sb strings.Builder
	writeHeader(&sb, f, d)

	if opts.ShowSource && f != nil && d.Span.IsValid() {
		writeSnippet(&sb, f, d.Span, opts)
	}

	for _, rel := range d.Related {
		sb.WriteString("\n")
		if f != nil && rel.Span.IsValid() {
			p := f.Position(rel.Span.Start)
			fmt.Fprintf(&sb, "  %s:%d:%d: note: %s", displayName(f), p.Line, p.Col, rel.Message)
		} else {
			fmt.Fprintf(&sb, "  note: %s", rel.Message)
		}
	}

	for _, fix := range d.Fixes {
		fmt.Fprintf(&sb, "\n  help: %s", fix.Title)
	}

	return sb.String()
}

// RenderAll formats every diagnostic, one per entry, in sorted order.
func RenderAll(f *source.File, ds []Diagnostic, opts RenderOptions) string {
	var lines []string
	for _, d := range ds {
		lines = append(lines, Render(f, d, opts))
	}
	return strings.Join(lines, "\n")
}

func writeHeader(sb *strings.Builder, f *source.File, d Diagnostic) {
	if f != nil && d.Span.IsValid() {
		p := f.Position(d.Span.Start)
		fmt.Fprintf(sb, "%s:%d:%d: ", displayName(f), p.Line, p.Col)
	}
	fmt.Fprintf(sb, "%s: %s", d.Severity, d.Message)
	if d.Code != "" {
		fmt.Fprintf(sb, " [%s]", d.Code)
	}
}

func displayName(f *source.File) string {
	if n := f.Name(); n != "" {
		return n
	}
	return "<input>"
}

// writeSnippet appends the source line and a caret run underlining the span.
//
// Only the first line of a multi-line span is underlined; clinical statements
// are line-oriented, so a span crossing lines means something already went
// wrong and a full multi-line renderer would be noise.
func writeSnippet(sb *strings.Builder, f *source.File, span source.Span, opts RenderOptions) {
	tab := opts.TabWidth
	if tab <= 0 {
		tab = 4
	}

	start := f.Position(span.Start)
	lineText := f.LineText(start.Line)
	lineSpan := f.LineSpan(start.Line)

	// Clamp the underline to this line so a runaway span cannot draw carets
	// past the end of the text.
	end := span.End
	if end > lineSpan.End {
		end = lineSpan.End
	}
	if end < span.Start {
		end = span.Start
	}

	prefix := f.Content()[lineSpan.Start:span.Start]
	underlined := f.Content()[span.Start:end]

	gutter := fmt.Sprintf("%d | ", start.Line)
	pad := strings.Repeat(" ", len(gutter))

	sb.WriteString("\n")
	sb.WriteString(gutter)
	sb.WriteString(expandTabs(lineText, tab))
	sb.WriteString("\n")
	sb.WriteString(pad)
	sb.WriteString(strings.Repeat(" ", displayWidth(prefix, tab)))

	carets := displayWidth(underlined, tab)
	if carets < 1 {
		carets = 1
	}
	sb.WriteString(strings.Repeat("^", carets))
}

// displayWidth counts the columns s occupies, expanding tabs to the next
// multiple of tab. Every other rune counts as one column: clinical text is
// overwhelmingly narrow, and full east-asian width handling would pull in a
// dependency for a case ClinLang does not currently meet.
func displayWidth(s string, tab int) int {
	w := 0
	for _, r := range s {
		if r == '\t' {
			w += tab - (w % tab)
			continue
		}
		w++
	}
	return w
}

func expandTabs(s string, tab int) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + utf8.RuneCountInString(s))
	w := 0
	for _, r := range s {
		if r == '\t' {
			n := tab - (w % tab)
			sb.WriteString(strings.Repeat(" ", n))
			w += n
			continue
		}
		sb.WriteRune(r)
		w++
	}
	return sb.String()
}

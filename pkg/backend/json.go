package backend

import (
	"bytes"
	"encoding/json"

	"clinlang/pkg/diag"
	"clinlang/pkg/ir"
)

// JSON renders the IR itself.
//
// This is the structured output an integration consumes. It is the IR verbatim
// rather than a bespoke DTO, so what a caller receives is exactly what the
// engine understood: every value with its unit, every concept with its
// identity, every node with the source span it came from.
type JSON struct{}

func (JSON) Name() string        { return "json" }
func (JSON) ContentType() string { return "application/json; charset=utf-8" }

// Emit serialises the case.
//
// HTML escaping is disabled because clinical text legitimately contains &, <
// and > — a chief complaint of "sats <90 on air" must round-trip as written,
// not as <90.
func (b JSON) Emit(c *ir.Case, opts Options) ([]byte, []diag.Diagnostic, error) {
	payload := any(c)
	if len(opts.Diagnostics) > 0 {
		payload = struct {
			*ir.Case
			Diagnostics []diag.Diagnostic `json:"diagnostics"`
		}{c, opts.Diagnostics}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), nil, nil
}

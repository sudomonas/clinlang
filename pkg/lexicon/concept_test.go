package lexicon

import (
	"testing"
)

// Every alias of a thing resolves to one identity. Without this, "tds" and
// "tid" are unrelated strings that happen to render the same way.
func TestAliasesShareOneIdentity(t *testing.T) {
	l := Default()

	groups := []struct {
		kind    ConceptKind
		aliases []string
	}{
		{KindFrequency, []string{"tds", "tid"}},
		{KindFrequency, []string{"bd", "bid", "twice"}},
		{KindFrequency, []string{"od", "qd", "daily"}},
		{KindRoute, []string{"po", "oral", "bymouth"}},
		{KindRoute, []string{"sc", "sq", "subcut", "subcutaneous"}},
	}

	for _, g := range groups {
		var first string
		for i, alias := range g.aliases {
			c, ok := l.Resolve(alias, g.kind)
			if !ok {
				t.Errorf("%s alias %q did not resolve", g.kind, alias)
				continue
			}
			if i == 0 {
				first = c.ID
				continue
			}
			if c.ID != first {
				t.Errorf("%q resolved to %q but %q resolved to %q; aliases must share one identity",
					g.aliases[0], first, alias, c.ID)
			}
		}
	}
}

// Kind-scoped resolution is what keeps one flat namespace from making lookups
// depend on load order.
func TestResolveIsKindScoped(t *testing.T) {
	l := Default()

	if c, ok := l.Resolve("po", KindRoute); !ok || c.ID != "route.po" {
		t.Errorf("po as a route resolved to %+v", c)
	}
	// The same text is not a measurement, and asking for one must fail rather
	// than returning the route.
	if c, ok := l.Resolve("po", KindMeasurement); ok {
		t.Errorf("po resolved as a measurement to %+v", c)
	}

	// Caller-supplied precedence: a prescription settles a bare word as a
	// frequency before a route before a drug.
	if c, ok := l.Resolve("tds", KindFrequency, KindRoute, KindDrug); !ok || c.Kind != KindFrequency {
		t.Errorf("tds resolved to %+v, want a frequency", c)
	}
}

func TestConceptListingIsDeterministic(t *testing.T) {
	l := Default()
	first := l.Concepts(KindRoute)
	for i := 0; i < 30; i++ {
		got := l.Concepts(KindRoute)
		if len(got) != len(first) {
			t.Fatalf("iteration %d returned %d concepts, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].ID != first[j].ID {
				t.Fatalf("iteration %d differs at %d: %q vs %q", i, j, got[j].ID, first[j].ID)
			}
		}
	}
}

func TestResolveHandlesEmptyAndUnknown(t *testing.T) {
	l := Default()
	for _, in := range []string{"", "   ", "definitelynotaconcept"} {
		if c, ok := l.Resolve(in); ok {
			t.Errorf("Resolve(%q) unexpectedly returned %+v", in, c)
		}
	}
	var nilLex *Lexicon
	if _, ok := nilLex.Resolve("hb"); ok {
		t.Error("Resolve on a nil lexicon must report failure")
	}
}

func BenchmarkResolve(b *testing.B) {
	l := Default()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Resolve("tds", KindFrequency, KindRoute)
	}
}

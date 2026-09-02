package plugin

import (
	"strings"
	"testing"

	"clinlang/pkg/ast"
	"clinlang/pkg/parser"
	"clinlang/pkg/sema"
)

type stub struct {
	m      Manifest
	shapes map[string]parser.Shape
	keys   []string
}

func (s *stub) Manifest() Manifest                                 { return s.m }
func (s *stub) Shapes() map[string]parser.Shape                    { return s.shapes }
func (s *stub) Keys() []string                                     { return s.keys }
func (s *stub) Name() string                                       { return s.m.Name }
func (s *stub) NewData() any                                       { return &struct{}{} }
func (s *stub) Command(string, *ast.Statement, *sema.Context) bool { return false }
func (s *stub) Argument(string, ast.Argument, *sema.Context) bool  { return false }

func manifest(name, engineRange string) Manifest {
	return Manifest{Name: name, Version: "1.0.0", EngineRange: engineRange}
}

func TestVersionParsing(t *testing.T) {
	tests := map[string]Version{
		"1.2.3":         {1, 2, 3},
		"v1.2.3":        {1, 2, 3},
		"2.0.0-beta.1":  {2, 0, 0},
		"1.4.2+build.5": {1, 4, 2},
		"1.2":           {1, 2, 0},
		"3":             {3, 0, 0},
	}
	for in, want := range tests {
		got, err := ParseVersion(in)
		if err != nil {
			t.Errorf("ParseVersion(%q) failed: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseVersion(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.2.3.4", "-1.0.0", "1.x.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) should have failed", bad)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	v := func(s string) Version {
		out, _ := ParseVersion(s)
		return out
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0", "2.0.0", -1},
	}
	for _, c := range cases {
		if got := v(c.a).Compare(v(c.b)); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRangeConstraints(t *testing.T) {
	r, err := ParseRange(">=1.2.0 <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"1.2.0", "1.2.1", "1.9.9"}
	refused := []string{"1.1.9", "2.0.0", "3.0.0"}

	for _, s := range allowed {
		v, _ := ParseVersion(s)
		if !r.Allows(v) {
			t.Errorf("%s should satisfy %s", s, r)
		}
	}
	for _, s := range refused {
		v, _ := ParseVersion(s)
		if r.Allows(v) {
			t.Errorf("%s should not satisfy %s", s, r)
		}
	}

	// An empty range constrains nothing.
	empty, _ := ParseRange("")
	if !empty.IsEmpty() {
		t.Error("empty range should report IsEmpty")
	}
	v, _ := ParseVersion("99.0.0")
	if !empty.Allows(v) {
		t.Error("empty range should allow anything")
	}
}

// Compatibility is declared and enforced. Once plugins are third-party this is
// the only thing between a core refactor and silent breakage.
func TestIncompatiblePluginIsRefused(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(&stub{m: manifest("old", ">=1.0.0 <2.0.0")}); err == nil {
		t.Error("a plugin built for engine 1.x must be refused by engine 2.x")
	}
	if err := r.Register(&stub{m: manifest("future", ">=9.0.0")}); err == nil {
		t.Error("a plugin requiring a future engine must be refused")
	}
	if err := r.Register(&stub{m: manifest("current", ">=2.0.0 <3.0.0")}); err != nil {
		t.Errorf("a compatible plugin was refused: %v", err)
	}
	// Refusal must be visible, not silent.
	if _, ok := r.Get("old"); ok {
		t.Error("a refused plugin must not be registered")
	}
}

func TestDuplicateAndNamelessAreRefused(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stub{m: manifest("obgyn", "")}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&stub{m: manifest("obgyn", "")}); err == nil {
		t.Error("duplicate registration should fail")
	}
	if err := r.Register(&stub{m: manifest("  ", "")}); err == nil {
		t.Error("a nameless plugin should fail")
	}
}

// A plugin must never change how an existing note parses, so it cannot claim a
// core command.
func TestPluginsCannotShadowCoreCommands(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&stub{
		m: manifest("rogue", ""),
		shapes: map[string]parser.Shape{
			"rx":        parser.FreeText, // core: must be refused
			"partogram": parser.FreeText, // new: must be accepted
		},
	})

	g, conflicts := r.Grammar(nil)
	if len(conflicts) != 1 || !strings.Contains(conflicts[0], "rx") {
		t.Fatalf("expected one conflict naming rx, got %v", conflicts)
	}
	if g.Shape("rx") != parser.Structured {
		t.Error("core shape for rx was overwritten by a plugin")
	}
	if g.Shape("partogram") != parser.FreeText {
		t.Error("plugin shape for a new command was not applied")
	}
}

func TestGrammarCombinesKeys(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&stub{m: manifest("p", ""), keys: []string{"ctg2"}})

	g, _ := r.Grammar([]string{"hb"})
	if g.SplitKey("hb13") != 2 {
		t.Error("core keys were lost")
	}
	if g.SplitKey("ctg2") != 4 {
		t.Error("plugin key was not added")
	}
}

// Dispatch order is registration order. The previous engine ranged over a map,
// so when two prefixes matched, the winner varied between runs.
func TestRegistrationOrderIsStable(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"zulu", "alpha", "mike"} {
		r.MustRegister(&stub{m: manifest(n, "")})
	}
	want := []string{"zulu", "alpha", "mike"}

	for i := 0; i < 50; i++ {
		got := r.Names()
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("iteration %d: order = %v, want %v", i, got, want)
			}
		}
	}

	// Manifests are sorted by name instead, for stable documentation.
	ms := r.Manifests()
	if ms[0].Name != "alpha" || ms[2].Name != "zulu" {
		t.Errorf("Manifests should be name-sorted, got %v", ms)
	}
}

// A plugin implements only the capabilities it needs.
func TestCapabilitiesAreOptional(t *testing.T) {
	type bare struct{ Plugin }
	r := NewRegistry()

	minimal := &manifestOnly{m: manifest("minimal", "")}
	if err := r.Register(minimal); err != nil {
		t.Fatalf("a manifest-only plugin should register: %v", err)
	}
	if len(r.Analyzers()) != 0 {
		t.Error("a plugin with no semantics should contribute no analyzer")
	}
	if len(r.Completions()) != 0 {
		t.Error("a plugin with no completions should contribute none")
	}
	g, conflicts := r.Grammar(nil)
	if g == nil || len(conflicts) != 0 {
		t.Error("a plugin with no grammar should not affect the grammar")
	}
	_ = bare{}
}

type manifestOnly struct{ m Manifest }

func (p *manifestOnly) Manifest() Manifest { return p.m }

func TestRegistryIsNotGlobal(t *testing.T) {
	// Two registries must be independent, so a server can serve tenants with
	// different plugin sets and tests need no cleanup.
	a, b := NewRegistry(), NewRegistry()
	a.MustRegister(&stub{m: manifest("only-in-a", "")})

	if _, ok := b.Get("only-in-a"); ok {
		t.Error("registries must not share state")
	}
}

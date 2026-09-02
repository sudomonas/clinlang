package plugin

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a major.minor.patch version.
//
// Hand-rolled rather than pulled from a dependency: ClinLang ships as a single
// binary with essentially no third-party code, and the subset of semver needed
// to answer "does this plugin work with this engine" is small enough that the
// dependency would cost more in supply-chain review than it saves in code.
// Pre-release and build metadata are parsed and then ignored, because nothing
// here needs to order them.
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion reads a version string such as "1.4.2" or "v1.4.2".
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")

	// Discard pre-release and build metadata.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}

	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}

	var v Version
	dst := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		*dst[i] = n
	}
	return v, nil
}

// String renders the version.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0 or 1 as v sorts before, equal to, or after other.
func (v Version) Compare(other Version) int {
	switch {
	case v.Major != other.Major:
		return sign(v.Major - other.Major)
	case v.Minor != other.Minor:
		return sign(v.Minor - other.Minor)
	case v.Patch != other.Patch:
		return sign(v.Patch - other.Patch)
	default:
		return 0
	}
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

// Range is a conjunction of version constraints, such as ">=1.2.0 <2.0.0".
//
// Supported operators are >=, >, <=, <, = and the bare version, which means =.
// Every constraint must hold.
type Range struct {
	source      string
	constraints []constraint
}

type constraint struct {
	op string
	v  Version
}

// ParseRange reads a space-separated constraint list.
//
// An empty range accepts every version, which is what an unspecified
// compatibility declaration means.
func ParseRange(s string) (Range, error) {
	r := Range{source: strings.TrimSpace(s)}
	if r.source == "" {
		return r, nil
	}

	for _, field := range strings.Fields(r.source) {
		op := "="
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(field, candidate) {
				op = candidate
				field = strings.TrimPrefix(field, candidate)
				break
			}
		}
		v, err := ParseVersion(field)
		if err != nil {
			return Range{}, fmt.Errorf("invalid constraint in %q: %w", s, err)
		}
		r.constraints = append(r.constraints, constraint{op: op, v: v})
	}
	return r, nil
}

// String returns the original constraint text.
func (r Range) String() string { return r.source }

// IsEmpty reports whether the range constrains nothing.
func (r Range) IsEmpty() bool { return len(r.constraints) == 0 }

// Allows reports whether v satisfies every constraint.
func (r Range) Allows(v Version) bool {
	for _, c := range r.constraints {
		cmp := v.Compare(c.v)
		ok := false
		switch c.op {
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			ok = cmp < 0
		case "=":
			ok = cmp == 0
		}
		if !ok {
			return false
		}
	}
	return true
}

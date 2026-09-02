package diag

// Diagnostic codes are stable, greppable identifiers. Once published, a code's
// meaning never changes: wording may be reworded and severity may be adjusted,
// but CLN2003 refers to the same defect forever. Renaming a concept means
// retiring the old code and issuing a new one.
//
// Ranges are allocated by validation layer so that a whole layer can be
// filtered, documented, or disabled as a unit:
//
//	CLN0xxx  deprecation and lifecycle notices
//	CLN1xxx  lexical and syntactic — malformed input, operates on tokens/AST
//	CLN2xxx  structural — well-formed tokens in an invalid arrangement
//	CLN3xxx  semantic — resolved against the lexicon and profile scope
//	CLN9xxx  internal — engine invariant violations, should never reach a user
//
// Two ranges are deliberately unallocated. CLN4xxx (terminology) and CLN5xxx
// (data sanity, e.g. "spo2 150") belong to layers this engine does not
// implement: ClinLang does not check whether a clinical value is plausible.
// Reserving the ranges keeps the door open without implying the checks exist.
const (
	// ── CLN0xxx — lifecycle ──────────────────────────────────────────────────

	// DeprecatedSyntax marks input that still parses but is scheduled for
	// removal. Always a hint or warning, never an error.
	DeprecatedSyntax Code = "CLN0001"

	// ── CLN1xxx — lexical and syntactic ──────────────────────────────────────

	// UnterminatedString marks a quoted free-text run with no closing quote.
	UnterminatedString Code = "CLN1001"

	// InvalidCharacter marks a byte that cannot begin any token.
	InvalidCharacter Code = "CLN1002"

	// MalformedNumber marks a numeric literal that cannot be parsed, such as
	// one carrying more than one decimal point.
	MalformedNumber Code = "CLN1003"

	// MalformedRatio marks a ratio with a missing or non-numeric side, such as
	// the "bp140//90" the old parser accepted silently.
	MalformedRatio Code = "CLN1004"

	// UnexpectedToken marks a token that cannot appear where it was found.
	UnexpectedToken Code = "CLN1005"

	// TrailingQualifier marks an intensity run with nothing to qualify, such as
	// a bare "+++".
	TrailingQualifier Code = "CLN1006"

	// ── CLN2xxx — structural ─────────────────────────────────────────────────

	// UnknownCommand marks a leading word that matches no core command and no
	// command contributed by an active profile.
	UnknownCommand Code = "CLN2001"

	// EmptyCommand marks a command that requires arguments but was given none.
	EmptyCommand Code = "CLN2002"

	// MissingDrug marks an rx statement with no medication name.
	MissingDrug Code = "CLN2003"

	// UnknownPragma marks an @-directive the engine does not recognise.
	UnknownPragma Code = "CLN2004"

	// MalformedPragma marks a recognised pragma with an unusable argument list.
	MalformedPragma Code = "CLN2005"

	// UnknownProfile marks a profile named in @profile that is not registered.
	UnknownProfile Code = "CLN2006"

	// DuplicateStatement marks a second occurrence of a statement that may
	// appear only once per encounter, such as pt.
	DuplicateStatement Code = "CLN2007"

	// CommandConflict marks a profile command suppressed because it collides
	// with a core command. Profiles are additive-only and never shadow core.
	CommandConflict Code = "CLN2008"

	// TokenConflict marks a profile inline-token prefix suppressed because
	// another already-registered prefix claims it.
	TokenConflict Code = "CLN2009"

	// ── CLN3xxx — semantic ───────────────────────────────────────────────────

	// UnrecognizedToken marks an argument that no core rule and no active
	// profile extension could interpret. This is the replacement for the old
	// position-free "Unrecognized vitals token: xyz" warnings.
	UnrecognizedToken Code = "CLN3001"

	// MissingUnit marks a quantity whose unit is required but absent, such as a
	// dose given as a bare number.
	MissingUnit Code = "CLN3002"

	// UnknownUnit marks a unit token that is not in the configured unit table.
	UnknownUnit Code = "CLN3003"

	// AmbiguousKey marks a compound word that could split into key and value in
	// more than one way, where the lexicon does not settle it.
	AmbiguousKey Code = "CLN3004"

	// MissingFrequency marks a prescription with no dosing frequency.
	MissingFrequency Code = "CLN3005"

	// MissingDemographic marks an absent age or sex on the pt statement.
	MissingDemographic Code = "CLN3006"

	// ConflictingValue marks a field assigned twice within one statement with
	// two different values.
	ConflictingValue Code = "CLN3007"

	// UnknownAbbreviation marks shorthand that no lexicon entry expands. It is
	// a hint, not an error: unknown text passes through verbatim by design.
	UnknownAbbreviation Code = "CLN3008"

	// ── CLN9xxx — internal ───────────────────────────────────────────────────

	// InternalError marks a violated engine invariant. Reaching one is a bug in
	// ClinLang, not in the user's input.
	InternalError Code = "CLN9001"

	// InputTooLarge marks input exceeding the configured size limit.
	InputTooLarge Code = "CLN9002"

	// Cancelled marks a parse abandoned because its context was cancelled.
	Cancelled Code = "CLN9003"
)

// codeInfo is the registry backing documentation, tooling, and the conformance
// test that asserts every emitted code is declared here.
type codeInfo struct {
	Code    Code
	Layer   string
	Summary string
}

var codeRegistry = []codeInfo{
	{DeprecatedSyntax, "lifecycle", "Syntax still accepted but scheduled for removal"},

	{UnterminatedString, "syntax", "Quoted text has no closing quote"},
	{InvalidCharacter, "syntax", "Character cannot begin a token"},
	{MalformedNumber, "syntax", "Numeric literal could not be parsed"},
	{MalformedRatio, "syntax", "Ratio is missing a numerator or denominator"},
	{UnexpectedToken, "syntax", "Token is not valid in this position"},
	{TrailingQualifier, "syntax", "Intensity marker has nothing to qualify"},

	{UnknownCommand, "structural", "No core command or active profile defines this command"},
	{EmptyCommand, "structural", "Command requires arguments but received none"},
	{MissingDrug, "structural", "Prescription has no medication name"},
	{UnknownPragma, "structural", "Unrecognised @ directive"},
	{MalformedPragma, "structural", "Directive arguments could not be read"},
	{UnknownProfile, "structural", "Named profile is not registered"},
	{DuplicateStatement, "structural", "Statement may appear only once per encounter"},
	{CommandConflict, "structural", "Profile command suppressed; core commands are never shadowed"},
	{TokenConflict, "structural", "Profile token prefix already claimed"},

	{UnrecognizedToken, "semantic", "Argument matched no core rule or profile extension"},
	{MissingUnit, "semantic", "Quantity requires a unit"},
	{UnknownUnit, "semantic", "Unit is not in the configured unit table"},
	{AmbiguousKey, "semantic", "Word splits into key and value in more than one way"},
	{MissingFrequency, "semantic", "Prescription has no dosing frequency"},
	{MissingDemographic, "semantic", "Patient age or sex not stated"},
	{ConflictingValue, "semantic", "Field assigned two different values in one statement"},
	{UnknownAbbreviation, "semantic", "No lexicon entry expands this shorthand"},

	{InternalError, "internal", "Engine invariant violated"},
	{InputTooLarge, "internal", "Input exceeds the configured size limit"},
	{Cancelled, "internal", "Parse abandoned because the context was cancelled"},
}

// Codes returns every declared diagnostic code, in registry order.
func Codes() []Code {
	out := make([]Code, 0, len(codeRegistry))
	for _, info := range codeRegistry {
		out = append(out, info.Code)
	}
	return out
}

// Describe returns the layer and one-line summary for a code.
//
// The bool is false for codes that are not declared, which is what the
// conformance test checks for.
func Describe(c Code) (layer, summary string, ok bool) {
	for _, info := range codeRegistry {
		if info.Code == c {
			return info.Layer, info.Summary, true
		}
	}
	return "", "", false
}

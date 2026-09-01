package diag

// Stable diagnostic codes. These are a PUBLIC CONTRACT (spec FR-010): each
// condition maps to exactly one code, codes are printed by `rune analyze`, sent
// to editors, and asserted by golden tests. A code's meaning never changes once
// shipped. See specs/011-rune-lsp/contracts/diagnostic-codes.md.
const (
	// Parser diagnostics (RUNE1xxx) — always error severity.
	CodeUnexpectedToken   = "RUNE1001"
	CodeInvalidIndent     = "RUNE1002"
	CodeUnterminatedStr   = "RUNE1003"
	CodeIncompleteExpr    = "RUNE1004"
	CodeMalformedTaskDecl = "RUNE1005"
	// Malformed inline parameter constraint: enum without a value list, empty
	// enum(), a non-string enum value, or a value list on a non-enum kind
	// (spec 023).
	CodeMalformedConstraint = "RUNE1006"

	// Semantic diagnostics (RUNE2xxx) — error, except RUNE2010 (warning).
	CodeUnknownDependency  = "RUNE2001"
	CodeDuplicateTask      = "RUNE2002"
	CodeDependencyCycle    = "RUNE2003"
	CodeUndefinedVariable  = "RUNE2004"
	CodeWrongArgCount      = "RUNE2005"
	CodeDuplicateParam     = "RUNE2006"
	CodeInvalidAttribute   = "RUNE2007"
	CodeInvalidSetting     = "RUNE2008"
	CodeInvalidExecutor    = "RUNE2009"
	CodeUndocumentedTask   = "RUNE2010" // warning: public task lacks documentation (FR-008a)
	CodeInvalidFailureHook = "RUNE2011" // a || failure hook targets an agent-executor task (spec 022 FR-011)

	// Parameter type annotations and annotation attributes (spec 023).
	CodeUnknownParamType    = "RUNE2012" // unknown constraint kind name
	CodeInvalidEnumValues   = "RUNE2013" // duplicate enum values
	CodeDefaultViolatesType = "RUNE2014" // literal default violates its own constraint
	CodeInvalidAnnotation   = "RUNE2015" // param-doc names an unknown parameter / duplicate param-doc / duplicate returns
	CodeKindShadowsTask     = "RUNE2016" // warning: annotated kind name is also a task name (possible legacy re-parse)

	// Project diagnostics (RUNE3xxx) — always error severity.
	CodeUnresolvedImport   = "RUNE3001"
	CodeImportCycle        = "RUNE3002"
	CodeDuplicateNamespace = "RUNE3003"
	CodeIncompatibleVer    = "RUNE3004"
)

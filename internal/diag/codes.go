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

	// Project diagnostics (RUNE3xxx) — always error severity.
	CodeUnresolvedImport   = "RUNE3001"
	CodeImportCycle        = "RUNE3002"
	CodeDuplicateNamespace = "RUNE3003"
	CodeIncompatibleVer    = "RUNE3004"
)

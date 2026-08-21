package dsl

import "fmt"

// Pos records a source position in a text file.
type Pos struct {
	Line   int // 1-based line number
	Column int // 1-based byte column number
	Offset int // 0-based byte offset
}

func (p Pos) String() string {
	if p.Line == 0 && p.Column == 0 {
		return "-"
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// TokenType represents a lexical token type.
type TokenType uint16

const (
	TokenEOF TokenType = iota
	TokenIllegal

	// Identifiers and Literals
	TokenIdent     // e.g. driver, status, my_rule
	TokenString    // e.g. "foo"
	TokenInt       // e.g. 123, -45
	TokenDecimal   // e.g. 12.34
	TokenTimestamp // e.g. ts("2026-08-21T10:00:00Z")
	TokenDate      // e.g. date("2026-08-21")
	TokenDuration  // e.g. dur("8h")
	TokenAtom      // e.g. atom("ACTIVE") or :ACTIVE

	// Keywords
	TokenSchema
	TokenEntity
	TokenRelation
	TokenFrom
	TokenTo
	TokenRule
	TokenReads
	TokenWrites
	TokenDependsOn
	TokenSelect
	TokenWhere
	TokenGroupBy
	TokenHaving
	TokenSet
	TokenInsert
	TokenInto
	TokenAs
	TokenDelete
	TokenRelate
	TokenUnrelate
	TokenMerge
	TokenSplit
	TokenPartition
	TokenDiscriminator
	TokenReanchorRelations
	TokenRetainSources
	TokenRetainSource
	TokenGuard
	TokenRequired
	TokenOptional
	TokenTrue
	TokenFalse
	TokenNull
	TokenCheckpoint
	TokenAfter
	TokenProfile
	TokenFor
	TokenRequire
	TokenPresent
	TokenImplies

	// Built-in Function Keywords
	TokenAll
	TokenAny
	TokenAllEqual
	TokenExists
	TokenCount
	TokenSum
	TokenMin
	TokenMax
	TokenCoalesce
	TokenIf
	TokenConcat
	TokenSubstring
	TokenTrim
	TokenAbs
	TokenClamp
	TokenDateAdd
	TokenDateDiff
	TokenExtract

	// Punctuation & Operators
	TokenDot       // .
	TokenComma     // ,
	TokenColon     // :
	TokenSemicolon // ;
	TokenAssign    // =
	TokenLParen    // (
	TokenRParen    // )
	TokenLBrace    // {
	TokenRBrace    // }
	TokenLBracket  // [
	TokenRBracket  // ]

	// Operators
	TokenEqual    // ==
	TokenNotEqual // !=
	TokenLess     // <
	TokenLessEq   // <=
	TokenGreater  // >
	TokenGreatEq  // >=
	TokenAnd      // && or and
	TokenOr       // || or or
	TokenNot      // ! or not
	TokenPlus     // +
	TokenMinus    // -
	TokenStar     // *
	TokenSlash    // /
	TokenPercent  // %
	TokenArrow    // ->
)

var keywords = map[string]TokenType{
	"schema":             TokenSchema,
	"entity":             TokenEntity,
	"relation":           TokenRelation,
	"from":               TokenFrom,
	"to":                 TokenTo,
	"rule":               TokenRule,
	"reads":              TokenReads,
	"writes":             TokenWrites,
	"depends_on":         TokenDependsOn,
	"select":             TokenSelect,
	"where":              TokenWhere,
	"group_by":           TokenGroupBy,
	"having":             TokenHaving,
	"set":                TokenSet,
	"insert":             TokenInsert,
	"into":               TokenInto,
	"as":                 TokenAs,
	"delete":             TokenDelete,
	"relate":             TokenRelate,
	"unrelate":           TokenUnrelate,
	"merge":              TokenMerge,
	"split":              TokenSplit,
	"partition":          TokenPartition,
	"discriminator":      TokenDiscriminator,
	"reanchor_relations": TokenReanchorRelations,
	"retain_sources":     TokenRetainSources,
	"retain_source":      TokenRetainSource,
	"guard":              TokenGuard,
	"required":           TokenRequired,
	"optional":           TokenOptional,
	"true":               TokenTrue,
	"false":              TokenFalse,
	"null":               TokenNull,
	"checkpoint":         TokenCheckpoint,
	"after":              TokenAfter,
	"profile":            TokenProfile,
	"for":                TokenFor,
	"require":            TokenRequire,
	"present":            TokenPresent,
	"implies":            TokenImplies,
	"and":                TokenAnd,
	"or":                 TokenOr,
	"not":                TokenNot,
	"all":                TokenAll,
	"any":                TokenAny,
	"all_equal":          TokenAllEqual,
	"exists":             TokenExists,
	"count":              TokenCount,
	"sum":                TokenSum,
	"min":                TokenMin,
	"max":                TokenMax,
	"coalesce":           TokenCoalesce,
	"if":                 TokenIf,
	"concat":             TokenConcat,
	"substring":          TokenSubstring,
	"trim":               TokenTrim,
	"abs":                TokenAbs,
	"clamp":              TokenClamp,
	"date_add":           TokenDateAdd,
	"date_diff":          TokenDateDiff,
	"extract":            TokenExtract,
}

// Token represents one lexical token.
type Token struct {
	Type    TokenType
	Literal string
	Pos     Pos
}

func (t Token) String() string {
	return fmt.Sprintf("%v(%q)@%s", t.Type, t.Literal, t.Pos)
}

func LookupIdent(ident string) TokenType {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return TokenIdent
}

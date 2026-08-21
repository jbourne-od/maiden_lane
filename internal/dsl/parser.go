package dsl

import (
	"fmt"
)

// Precedence levels for Pratt expression parsing.
const (
	_ int = iota
	precLowest
	precOr
	precAnd
	precEquals
	precLessGreater
	precSum
	precProduct
	precPrefix
	precCall
	precIndex
)

var precedences = map[TokenType]int{
	TokenOr:       precOr,
	TokenAnd:      precAnd,
	TokenEqual:    precEquals,
	TokenNotEqual: precEquals,
	TokenLess:     precLessGreater,
	TokenLessEq:   precLessGreater,
	TokenGreater:  precLessGreater,
	TokenGreatEq:  precLessGreater,
	TokenPlus:     precSum,
	TokenMinus:    precSum,
	TokenStar:     precProduct,
	TokenSlash:    precProduct,
	TokenPercent:  precProduct,
	TokenLParen:   precCall,
	TokenDot:      precIndex,
}

// Parser parses tokens into an AST.
type Parser struct {
	l         *Lexer
	curToken  Token
	peekToken Token
	errors    []string
}

// NewParser creates a new Parser.
func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l}
	// Read two tokens so curToken and peekToken are both set
	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *Parser) curTokenIs(t TokenType) bool {
	return p.curToken.Type == t
}

func (p *Parser) peekTokenIs(t TokenType) bool {
	return p.peekToken.Type == t
}

func (p *Parser) expectPeek(t TokenType) bool {
	if p.peekTokenIs(t) {
		p.nextToken()
		return true
	}
	p.peekError(t)
	return false
}

func (p *Parser) peekError(t TokenType) {
	msg := fmt.Sprintf("%s: expected next token to be %v, got %v (%q) instead",
		p.peekToken.Pos, t, p.peekToken.Type, p.peekToken.Literal)
	p.errors = append(p.errors, msg)
}

func (p *Parser) Errors() []string {
	return p.errors
}

// ParseFile parses a complete .ml source file.
func (p *Parser) ParseFile() (*File, error) {
	file := &File{Pos: p.curToken.Pos}
	for !p.curTokenIs(TokenEOF) {
		decl := p.parseDeclaration()
		if decl != nil {
			file.Declarations = append(file.Declarations, decl)
		}
		if p.curTokenIs(TokenSemicolon) {
			p.nextToken()
		} else {
			p.nextToken()
		}
	}
	if len(p.errors) > 0 {
		return file, fmt.Errorf("parse errors:\n  %s", formatErrors(p.errors))
	}
	return file, nil
}

func formatErrors(errs []string) string {
	res := ""
	for i, e := range errs {
		if i > 0 {
			res += "\n  "
		}
		res += e
	}
	return res
}

func (p *Parser) parseDeclaration() Declaration {
	switch p.curToken.Type {
	case TokenSchema:
		return p.parseSchemaDecl()
	case TokenEntity:
		return p.parseEntityDeclStandalone()
	case TokenRelation:
		return p.parseRelationDeclStandalone()
	case TokenRule:
		return p.parseRuleDecl()
	case TokenCheckpoint:
		return p.parseCheckpointDecl()
	case TokenProfile:
		return p.parseProfileDecl()
	case TokenSemicolon:
		return nil
	default:
		p.errors = append(p.errors, fmt.Sprintf("%s: unexpected token %v (%q) for top-level declaration",
			p.curToken.Pos, p.curToken.Type, p.curToken.Literal))
		return nil
	}
}

func (p *Parser) parseSchemaDecl() *SchemaDecl {
	decl := &SchemaDecl{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenEntity:
			ent := p.parseEntityDecl()
			if ent != nil {
				decl.Entities = append(decl.Entities, ent)
			}
		case TokenRelation:
			rel := p.parseRelationDecl()
			if rel != nil {
				decl.Relations = append(decl.Relations, rel)
			}
		default:
			p.errors = append(p.errors, fmt.Sprintf("%s: unexpected token %v in schema declaration",
				p.curToken.Pos, p.curToken.Type))
			return nil
		}
		p.nextToken()
	}
	return decl
}

func (p *Parser) parseEntityDeclStandalone() *SchemaDecl {
	ent := p.parseEntityDecl()
	if ent == nil {
		return nil
	}
	return &SchemaDecl{
		Pos:      ent.Pos,
		Entities: []*EntityDecl{ent},
	}
}

func (p *Parser) parseRelationDeclStandalone() *SchemaDecl {
	rel := p.parseRelationDecl()
	if rel == nil {
		return nil
	}
	return &SchemaDecl{
		Pos:       rel.Pos,
		Relations: []*RelationDecl{rel},
	}
}

func (p *Parser) parseEntityDecl() *EntityDecl {
	ent := &EntityDecl{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	ent.Kind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		field := p.parseFieldDecl()
		if field != nil {
			ent.Fields = append(ent.Fields, field)
		}
		p.nextToken()
	}
	return ent
}

func (p *Parser) parseFieldDecl() *FieldDecl {
	field := &FieldDecl{Pos: p.curToken.Pos}
	if !p.curTokenIs(TokenIdent) {
		p.errors = append(p.errors, fmt.Sprintf("%s: expected field name, got %v", p.curToken.Pos, p.curToken.Type))
		return nil
	}
	field.Name = p.curToken.Literal

	if !p.expectPeek(TokenColon) {
		return nil
	}
	p.nextToken() // consume :

	if !p.curTokenIs(TokenIdent) {
		p.errors = append(p.errors, fmt.Sprintf("%s: expected field type, got %v", p.curToken.Pos, p.curToken.Type))
		return nil
	}
	field.Type = p.curToken.Literal

	field.Required = true
	if p.peekTokenIs(TokenOptional) {
		p.nextToken()
		field.Required = false
	} else if p.peekTokenIs(TokenRequired) {
		p.nextToken()
		field.Required = true
	}

	if p.peekTokenIs(TokenComma) || p.peekTokenIs(TokenSemicolon) {
		p.nextToken() // consume optional delimiter
	}
	return field
}

func (p *Parser) parseRelationDecl() *RelationDecl {
	rel := &RelationDecl{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	rel.Kind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenFrom:
			if !p.expectPeek(TokenColon) || !p.expectPeek(TokenIdent) {
				return nil
			}
			rel.FromKind = p.curToken.Literal
		case TokenTo:
			if !p.expectPeek(TokenColon) || !p.expectPeek(TokenIdent) {
				return nil
			}
			rel.ToKind = p.curToken.Literal
		}
		if p.peekTokenIs(TokenComma) || p.peekTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		p.nextToken()
	}
	return rel
}

func (p *Parser) parseRuleDecl() *RuleDecl {
	rule := &RuleDecl{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) && !p.expectPeek(TokenString) {
		return nil
	}
	rule.ID = p.curToken.Literal

	// Optional annotations: (reads: [...], writes: [...], depends_on: [...])
	if p.peekTokenIs(TokenLParen) {
		p.nextToken() // consume (
		p.parseRuleAnnotations(rule)
	}

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	rule.Operator = p.parseOperator()
	if p.peekTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	if !p.expectPeek(TokenRBrace) {
		return nil
	}
	if p.peekTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return rule
}

func (p *Parser) parseCheckpointDecl() *CheckpointDecl {
	decl := &CheckpointDecl{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) && !p.expectPeek(TokenString) {
		return nil
	}
	decl.Key = p.curToken.Literal

	if !p.expectPeek(TokenAfter) {
		return nil
	}
	if !p.expectPeek(TokenIdent) && !p.expectPeek(TokenString) {
		return nil
	}
	decl.After = p.curToken.Literal

	if p.peekTokenIs(TokenSemicolon) {
		p.nextToken()
	}
	return decl
}

func (p *Parser) parseProfileDecl() *ProfileDecl {
	decl := &ProfileDecl{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) && !p.expectPeek(TokenString) {
		return nil
	}
	decl.Key = p.curToken.Literal

	if !p.expectPeek(TokenFor) || !p.expectPeek(TokenEntity) || !p.expectPeek(TokenIdent) {
		return nil
	}
	decl.EntityKind = p.curToken.Literal

	if p.peekTokenIs(TokenLParen) {
		p.nextToken() // (
		for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
			p.nextToken()
			if p.curTokenIs(TokenImplies) {
				if p.expectPeek(TokenColon) && p.expectPeek(TokenLBracket) {
					decl.Implies = p.parseStringList()
				}
			}
		}
	}

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenRequire) {
			req := p.parseRequirementDecl()
			if req != nil {
				decl.Requirements = append(decl.Requirements, req)
			}
		}
		if p.peekTokenIs(TokenSemicolon) {
			p.nextToken()
		}
		p.nextToken()
	}
	return decl
}

func (p *Parser) parseRequirementDecl() *RequirementDecl {
	req := &RequirementDecl{Pos: p.curToken.Pos}
	p.nextToken() // consume require
	pathExpr := p.parsePathExpression()
	if pathExpr == nil {
		return nil
	}
	req.Field = pathExpr.Path

	if !p.expectPeek(TokenPresent) || !p.expectPeek(TokenAs) {
		return nil
	}
	if !p.expectPeek(TokenIdent) && !p.expectPeek(TokenString) {
		return nil
	}
	req.Code = p.curToken.Literal
	return req
}

func (p *Parser) parseRuleAnnotations(rule *RuleDecl) {
	for !p.curTokenIs(TokenRParen) && !p.curTokenIs(TokenEOF) {
		p.nextToken()
		switch p.curToken.Type {
		case TokenReads:
			if p.expectPeek(TokenColon) && p.expectPeek(TokenLBracket) {
				rule.DeclaredReads = p.parseStringList()
			}
		case TokenWrites:
			if p.expectPeek(TokenColon) && p.expectPeek(TokenLBracket) {
				rule.DeclaredWrites = p.parseStringList()
			}
		case TokenDependsOn:
			if p.expectPeek(TokenColon) && p.expectPeek(TokenLBracket) {
				rule.DependsOn = p.parseStringList()
			}
		}
		if p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
	}
}

func (p *Parser) parseStringList() []string {
	var list []string
	p.nextToken() // consume [
	for !p.curTokenIs(TokenRBracket) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenIdent) || p.curTokenIs(TokenString) {
			list = append(list, p.curToken.Literal)
		}
		if p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}
	return list
}

func (p *Parser) parseOperator() OperatorNode {
	switch p.curToken.Type {
	case TokenSelect:
		return p.parseSelectAndAssign()
	case TokenInsert:
		return p.parseInsertEntity()
	case TokenDelete:
		return p.parseDeleteEntity()
	case TokenRelate:
		return p.parseRelateEntities()
	case TokenUnrelate:
		return p.parseUnrelateEntities()
	case TokenMerge:
		return p.parseMergeEntities()
	case TokenSplit:
		return p.parseSplitEntity()
	default:
		p.errors = append(p.errors, fmt.Sprintf("%s: unknown operator keyword %v (%q)",
			p.curToken.Pos, p.curToken.Type, p.curToken.Literal))
		return nil
	}
}

func (p *Parser) parseSelector() *SelectorNode {
	sel := &SelectorNode{Pos: p.curToken.Pos, CardinalityKind: "any"}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	sel.Kind = p.curToken.Literal

	// Optional where
	if p.peekTokenIs(TokenWhere) {
		p.nextToken() // consume where
		p.nextToken()
		sel.Where = p.parseExpression(precLowest)
	}

	// Optional group by
	if p.peekTokenIs(TokenGroupBy) {
		p.nextToken() // consume group_by
		p.nextToken()
		sel.GroupBy = p.parseExpression(precLowest)
	}

	// Optional having
	if p.peekTokenIs(TokenHaving) {
		p.nextToken() // consume having
		p.nextToken()
		sel.Having = p.parseExpression(precLowest)
	}
	return sel
}

func (p *Parser) parseAssignments() []*AssignmentNode {
	var list []*AssignmentNode
	if !p.curTokenIs(TokenSet) {
		if !p.expectPeek(TokenSet) {
			return nil
		}
	}
	p.nextToken() // consume set

	for {
		assign := &AssignmentNode{Pos: p.curToken.Pos}
		targetExpr := p.parsePathExpression()
		if targetExpr == nil {
			break
		}
		assign.Target = targetExpr.Path

		if !p.expectPeek(TokenAssign) {
			return nil
		}
		p.nextToken() // consume =
		assign.Value = p.parseExpression(precLowest)
		list = append(list, assign)

		if p.peekTokenIs(TokenComma) {
			p.nextToken() // consume ,
			p.nextToken()
			continue
		}
		if p.peekTokenIs(TokenSemicolon) {
			p.nextToken() // consume ;
		}
		break
	}
	return list
}

func (p *Parser) parseSelectAndAssign() *SelectAndAssignNode {
	pos := p.curToken.Pos
	sel := p.parseSelector()
	var assignments []*AssignmentNode
	if p.peekTokenIs(TokenSet) {
		p.nextToken()
		assignments = p.parseAssignments()
	}
	return &SelectAndAssignNode{
		Pos:         pos,
		Selector:    sel,
		Guard:       sel.Having,
		Assignments: assignments,
	}
}

func (p *Parser) parseInsertEntity() *InsertEntityNode {
	node := &InsertEntityNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	node.TargetKind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenSelect:
			node.Selector = p.parseSelector()
			node.Guard = node.Selector.Having
		case TokenDiscriminator:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			node.Discriminator = p.parseExpression(precLowest)
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}

	if p.peekTokenIs(TokenSet) {
		p.nextToken()
		node.Assignments = p.parseAssignments()
	}
	return node
}

func (p *Parser) parseDeleteEntity() *DeleteEntityNode {
	node := &DeleteEntityNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		if p.curTokenIs(TokenSelect) {
			node.Selector = p.parseSelector()
			node.Guard = node.Selector.Having
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}
	return node
}

func (p *Parser) parseRelateEntities() *RelateEntitiesNode {
	node := &RelateEntitiesNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenTo) || !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenAs) {
		return nil
	}
	p.nextToken()
	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenString) {
		p.errors = append(p.errors, fmt.Sprintf("%s: expected relation name, got %v", p.curToken.Pos, p.curToken.Type))
		return nil
	}
	node.RelationKind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenFrom:
			if !p.expectPeek(TokenColon) || !p.expectPeek(TokenSelect) {
				return nil
			}
			node.FromSelector = p.parseSelector()
		case TokenTo:
			if !p.expectPeek(TokenColon) || !p.expectPeek(TokenSelect) {
				return nil
			}
			node.ToSelector = p.parseSelector()
		case TokenGuard:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			node.Guard = p.parseExpression(precLowest)
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}
	return node
}

func (p *Parser) parseUnrelateEntities() *UnrelateEntitiesNode {
	node := &UnrelateEntitiesNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenFrom) || !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenAs) {
		return nil
	}
	p.nextToken()
	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenString) {
		p.errors = append(p.errors, fmt.Sprintf("%s: expected relation name, got %v", p.curToken.Pos, p.curToken.Type))
		return nil
	}
	node.RelationKind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenFrom:
			if !p.expectPeek(TokenColon) || !p.expectPeek(TokenSelect) {
				return nil
			}
			node.FromSelector = p.parseSelector()
		case TokenTo:
			if !p.expectPeek(TokenColon) || !p.expectPeek(TokenSelect) {
				return nil
			}
			node.ToSelector = p.parseSelector()
		case TokenGuard:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			node.Guard = p.parseExpression(precLowest)
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}
	return node
}

func (p *Parser) parseMergeEntities() *MergeEntitiesNode {
	node := &MergeEntitiesNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenInto) || !p.expectPeek(TokenIdent) {
		return nil
	}
	node.TargetKind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenSelect:
			node.Selector = p.parseSelector()
			node.Guard = node.Selector.Having
		case TokenDiscriminator:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			node.Discriminator = p.parseExpression(precLowest)
		case TokenReanchorRelations:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			if !p.curTokenIs(TokenTrue) && !p.curTokenIs(TokenFalse) {
				p.errors = append(p.errors, fmt.Sprintf("%s: expected boolean for reanchor_relations, got %v", p.curToken.Pos, p.curToken.Type))
				return nil
			}
			node.ReanchorRelations = (p.curToken.Type == TokenTrue)
		case TokenRetainSources:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			if !p.curTokenIs(TokenTrue) && !p.curTokenIs(TokenFalse) {
				p.errors = append(p.errors, fmt.Sprintf("%s: expected boolean for retain_sources, got %v", p.curToken.Pos, p.curToken.Type))
				return nil
			}
			node.RetainSources = (p.curToken.Type == TokenTrue)
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}

	if p.peekTokenIs(TokenSet) {
		p.nextToken()
		node.Assignments = p.parseAssignments()
	}
	return node
}

func (p *Parser) parseSplitEntity() *SplitEntityNode {
	node := &SplitEntityNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenIdent) {
		return nil
	}
	if !p.expectPeek(TokenInto) || !p.expectPeek(TokenIdent) {
		return nil
	}
	node.TargetKind = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenSelect:
			node.Selector = p.parseSelector()
			node.Guard = node.Selector.Having
		case TokenRetainSource:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			if !p.curTokenIs(TokenTrue) && !p.curTokenIs(TokenFalse) {
				p.errors = append(p.errors, fmt.Sprintf("%s: expected boolean for retain_source, got %v", p.curToken.Pos, p.curToken.Type))
				return nil
			}
			node.RetainSource = (p.curToken.Type == TokenTrue)
		case TokenPartition:
			part := p.parsePartition()
			if part != nil {
				node.Partitions = append(node.Partitions, part)
			}
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}
	return node
}

func (p *Parser) parsePartition() *PartitionNode {
	part := &PartitionNode{Pos: p.curToken.Pos}
	if !p.expectPeek(TokenString) && !p.expectPeek(TokenIdent) {
		return nil
	}
	part.Name = p.curToken.Literal

	if !p.expectPeek(TokenLBrace) {
		return nil
	}
	p.nextToken() // consume {

	for !p.curTokenIs(TokenRBrace) && !p.curTokenIs(TokenEOF) {
		switch p.curToken.Type {
		case TokenDiscriminator:
			if !p.expectPeek(TokenColon) {
				return nil
			}
			p.nextToken()
			part.Discriminator = p.parseExpression(precLowest)
		case TokenSet:
			part.Assignments = p.parseAssignments()
		}
		if p.peekTokenIs(TokenSemicolon) || p.peekTokenIs(TokenComma) {
			p.nextToken()
		}
		p.nextToken()
	}
	return part
}

// Pratt expression parser.

func (p *Parser) parseExpression(precedence int) ExprNode {
	prefix := p.parsePrefixExpression()
	if prefix == nil {
		return nil
	}

	leftExp := prefix
	for !p.peekTokenIs(TokenSemicolon) && !p.peekTokenIs(TokenComma) && !p.peekTokenIs(TokenRBrace) && !p.peekTokenIs(TokenRParen) && precedence < p.peekPrecedence() {
		p.nextToken()
		leftExp = p.parseInfixExpression(leftExp)
	}
	return leftExp
}

func (p *Parser) parsePrefixExpression() ExprNode {
	switch p.curToken.Type {
	case TokenIdent, TokenFrom, TokenTo:
		return p.parsePathOrFunction()
	case TokenString:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitString, Literal: p.curToken.Literal}
	case TokenInt:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitInt, Literal: p.curToken.Literal}
	case TokenDecimal:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitDecimal, Literal: p.curToken.Literal}
	case TokenTrue:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitBool, Literal: "true"}
	case TokenFalse:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitBool, Literal: "false"}
	case TokenNull:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitNull, Literal: "null"}
	case TokenAtom:
		return &LiteralExpr{Pos: p.curToken.Pos, Kind: LitAtom, Literal: p.curToken.Literal}
	case TokenMinus:
		pos := p.curToken.Pos
		// Direct negative number literal support
		if p.peekTokenIs(TokenInt) {
			p.nextToken()
			return &LiteralExpr{Pos: pos, Kind: LitInt, Literal: "-" + p.curToken.Literal}
		}
		if p.peekTokenIs(TokenDecimal) {
			p.nextToken()
			return &LiteralExpr{Pos: pos, Kind: LitDecimal, Literal: "-" + p.curToken.Literal}
		}
		p.nextToken()
		right := p.parseExpression(precPrefix)
		return &UnaryExpr{Pos: pos, Op: TokenMinus, Right: right}
	case TokenNot:
		pos := p.curToken.Pos
		p.nextToken()
		right := p.parseExpression(precPrefix)
		return &UnaryExpr{Pos: pos, Op: TokenNot, Right: right}
	case TokenLParen:
		p.nextToken()
		exp := p.parseExpression(precLowest)
		if !p.expectPeek(TokenRParen) {
			return nil
		}
		return exp
	// Built-in keywords acting as function calls
	case TokenAll, TokenAny, TokenAllEqual, TokenExists, TokenCount, TokenSum, TokenMin, TokenMax,
		TokenCoalesce, TokenIf, TokenConcat, TokenSubstring, TokenTrim, TokenAbs, TokenClamp,
		TokenDateAdd, TokenDateDiff, TokenExtract:
		return p.parseBuiltinCall()
	default:
		p.errors = append(p.errors, fmt.Sprintf("%s: unexpected token %v (%q) in expression",
			p.curToken.Pos, p.curToken.Type, p.curToken.Literal))
		return nil
	}
}

func (p *Parser) parsePathOrFunction() ExprNode {
	pos := p.curToken.Pos
	name := p.curToken.Literal

	// Temporal/Atom constructor helpers: ts("..."), date("..."), dur("..."), atom("..."), dec("...")
	if p.peekTokenIs(TokenLParen) {
		switch name {
		case "ts", "timestamp":
			p.nextToken() // (
			p.nextToken() // arg
			lit := p.curToken.Literal
			p.expectPeek(TokenRParen)
			return &LiteralExpr{Pos: pos, Kind: LitTimestamp, Literal: lit}
		case "date":
			p.nextToken() // (
			p.nextToken() // arg
			lit := p.curToken.Literal
			p.expectPeek(TokenRParen)
			return &LiteralExpr{Pos: pos, Kind: LitDate, Literal: lit}
		case "dur", "duration":
			p.nextToken() // (
			p.nextToken() // arg
			lit := p.curToken.Literal
			p.expectPeek(TokenRParen)
			return &LiteralExpr{Pos: pos, Kind: LitDuration, Literal: lit}
		case "atom":
			p.nextToken() // (
			p.nextToken() // arg
			lit := p.curToken.Literal
			p.expectPeek(TokenRParen)
			return &LiteralExpr{Pos: pos, Kind: LitAtom, Literal: lit}
		case "dec", "decimal":
			p.nextToken() // (
			p.nextToken() // arg
			lit := p.curToken.Literal
			p.expectPeek(TokenRParen)
			return &LiteralExpr{Pos: pos, Kind: LitDecimal, Literal: lit}
		default:
			// General function call
			return p.parseCall(name, pos)
		}
	}

	// Path navigation (e.g. driver.depot, from.status)
	path := name
	for p.peekTokenIs(TokenDot) {
		p.nextToken() // consume .
		if !p.peekTokenIs(TokenIdent) && !p.peekTokenIs(TokenFrom) && !p.peekTokenIs(TokenTo) {
			p.errors = append(p.errors, fmt.Sprintf("%s: expected identifier after dot, got %v", p.peekToken.Pos, p.peekToken.Type))
			return nil
		}
		p.nextToken()
		path += "." + p.curToken.Literal
	}
	return &IdentExpr{Pos: pos, Path: path}
}

func (p *Parser) parsePathExpression() *IdentExpr {
	if !p.curTokenIs(TokenIdent) && !p.curTokenIs(TokenFrom) && !p.curTokenIs(TokenTo) {
		p.errors = append(p.errors, fmt.Sprintf("%s: expected identifier for path, got %v", p.curToken.Pos, p.curToken.Type))
		return nil
	}
	pos := p.curToken.Pos
	path := p.curToken.Literal
	for p.peekTokenIs(TokenDot) {
		p.nextToken() // consume .
		if !p.peekTokenIs(TokenIdent) && !p.peekTokenIs(TokenFrom) && !p.peekTokenIs(TokenTo) {
			p.errors = append(p.errors, fmt.Sprintf("%s: expected identifier after dot, got %v", p.peekToken.Pos, p.peekToken.Type))
			return nil
		}
		p.nextToken()
		path += "." + p.curToken.Literal
	}
	return &IdentExpr{Pos: pos, Path: path}
}

func (p *Parser) parseBuiltinCall() ExprNode {
	pos := p.curToken.Pos
	name := p.curToken.Literal
	return p.parseCall(name, pos)
}

func (p *Parser) parseCall(funcName string, pos Pos) ExprNode {
	if !p.expectPeek(TokenLParen) {
		return nil
	}
	var args []ExprNode
	if !p.peekTokenIs(TokenRParen) {
		p.nextToken()
		args = append(args, p.parseExpression(precLowest))
		for p.peekTokenIs(TokenComma) {
			p.nextToken() // consume ,
			p.nextToken()
			args = append(args, p.parseExpression(precLowest))
		}
	}
	if !p.expectPeek(TokenRParen) {
		return nil
	}
	return &CallExpr{Pos: pos, FuncName: funcName, Args: args}
}

func (p *Parser) parseInfixExpression(left ExprNode) ExprNode {
	pos := p.curToken.Pos
	op := p.curToken.Type
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	return &BinaryExpr{Pos: pos, Op: op, Left: left, Right: right}
}

func (p *Parser) peekPrecedence() int {
	if p, ok := precedences[p.peekToken.Type]; ok {
		return p
	}
	return precLowest
}

func (p *Parser) curPrecedence() int {
	if p, ok := precedences[p.curToken.Type]; ok {
		return p
	}
	return precLowest
}

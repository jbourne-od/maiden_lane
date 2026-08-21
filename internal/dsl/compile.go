package dsl

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// CompileText parses and compiles .ml source text into semantic SchemaDeclaration and RulesetDeclaration.
func CompileText(src string) (semantic.SchemaDeclaration, semantic.RulesetDeclaration, error) {
	req, err := CompileRequestFromText(src)
	if err != nil {
		return semantic.SchemaDeclaration{}, semantic.RulesetDeclaration{}, err
	}
	return req.Schema, req.Rules, nil
}

// CompileFile lowers an AST File into semantic SchemaDeclaration and RulesetDeclaration.
func CompileFile(file *File) (semantic.SchemaDeclaration, semantic.RulesetDeclaration, error) {
	req, err := CompileRequestFromFile(file)
	if err != nil {
		return semantic.SchemaDeclaration{}, semantic.RulesetDeclaration{}, err
	}
	return req.Schema, req.Rules, nil
}

// CompileRequestFromText parses and compiles .ml source text into a full semantic.CompileRequest.
func CompileRequestFromText(src string) (semantic.CompileRequest, error) {
	l := NewLexer(src)
	p := NewParser(l)
	file, err := p.ParseFile()
	if err != nil {
		return semantic.CompileRequest{}, err
	}
	return CompileRequestFromFile(file)
}

// CompileRequestFromFile lowers an AST File into a full semantic.CompileRequest.
func CompileRequestFromFile(file *File) (semantic.CompileRequest, error) {
	var entities []semantic.EntityDeclaration
	var relations []semantic.RelationDeclaration
	var transformations []semantic.TransformationDeclaration
	var checkpoints []semantic.CheckpointDeclaration
	var profiles []semantic.ProfileDeclaration

	for _, decl := range file.Declarations {
		switch d := decl.(type) {
		case *SchemaDecl:
			for _, ent := range d.Entities {
				entityDecl, err := lowerEntityDecl(ent)
				if err != nil {
					return semantic.CompileRequest{}, err
				}
				entities = append(entities, entityDecl)
			}
			for _, rel := range d.Relations {
				relDecl := semantic.RelationDeclaration{
					Kind:     semantic.RelationKind(rel.Kind),
					FromKind: semantic.EntityKind(rel.FromKind),
					ToKind:   semantic.EntityKind(rel.ToKind),
				}
				relations = append(relations, relDecl)
			}
		case *RuleDecl:
			transDecl, err := lowerRuleDecl(d)
			if err != nil {
				return semantic.CompileRequest{}, err
			}
			transformations = append(transformations, transDecl)
		case *CheckpointDecl:
			checkpoints = append(checkpoints, semantic.CheckpointDeclaration{
				Key:   semantic.CheckpointKey(d.Key),
				After: semantic.RuleID(d.After),
			})
		case *ProfileDecl:
			profDecl, err := lowerProfileDecl(d)
			if err != nil {
				return semantic.CompileRequest{}, err
			}
			profiles = append(profiles, profDecl)
		}
	}

	schema, err := semantic.NewSchema(entities, relations)
	if err != nil {
		return semantic.CompileRequest{}, fmt.Errorf("schema error: %w", err)
	}

	return semantic.CompileRequest{
		Schema: schema.Declaration(),
		Rules: semantic.RulesetDeclaration{
			Transformations: transformations,
			Checkpoints:     checkpoints,
		},
		Profiles:                 profiles,
		CompilerSemanticsVersion: "semantics.v1",
	}, nil
}

func lowerProfileDecl(p *ProfileDecl) (semantic.ProfileDeclaration, error) {
	reqs := make([]semantic.RequirementAtom, 0, len(p.Requirements))
	for _, r := range p.Requirements {
		reqs = append(reqs, semantic.RequirementAtom{
			Code:  semantic.RequirementCode(r.Code),
			Kind:  semantic.FieldPresent,
			Field: semantic.FieldPath(r.Field),
		})
	}
	implies := make([]semantic.ProfileKey, len(p.Implies))
	for i, imp := range p.Implies {
		implies[i] = semantic.ProfileKey(imp)
	}
	return semantic.ProfileDeclaration{
		Key: semantic.ProfileKey(p.Key),
		Scope: semantic.ProfileScope{
			Kind:       semantic.AllEntitiesOfKind,
			EntityKind: semantic.EntityKind(p.EntityKind),
		},
		Aggregation:  semantic.AllSelected,
		Requirements: reqs,
		Implies:      implies,
	}, nil
}

func lowerEntityDecl(ent *EntityDecl) (semantic.EntityDeclaration, error) {
	fields := make([]semantic.FieldDeclaration, 0, len(ent.Fields))
	for _, f := range ent.Fields {
		fieldDecl, err := lowerFieldDecl(f)
		if err != nil {
			return semantic.EntityDeclaration{}, err
		}
		fields = append(fields, fieldDecl)
	}
	return semantic.EntityDeclaration{
		Kind:   semantic.EntityKind(ent.Kind),
		Fields: fields,
	}, nil
}

func lowerFieldDecl(field *FieldDecl) (semantic.FieldDeclaration, error) {
	var valType semantic.ValueKind
	switch strings.ToLower(field.Type) {
	case "string":
		valType = semantic.ValueString
	case "int64", "int":
		valType = semantic.ValueInt64
	case "decimal", "dec":
		valType = semantic.ValueDecimal
	case "timestamp", "ts":
		valType = semantic.ValueTimestamp
	case "date":
		valType = semantic.ValueDate
	case "duration", "dur":
		valType = semantic.ValueDuration
	case "atom":
		valType = semantic.ValueAtom
	default:
		return semantic.FieldDeclaration{}, fmt.Errorf("%s: unknown field type %q", field.Pos, field.Type)
	}

	return semantic.FieldDeclaration{
		Name:                   semantic.FieldName(field.Name),
		Kind:                   valType,
		RequiredAtConstruction: field.Required,
	}, nil
}

func lowerRuleDecl(rule *RuleDecl) (semantic.TransformationDeclaration, error) {
	decl := semantic.TransformationDeclaration{
		ID: semantic.RuleID(rule.ID),
	}
	for _, dep := range rule.DependsOn {
		decl.After = append(decl.After, semantic.RuleID(dep))
	}

	switch op := rule.Operator.(type) {
	case *SelectAndAssignNode:
		decl.Operator = semantic.OperatorSelectAndAssign
		sa, err := lowerSelectAndAssign(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.SelectAssign = sa

	case *InsertEntityNode:
		decl.Operator = semantic.OperatorInsertEntity
		ins, err := lowerInsertEntity(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.InsertEntity = ins

	case *DeleteEntityNode:
		decl.Operator = semantic.OperatorDeleteEntity
		del, err := lowerDeleteEntity(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.DeleteEntity = del

	case *RelateEntitiesNode:
		decl.Operator = semantic.OperatorRelateEntities
		rel, err := lowerRelateEntities(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.RelateEntities = rel

	case *UnrelateEntitiesNode:
		decl.Operator = semantic.OperatorUnrelateEntities
		unrel, err := lowerUnrelateEntities(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.UnrelateEntities = unrel

	case *MergeEntitiesNode:
		decl.Operator = semantic.OperatorMergeEntities
		mrg, err := lowerMergeEntities(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.MergeEntities = mrg

	case *SplitEntityNode:
		decl.Operator = semantic.OperatorSplitEntity
		splt, err := lowerSplitEntity(op)
		if err != nil {
			return semantic.TransformationDeclaration{}, err
		}
		decl.SplitEntity = splt

	default:
		return semantic.TransformationDeclaration{}, fmt.Errorf("%s: unsupported operator in rule %s", rule.Pos, rule.ID)
	}

	// Auto-derive DeclaredReads if omitted
	if len(rule.DeclaredReads) > 0 {
		for _, r := range rule.DeclaredReads {
			decl.DeclaredReads = append(decl.DeclaredReads, semantic.FieldPath(r))
		}
	} else {
		decl.DeclaredReads = deriveRuleReads(&decl)
	}

	// Auto-derive DeclaredWrites if omitted
	if len(rule.DeclaredWrites) > 0 {
		for _, w := range rule.DeclaredWrites {
			decl.DeclaredWrites = append(decl.DeclaredWrites, semantic.FieldPath(w))
		}
	} else {
		decl.DeclaredWrites = deriveRuleWrites(&decl)
	}

	return decl, nil
}

func lowerSelector(node *SelectorNode) (semantic.Selector, error) {
	if node == nil {
		return semantic.Selector{}, fmt.Errorf("nil selector node")
	}

	cardinality := semantic.Cardinality{Kind: semantic.CardinalityAny}
	if node.CardinalityKind == "exactly" {
		cardinality = semantic.Cardinality{Kind: semantic.CardinalityExactly, Count: node.CardinalityCount}
	} else if node.CardinalityKind == "at_least" {
		cardinality = semantic.Cardinality{Kind: semantic.CardinalityAtLeast, Count: node.CardinalityCount}
	}

	var whereExpr *semantic.Expr
	if node.Where != nil {
		w, err := lowerExpr(node.Where)
		if err != nil {
			return semantic.Selector{}, err
		}
		whereExpr = &w
	}

	var groupByExpr *semantic.Expr
	if node.GroupBy != nil {
		g, err := lowerExpr(node.GroupBy)
		if err != nil {
			return semantic.Selector{}, err
		}
		groupByExpr = &g
	}

	return semantic.Selector{
		Kind:    semantic.EntityKind(node.Kind),
		Where:   whereExpr,
		GroupBy: groupByExpr,
		Members: cardinality,
	}, nil
}

func lowerAssignments(nodes []*AssignmentNode) ([]semantic.FieldAssignment, error) {
	var assigns []semantic.FieldAssignment
	for _, a := range nodes {
		valExpr, err := lowerExpr(a.Value)
		if err != nil {
			return nil, err
		}
		assigns = append(assigns, semantic.FieldAssignment{
			Target: semantic.FieldPath(a.Target),
			Value:  valExpr,
		})
	}
	return assigns, nil
}

func defaultGroupGuard() semantic.Expr {
	return semantic.Expr{
		Kind: semantic.ExprAllMembers,
		Args: []semantic.Expr{
			{
				Kind: semantic.ExprEqual,
				Args: []semantic.Expr{
					{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
					{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
				},
			},
		},
	}
}

func defaultRelationGuard() semantic.Expr {
	return semantic.Expr{
		Kind: semantic.ExprEqual,
		Args: []semantic.Expr{
			{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
			{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
		},
	}
}

func intLiteralVal(n int64) *semantic.Value {
	v := semantic.NewInt64Value(n)
	return &v
}

func lowerSelectAndAssign(node *SelectAndAssignNode) (*semantic.SelectAssignDeclaration, error) {
	assigns, err := lowerAssignments(node.Assignments)
	if err != nil {
		return nil, err
	}

	// If group_by was omitted, group by where field or first assignment target
	if node.Selector.GroupBy == nil {
		if node.Selector.Where != nil {
			wherePaths := collectASTFieldPaths(node.Selector.Where)
			if len(wherePaths) > 0 {
				node.Selector.GroupBy = &IdentExpr{Pos: node.Pos, Path: wherePaths[0]}
			}
		}
		if node.Selector.GroupBy == nil && len(assigns) > 0 {
			firstTarget := string(assigns[0].Target)
			node.Selector.GroupBy = &IdentExpr{Pos: node.Pos, Path: firstTarget}
		}
		if node.Selector.CardinalityKind == "any" {
			node.Selector.CardinalityKind = "at_least"
			node.Selector.CardinalityCount = 1
		}
	}

	sel, err := lowerSelector(node.Selector)
	if err != nil {
		return nil, err
	}

	decl := &semantic.SelectAssignDeclaration{
		Selector:    sel,
		Assignments: assigns,
		Guard:       defaultGroupGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerInsertEntity(node *InsertEntityNode) (*semantic.InsertEntityDeclaration, error) {
	// If group_by was omitted in insert selector, default to grouping
	if node.Selector != nil && node.Selector.GroupBy == nil {
		node.Selector.GroupBy = &IdentExpr{Pos: node.Pos, Path: node.Selector.Kind + ".id"}
		if node.Selector.CardinalityKind == "any" {
			node.Selector.CardinalityKind = "at_least"
			node.Selector.CardinalityCount = 1
		}
	}

	sel, err := lowerSelector(node.Selector)
	if err != nil {
		return nil, err
	}
	assigns, err := lowerAssignments(node.Assignments)
	if err != nil {
		return nil, err
	}
	disc, err := lowerExpr(node.Discriminator)
	if err != nil {
		return nil, err
	}
	decl := &semantic.InsertEntityDeclaration{
		Selector:      sel,
		TargetKind:    semantic.EntityKind(node.TargetKind),
		Discriminator: disc,
		Assignments:   assigns,
		Guard:         defaultGroupGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerDeleteEntity(node *DeleteEntityNode) (*semantic.DeleteEntityDeclaration, error) {
	sel, err := lowerSelector(node.Selector)
	if err != nil {
		return nil, err
	}
	decl := &semantic.DeleteEntityDeclaration{
		Selector: sel,
		Guard:    defaultGroupGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerRelateEntities(node *RelateEntitiesNode) (*semantic.RelateEntitiesDeclaration, error) {
	fromSel, err := lowerSelector(node.FromSelector)
	if err != nil {
		return nil, err
	}
	toSel, err := lowerSelector(node.ToSelector)
	if err != nil {
		return nil, err
	}
	decl := &semantic.RelateEntitiesDeclaration{
		RelationKind: semantic.RelationKind(node.RelationKind),
		FromSelector: fromSel,
		ToSelector:   toSel,
		Guard:        defaultRelationGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerUnrelateEntities(node *UnrelateEntitiesNode) (*semantic.UnrelateEntitiesDeclaration, error) {
	fromSel, err := lowerSelector(node.FromSelector)
	if err != nil {
		return nil, err
	}
	toSel, err := lowerSelector(node.ToSelector)
	if err != nil {
		return nil, err
	}
	decl := &semantic.UnrelateEntitiesDeclaration{
		RelationKind: semantic.RelationKind(node.RelationKind),
		FromSelector: fromSel,
		ToSelector:   toSel,
		Guard:        defaultRelationGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerMergeEntities(node *MergeEntitiesNode) (*semantic.MergeEntitiesDeclaration, error) {
	sel, err := lowerSelector(node.Selector)
	if err != nil {
		return nil, err
	}
	assigns, err := lowerAssignments(node.Assignments)
	if err != nil {
		return nil, err
	}
	disc, err := lowerExpr(node.Discriminator)
	if err != nil {
		return nil, err
	}
	decl := &semantic.MergeEntitiesDeclaration{
		Selector:          sel,
		TargetKind:        semantic.EntityKind(node.TargetKind),
		Discriminator:     disc,
		RetainSources:     node.RetainSources,
		ReanchorRelations: node.ReanchorRelations,
		Assignments:       assigns,
		Guard:             defaultGroupGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerSplitEntity(node *SplitEntityNode) (*semantic.SplitEntityDeclaration, error) {
	sel, err := lowerSelector(node.Selector)
	if err != nil {
		return nil, err
	}
	partitions := make([]semantic.PartitionDeclaration, 0, len(node.Partitions))
	for _, p := range node.Partitions {
		disc, err := lowerExpr(p.Discriminator)
		if err != nil {
			return nil, err
		}
		assigns, err := lowerAssignments(p.Assignments)
		if err != nil {
			return nil, err
		}
		partitions = append(partitions, semantic.PartitionDeclaration{
			Discriminator: disc,
			Assignments:   assigns,
		})
	}
	decl := &semantic.SplitEntityDeclaration{
		Selector:     sel,
		TargetKind:   semantic.EntityKind(node.TargetKind),
		RetainSource: node.RetainSource,
		Partitions:   partitions,
		Guard:        defaultGroupGuard(),
	}
	if node.Guard != nil {
		guard, err := lowerExpr(node.Guard)
		if err != nil {
			return nil, err
		}
		decl.Guard = guard
	}
	return decl, nil
}

func lowerExpr(node ExprNode) (semantic.Expr, error) {
	if node == nil {
		return semantic.Expr{}, fmt.Errorf("nil expression node")
	}

	switch e := node.(type) {
	case *IdentExpr:
		return semantic.Expr{
			Kind:  semantic.ExprField,
			Field: semantic.FieldPath(e.Path),
		}, nil

	case *LiteralExpr:
		if e.Kind == LitBool {
			if e.Literal == "true" {
				return semantic.Expr{
					Kind: semantic.ExprEqual,
					Args: []semantic.Expr{
						{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
						{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
					},
				}, nil
			}
			return semantic.Expr{
				Kind: semantic.ExprEqual,
				Args: []semantic.Expr{
					{Kind: semantic.ExprLiteral, Literal: intLiteralVal(1)},
					{Kind: semantic.ExprLiteral, Literal: intLiteralVal(0)},
				},
			}, nil
		}
		val, err := lowerLiteral(e)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{
			Kind:    semantic.ExprLiteral,
			Literal: &val,
		}, nil

	case *UnaryExpr:
		switch e.Op {
		case TokenNot:
			right, err := lowerExpr(e.Right)
			if err != nil {
				return semantic.Expr{}, err
			}
			return semantic.Expr{Kind: semantic.ExprNot, Args: []semantic.Expr{right}}, nil
		case TokenMinus:
			if lit, ok := e.Right.(*LiteralExpr); ok {
				switch lit.Kind {
				case LitInt:
					n, err := strconv.ParseInt(lit.Literal, 10, 64)
					if err != nil {
						return semantic.Expr{}, fmt.Errorf("%s: invalid integer literal %q: %w", lit.Pos, lit.Literal, err)
					}
					val := semantic.NewInt64Value(-n)
					return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &val}, nil
				case LitDecimal:
					s := lit.Literal
					if strings.HasPrefix(s, "-") {
						s = strings.TrimPrefix(s, "-")
					} else {
						s = "-" + s
					}
					val, err := semantic.NewDecimalValue(s)
					if err != nil {
						return semantic.Expr{}, fmt.Errorf("%s: invalid decimal literal %q: %w", lit.Pos, lit.Literal, err)
					}
					return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &val}, nil
				case LitDuration:
					d, err := parseDurationLiteral(lit.Literal)
					if err != nil {
						return semantic.Expr{}, fmt.Errorf("%s: invalid duration literal %q: %w", lit.Pos, lit.Literal, err)
					}
					val := semantic.NewDurationValue(-d)
					return semantic.Expr{Kind: semantic.ExprLiteral, Literal: &val}, nil
				}
			}
			right, err := lowerExpr(e.Right)
			if err != nil {
				return semantic.Expr{}, err
			}
			return semantic.Expr{Kind: semantic.ExprSubtract, Args: []semantic.Expr{semantic.Expr{Kind: semantic.ExprLiteral, Literal: intLiteralVal(0)}, right}}, nil
		default:
			return semantic.Expr{}, fmt.Errorf("%s: unknown unary operator %v", e.Pos, e.Op)
		}

	case *BinaryExpr:
		left, err := lowerExpr(e.Left)
		if err != nil {
			return semantic.Expr{}, err
		}
		right, err := lowerExpr(e.Right)
		if err != nil {
			return semantic.Expr{}, err
		}
		switch e.Op {
		case TokenEqual:
			return semantic.Expr{Kind: semantic.ExprEqual, Args: []semantic.Expr{left, right}}, nil
		case TokenNotEqual:
			return semantic.Expr{Kind: semantic.ExprNot, Args: []semantic.Expr{{Kind: semantic.ExprEqual, Args: []semantic.Expr{left, right}}}}, nil
		case TokenLess:
			return semantic.Expr{Kind: semantic.ExprLess, Args: []semantic.Expr{left, right}}, nil
		case TokenGreater:
			return semantic.Expr{Kind: semantic.ExprLess, Args: []semantic.Expr{right, left}}, nil
		case TokenLessEq:
			return semantic.Expr{Kind: semantic.ExprNot, Args: []semantic.Expr{{Kind: semantic.ExprLess, Args: []semantic.Expr{right, left}}}}, nil
		case TokenGreatEq:
			return semantic.Expr{Kind: semantic.ExprNot, Args: []semantic.Expr{{Kind: semantic.ExprLess, Args: []semantic.Expr{left, right}}}}, nil
		case TokenAnd:
			return semantic.Expr{Kind: semantic.ExprAll, Args: []semantic.Expr{left, right}}, nil
		case TokenOr:
			return semantic.Expr{Kind: semantic.ExprAny, Args: []semantic.Expr{left, right}}, nil
		case TokenPlus:
			return semantic.Expr{Kind: semantic.ExprAdd, Args: []semantic.Expr{left, right}}, nil
		case TokenMinus:
			return semantic.Expr{Kind: semantic.ExprSubtract, Args: []semantic.Expr{left, right}}, nil
		case TokenStar:
			return semantic.Expr{Kind: semantic.ExprMultiply, Args: []semantic.Expr{left, right}}, nil
		case TokenSlash:
			return semantic.Expr{Kind: semantic.ExprDivide, Args: []semantic.Expr{left, right}}, nil
		case TokenPercent:
			return semantic.Expr{Kind: semantic.ExprModulo, Args: []semantic.Expr{left, right}}, nil
		default:
			return semantic.Expr{}, fmt.Errorf("%s: unknown binary operator %v", e.Pos, e.Op)
		}

	case *CallExpr:
		return lowerCallExpr(e)

	default:
		return semantic.Expr{}, fmt.Errorf("%s: unknown expression type %T", node.Position(), node)
	}
}

func lowerLiteral(lit *LiteralExpr) (semantic.Value, error) {
	switch lit.Kind {
	case LitString:
		return semantic.NewStringValue(lit.Literal)
	case LitInt:
		n, err := strconv.ParseInt(lit.Literal, 10, 64)
		if err != nil {
			return semantic.Value{}, fmt.Errorf("%s: invalid integer literal %q: %w", lit.Pos, lit.Literal, err)
		}
		return semantic.NewInt64Value(n), nil
	case LitDecimal:
		return semantic.NewDecimalValue(lit.Literal)
	case LitTimestamp:
		return parseTimestampLiteral(lit.Literal)
	case LitDate:
		return parseDateLiteral(lit.Literal)
	case LitDuration:
		durSec, err := parseDurationLiteral(lit.Literal)
		if err != nil {
			return semantic.Value{}, fmt.Errorf("%s: invalid duration literal: %w", lit.Pos, err)
		}
		return semantic.NewDurationValue(durSec), nil
	case LitAtom:
		return semantic.NewAtomValue(lit.Literal)
	default:
		return semantic.Value{}, fmt.Errorf("%s: unknown literal kind %q", lit.Pos, lit.Kind)
	}
}

func parseTimestampLiteral(s string) (semantic.Value, error) {
	return semantic.NewTimestampValue(s)
}

func parseDateLiteral(s string) (semantic.Value, error) {
	return semantic.NewDateValue(s)
}

func parseDurationLiteral(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimPrefix(s, "-")
	}
	if strings.HasSuffix(s, "h") {
		h, err := strconv.ParseInt(strings.TrimSuffix(s, "h"), 10, 64)
		if err != nil {
			return 0, err
		}
		dur := h * 3600
		if neg {
			dur = -dur
		}
		return dur, nil
	}
	if strings.HasSuffix(s, "m") {
		m, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		if err != nil {
			return 0, err
		}
		dur := m * 60
		if neg {
			dur = -dur
		}
		return dur, nil
	}
	if strings.HasSuffix(s, "s") {
		sec, err := strconv.ParseInt(strings.TrimSuffix(s, "s"), 10, 64)
		if err != nil {
			return 0, err
		}
		if neg {
			sec = -sec
		}
		return sec, nil
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if neg {
		sec = -sec
	}
	return sec, nil
}

func lowerCallExpr(call *CallExpr) (semantic.Expr, error) {
	name := strings.ToLower(call.FuncName)
	switch name {
	case "count":
		if len(call.Args) > 0 {
			return semantic.Expr{}, fmt.Errorf("%s: count() takes 0 arguments", call.Pos)
		}
		return semantic.Expr{Kind: semantic.ExprCount}, nil

	case "sum", "min", "max", "all_equal", "exists":
		if len(call.Args) != 1 {
			return semantic.Expr{}, fmt.Errorf("%s: %s() requires exactly 1 argument (field)", call.Pos, name)
		}
		ident, ok := call.Args[0].(*IdentExpr)
		if !ok {
			return semantic.Expr{}, fmt.Errorf("%s: %s() argument must be a field path", call.Pos, name)
		}
		var kind semantic.ExprKind
		switch name {
		case "sum":
			kind = semantic.ExprSum
		case "min":
			kind = semantic.ExprMin
		case "max":
			kind = semantic.ExprMax
		case "all_equal":
			kind = semantic.ExprAllEqual
		case "exists":
			kind = semantic.ExprExists
		}
		return semantic.Expr{
			Kind:  kind,
			Field: semantic.FieldPath(ident.Path),
		}, nil

	case "all":
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		if len(args) == 1 {
			return semantic.Expr{Kind: semantic.ExprAllMembers, Args: args}, nil
		}
		return semantic.Expr{Kind: semantic.ExprAll, Args: args}, nil

	case "any":
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		if len(args) == 1 {
			return semantic.Expr{Kind: semantic.ExprAnyMembers, Args: args}, nil
		}
		return semantic.Expr{Kind: semantic.ExprAny, Args: args}, nil

	case "coalesce":
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprCoalesce, Args: args}, nil

	case "if":
		if len(call.Args) != 3 {
			return semantic.Expr{}, fmt.Errorf("%s: if() requires exactly 3 arguments (cond, trueVal, falseVal)", call.Pos)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprIf, Args: args}, nil

	case "concat":
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprConcat, Args: args}, nil

	case "substring":
		if len(call.Args) != 3 {
			return semantic.Expr{}, fmt.Errorf("%s: substring() requires exactly 3 arguments (str, start, len)", call.Pos)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprSubstring, Args: args}, nil

	case "trim":
		if len(call.Args) != 1 {
			return semantic.Expr{}, fmt.Errorf("%s: trim() requires 1 argument", call.Pos)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprTrim, Args: args}, nil

	case "abs":
		if len(call.Args) != 1 {
			return semantic.Expr{}, fmt.Errorf("%s: abs() requires 1 argument", call.Pos)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprAbs, Args: args}, nil

	case "clamp":
		if len(call.Args) != 3 {
			return semantic.Expr{}, fmt.Errorf("%s: clamp() requires 3 arguments (val, min, max)", call.Pos)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprClamp, Args: args}, nil

	case "date_add", "timestamp_add":
		if len(call.Args) != 2 {
			return semantic.Expr{}, fmt.Errorf("%s: %s() requires 2 arguments (ts, dur)", call.Pos, call.FuncName)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprTimestampAdd, Args: args}, nil

	case "date_diff", "timestamp_diff":
		if len(call.Args) != 2 {
			return semantic.Expr{}, fmt.Errorf("%s: %s() requires 2 arguments (ts1, ts2)", call.Pos, call.FuncName)
		}
		args, err := lowerExprSlice(call.Args)
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{Kind: semantic.ExprTimestampDiff, Args: args}, nil

	case "extract":
		if len(call.Args) != 2 {
			return semantic.Expr{}, fmt.Errorf("%s: extract() requires 2 arguments (unit, ts)", call.Pos)
		}
		var unit string
		if ident, ok := call.Args[0].(*IdentExpr); ok {
			unit = ident.Path
		} else if lit, ok := call.Args[0].(*LiteralExpr); ok && lit.Kind == LitString {
			unit = lit.Literal
		} else {
			return semantic.Expr{}, fmt.Errorf("%s: extract() first argument must be a unit name (e.g. \"year\" or year)", call.Pos)
		}
		tsExpr, err := lowerExpr(call.Args[1])
		if err != nil {
			return semantic.Expr{}, err
		}
		return semantic.Expr{
			Kind:  semantic.ExprDateExtract,
			Field: semantic.FieldPath(unit),
			Args:  []semantic.Expr{tsExpr},
		}, nil

	default:
		return semantic.Expr{}, fmt.Errorf("%s: unknown function call %q", call.Pos, call.FuncName)
	}
}

func lowerExprSlice(nodes []ExprNode) ([]semantic.Expr, error) {
	res := make([]semantic.Expr, 0, len(nodes))
	for _, n := range nodes {
		e, err := lowerExpr(n)
		if err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	return res, nil
}

// Auto-derive reads and writes from AST
func deriveRuleReads(decl *semantic.TransformationDeclaration) []semantic.FieldPath {
	seen := make(map[semantic.FieldPath]struct{})
	addPaths := func(paths []semantic.FieldPath) {
		for _, p := range paths {
			seen[p] = struct{}{}
		}
	}
	collectExprPaths := func(e semantic.Expr) {
		addPaths(collectFieldPaths(e))
	}
	collectRelationGuardPaths := func(e semantic.Expr, fromKind, toKind semantic.EntityKind) {
		for _, p := range collectFieldPaths(e) {
			s := string(p)
			if strings.HasPrefix(s, "from.") {
				field := strings.TrimPrefix(s, "from.")
				seen[semantic.FieldPath(string(fromKind)+"."+field)] = struct{}{}
			} else if strings.HasPrefix(s, "to.") {
				field := strings.TrimPrefix(s, "to.")
				seen[semantic.FieldPath(string(toKind)+"."+field)] = struct{}{}
			} else {
				seen[p] = struct{}{}
			}
		}
	}

	switch decl.Operator {
	case semantic.OperatorSelectAndAssign:
		if decl.SelectAssign.Selector.Where != nil {
			collectExprPaths(*decl.SelectAssign.Selector.Where)
		}
		if decl.SelectAssign.Selector.GroupBy != nil {
			collectExprPaths(*decl.SelectAssign.Selector.GroupBy)
		}
		collectExprPaths(decl.SelectAssign.Guard)
		for _, a := range decl.SelectAssign.Assignments {
			collectExprPaths(a.Value)
		}

	case semantic.OperatorInsertEntity:
		if decl.InsertEntity.Selector.Where != nil {
			collectExprPaths(*decl.InsertEntity.Selector.Where)
		}
		if decl.InsertEntity.Selector.GroupBy != nil {
			collectExprPaths(*decl.InsertEntity.Selector.GroupBy)
		}
		collectExprPaths(decl.InsertEntity.Guard)
		collectExprPaths(decl.InsertEntity.Discriminator)
		for _, a := range decl.InsertEntity.Assignments {
			collectExprPaths(a.Value)
		}

	case semantic.OperatorDeleteEntity:
		if decl.DeleteEntity.Selector.Where != nil {
			collectExprPaths(*decl.DeleteEntity.Selector.Where)
		}
		if decl.DeleteEntity.Selector.GroupBy != nil {
			collectExprPaths(*decl.DeleteEntity.Selector.GroupBy)
		}
		collectExprPaths(decl.DeleteEntity.Guard)

	case semantic.OperatorRelateEntities:
		if decl.RelateEntities.FromSelector.Where != nil {
			collectExprPaths(*decl.RelateEntities.FromSelector.Where)
		}
		if decl.RelateEntities.FromSelector.GroupBy != nil {
			collectExprPaths(*decl.RelateEntities.FromSelector.GroupBy)
		}
		if decl.RelateEntities.ToSelector.Where != nil {
			collectExprPaths(*decl.RelateEntities.ToSelector.Where)
		}
		if decl.RelateEntities.ToSelector.GroupBy != nil {
			collectExprPaths(*decl.RelateEntities.ToSelector.GroupBy)
		}
		collectRelationGuardPaths(decl.RelateEntities.Guard, decl.RelateEntities.FromSelector.Kind, decl.RelateEntities.ToSelector.Kind)

	case semantic.OperatorUnrelateEntities:
		if decl.UnrelateEntities.FromSelector.Where != nil {
			collectExprPaths(*decl.UnrelateEntities.FromSelector.Where)
		}
		if decl.UnrelateEntities.FromSelector.GroupBy != nil {
			collectExprPaths(*decl.UnrelateEntities.FromSelector.GroupBy)
		}
		if decl.UnrelateEntities.ToSelector.Where != nil {
			collectExprPaths(*decl.UnrelateEntities.ToSelector.Where)
		}
		if decl.UnrelateEntities.ToSelector.GroupBy != nil {
			collectExprPaths(*decl.UnrelateEntities.ToSelector.GroupBy)
		}
		collectRelationGuardPaths(decl.UnrelateEntities.Guard, decl.UnrelateEntities.FromSelector.Kind, decl.UnrelateEntities.ToSelector.Kind)

	case semantic.OperatorMergeEntities:
		if decl.MergeEntities.Selector.Where != nil {
			collectExprPaths(*decl.MergeEntities.Selector.Where)
		}
		if decl.MergeEntities.Selector.GroupBy != nil {
			collectExprPaths(*decl.MergeEntities.Selector.GroupBy)
		}
		collectExprPaths(decl.MergeEntities.Guard)
		collectExprPaths(decl.MergeEntities.Discriminator)
		for _, a := range decl.MergeEntities.Assignments {
			collectExprPaths(a.Value)
		}

	case semantic.OperatorSplitEntity:
		if decl.SplitEntity.Selector.Where != nil {
			collectExprPaths(*decl.SplitEntity.Selector.Where)
		}
		if decl.SplitEntity.Selector.GroupBy != nil {
			collectExprPaths(*decl.SplitEntity.Selector.GroupBy)
		}
		collectExprPaths(decl.SplitEntity.Guard)
		for _, p := range decl.SplitEntity.Partitions {
			collectExprPaths(p.Discriminator)
			for _, a := range p.Assignments {
				collectExprPaths(a.Value)
			}
		}
	}

	var res []semantic.FieldPath
	for p := range seen {
		res = append(res, p)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i] < res[j]
	})
	return res
}

func deriveRuleWrites(decl *semantic.TransformationDeclaration) []semantic.FieldPath {
	seen := make(map[semantic.FieldPath]struct{})
	switch decl.Operator {
	case semantic.OperatorSelectAndAssign:
		for _, a := range decl.SelectAssign.Assignments {
			seen[a.Target] = struct{}{}
		}
	case semantic.OperatorInsertEntity:
		for _, a := range decl.InsertEntity.Assignments {
			seen[a.Target] = struct{}{}
		}
	case semantic.OperatorMergeEntities:
		for _, a := range decl.MergeEntities.Assignments {
			seen[a.Target] = struct{}{}
		}
	case semantic.OperatorSplitEntity:
		for _, p := range decl.SplitEntity.Partitions {
			for _, a := range p.Assignments {
				seen[a.Target] = struct{}{}
			}
		}
	}

	var res []semantic.FieldPath
	for p := range seen {
		res = append(res, p)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i] < res[j]
	})
	return res
}

func collectFieldPaths(e semantic.Expr) []semantic.FieldPath {
	var paths []semantic.FieldPath
	if e.Field != "" && e.Kind != semantic.ExprDateExtract {
		paths = append(paths, e.Field)
	}
	for _, arg := range e.Args {
		paths = append(paths, collectFieldPaths(arg)...)
	}
	return paths
}

func collectASTFieldPaths(node ExprNode) []string {
	if node == nil {
		return nil
	}
	switch e := node.(type) {
	case *IdentExpr:
		return []string{e.Path}
	case *UnaryExpr:
		return collectASTFieldPaths(e.Right)
	case *BinaryExpr:
		return append(collectASTFieldPaths(e.Left), collectASTFieldPaths(e.Right)...)
	case *CallExpr:
		var res []string
		for _, arg := range e.Args {
			res = append(res, collectASTFieldPaths(arg)...)
		}
		return res
	default:
		return nil
	}
}

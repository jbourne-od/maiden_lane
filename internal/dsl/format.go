package dsl

import (
	"fmt"
	"strings"
)

// FormatSource parses and pretty-prints .ml source text in canonical format.
func FormatSource(src string) (string, error) {
	l := NewLexer(src)
	p := NewParser(l)
	file, err := p.ParseFile()
	if err != nil {
		return "", err
	}
	return Format(file), nil
}

// Format returns canonical formatted text for a parsed File.
func Format(file *File) string {
	var sb strings.Builder
	for i, decl := range file.Declarations {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		formatDecl(&sb, decl)
	}
	sb.WriteString("\n")
	return sb.String()
}

func formatDecl(sb *strings.Builder, decl Declaration) {
	switch d := decl.(type) {
	case *SchemaDecl:
		formatSchemaDecl(sb, d)
	case *RuleDecl:
		formatRuleDecl(sb, d)
	case *CheckpointDecl:
		formatCheckpointDecl(sb, d)
	case *ProfileDecl:
		formatProfileDecl(sb, d)
	}
}

func formatCheckpointDecl(sb *strings.Builder, c *CheckpointDecl) {
	sb.WriteString(fmt.Sprintf("checkpoint %s after %s;", c.Key, c.After))
}

func formatProfileDecl(sb *strings.Builder, p *ProfileDecl) {
	sb.WriteString(fmt.Sprintf("profile %s for entity %s", p.Key, p.EntityKind))
	if len(p.Implies) > 0 {
		sb.WriteString(fmt.Sprintf(" (implies: [%s])", formatStringList(p.Implies)))
	}
	sb.WriteString(" {\n")
	for _, r := range p.Requirements {
		sb.WriteString(fmt.Sprintf("  require %s present as %s;\n", r.Field, r.Code))
	}
	sb.WriteString("}")
}

func formatSchemaDecl(sb *strings.Builder, d *SchemaDecl) {
	sb.WriteString("schema {\n")
	for _, ent := range d.Entities {
		sb.WriteString(fmt.Sprintf("  entity %s {\n", ent.Kind))
		for _, f := range ent.Fields {
			reqStr := ""
			if !f.Required {
				reqStr = " optional"
			}
			sb.WriteString(fmt.Sprintf("    %s: %s%s;\n", f.Name, f.Type, reqStr))
		}
		sb.WriteString("  }\n")
	}
	for _, rel := range d.Relations {
		sb.WriteString(fmt.Sprintf("  relation %s {\n", rel.Kind))
		sb.WriteString(fmt.Sprintf("    from: %s;\n", rel.FromKind))
		sb.WriteString(fmt.Sprintf("    to: %s;\n", rel.ToKind))
		sb.WriteString("  }\n")
	}
	sb.WriteString("}")
}

func formatRuleDecl(sb *strings.Builder, r *RuleDecl) {
	sb.WriteString(fmt.Sprintf("rule %s", r.ID))
	var annotations []string
	if len(r.DeclaredReads) > 0 {
		annotations = append(annotations, fmt.Sprintf("reads: [%s]", formatStringList(r.DeclaredReads)))
	}
	if len(r.DeclaredWrites) > 0 {
		annotations = append(annotations, fmt.Sprintf("writes: [%s]", formatStringList(r.DeclaredWrites)))
	}
	if len(r.DependsOn) > 0 {
		annotations = append(annotations, fmt.Sprintf("depends_on: [%s]", formatStringList(r.DependsOn)))
	}
	if len(annotations) > 0 {
		sb.WriteString(fmt.Sprintf(" (%s)", strings.Join(annotations, ", ")))
	}
	sb.WriteString(" {\n")
	formatOperator(sb, r.Operator, "  ")
	sb.WriteString("\n}")
}

func formatStringList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return strings.Join(quoted, ", ")
}

func formatOperator(sb *strings.Builder, op OperatorNode, indent string) {
	switch o := op.(type) {
	case *SelectAndAssignNode:
		formatSelectAndAssign(sb, o, indent)
	case *InsertEntityNode:
		formatInsertEntity(sb, o, indent)
	case *DeleteEntityNode:
		formatDeleteEntity(sb, o, indent)
	case *RelateEntitiesNode:
		formatRelateEntities(sb, o, indent)
	case *UnrelateEntitiesNode:
		formatUnrelateEntities(sb, o, indent)
	case *MergeEntitiesNode:
		formatMergeEntities(sb, o, indent)
	case *SplitEntityNode:
		formatSplitEntity(sb, o, indent)
	}
}

func formatSelector(sb *strings.Builder, s *SelectorNode, indent string) {
	sb.WriteString(fmt.Sprintf("%sselect %s\n", indent, s.Kind))
	if s.Where != nil {
		sb.WriteString(fmt.Sprintf("%swhere %s\n", indent, FormatExpr(s.Where)))
	}
	if s.GroupBy != nil {
		sb.WriteString(fmt.Sprintf("%sgroup_by %s\n", indent, FormatExpr(s.GroupBy)))
	}
	if s.Having != nil {
		sb.WriteString(fmt.Sprintf("%shaving %s\n", indent, FormatExpr(s.Having)))
	}
}

func formatAssignments(sb *strings.Builder, assigns []*AssignmentNode, indent string) {
	sb.WriteString(fmt.Sprintf("%sset ", indent))
	for i, a := range assigns {
		if i > 0 {
			sb.WriteString(fmt.Sprintf(",\n%s    ", indent))
		}
		sb.WriteString(fmt.Sprintf("%s = %s", a.Target, FormatExpr(a.Value)))
	}
	sb.WriteString(";")
}

func formatSelectAndAssign(sb *strings.Builder, node *SelectAndAssignNode, indent string) {
	formatSelector(sb, node.Selector, indent)
	if len(node.Assignments) > 0 {
		formatAssignments(sb, node.Assignments, indent)
	}
}

func formatInsertEntity(sb *strings.Builder, node *InsertEntityNode, indent string) {
	sb.WriteString(fmt.Sprintf("%sinsert %s {\n", indent, node.TargetKind))
	formatSelector(sb, node.Selector, indent+"  ")
	if node.Discriminator != nil {
		sb.WriteString(fmt.Sprintf("%s  discriminator: %s;\n", indent, FormatExpr(node.Discriminator)))
	}
	sb.WriteString(fmt.Sprintf("%s}\n", indent))
	if len(node.Assignments) > 0 {
		formatAssignments(sb, node.Assignments, indent)
	}
}

func formatDeleteEntity(sb *strings.Builder, node *DeleteEntityNode, indent string) {
	sb.WriteString(fmt.Sprintf("%sdelete %s {\n", indent, node.Selector.Kind))
	formatSelector(sb, node.Selector, indent+"  ")
	sb.WriteString(fmt.Sprintf("%s};", indent))
}

func formatRelateEntities(sb *strings.Builder, node *RelateEntitiesNode, indent string) {
	sb.WriteString(fmt.Sprintf("%srelate %s to %s as %s {\n",
		indent, node.FromSelector.Kind, node.ToSelector.Kind, node.RelationKind))
	sb.WriteString(fmt.Sprintf("%s  from:\n", indent))
	formatSelector(sb, node.FromSelector, indent+"    ")
	sb.WriteString(fmt.Sprintf("%s  to:\n", indent))
	formatSelector(sb, node.ToSelector, indent+"    ")
	if node.Guard != nil {
		sb.WriteString(fmt.Sprintf("%s  guard: %s;\n", indent, FormatExpr(node.Guard)))
	}
	sb.WriteString(fmt.Sprintf("%s};", indent))
}

func formatUnrelateEntities(sb *strings.Builder, node *UnrelateEntitiesNode, indent string) {
	sb.WriteString(fmt.Sprintf("%sunrelate %s from %s as %s {\n",
		indent, node.FromSelector.Kind, node.ToSelector.Kind, node.RelationKind))
	sb.WriteString(fmt.Sprintf("%s  from:\n", indent))
	formatSelector(sb, node.FromSelector, indent+"    ")
	sb.WriteString(fmt.Sprintf("%s  to:\n", indent))
	formatSelector(sb, node.ToSelector, indent+"    ")
	if node.Guard != nil {
		sb.WriteString(fmt.Sprintf("%s  guard: %s;\n", indent, FormatExpr(node.Guard)))
	}
	sb.WriteString(fmt.Sprintf("%s};", indent))
}

func formatMergeEntities(sb *strings.Builder, node *MergeEntitiesNode, indent string) {
	sb.WriteString(fmt.Sprintf("%smerge %s into %s {\n", indent, node.Selector.Kind, node.TargetKind))
	formatSelector(sb, node.Selector, indent+"  ")
	if node.Discriminator != nil {
		sb.WriteString(fmt.Sprintf("%s  discriminator: %s;\n", indent, FormatExpr(node.Discriminator)))
	}
	sb.WriteString(fmt.Sprintf("%s  retain_sources: %v;\n", indent, node.RetainSources))
	sb.WriteString(fmt.Sprintf("%s  reanchor_relations: %v;\n", indent, node.ReanchorRelations))
	sb.WriteString(fmt.Sprintf("%s}\n", indent))
	if len(node.Assignments) > 0 {
		formatAssignments(sb, node.Assignments, indent)
	}
}

func formatSplitEntity(sb *strings.Builder, node *SplitEntityNode, indent string) {
	sb.WriteString(fmt.Sprintf("%ssplit %s into %s {\n", indent, node.Selector.Kind, node.TargetKind))
	formatSelector(sb, node.Selector, indent+"  ")
	sb.WriteString(fmt.Sprintf("%s  retain_source: %v;\n", indent, node.RetainSource))
	for _, p := range node.Partitions {
		sb.WriteString(fmt.Sprintf("%s  partition %q {\n", indent, p.Name))
		if p.Discriminator != nil {
			sb.WriteString(fmt.Sprintf("%s    discriminator: %s;\n", indent, FormatExpr(p.Discriminator)))
		}
		if len(p.Assignments) > 0 {
			formatAssignments(sb, p.Assignments, indent+"    ")
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("%s  }\n", indent))
	}
	sb.WriteString(fmt.Sprintf("%s};", indent))
}

// FormatExpr formats an expression node into a readable string.
func FormatExpr(node ExprNode) string {
	if node == nil {
		return ""
	}
	switch e := node.(type) {
	case *IdentExpr:
		return e.Path

	case *LiteralExpr:
		switch e.Kind {
		case LitString:
			return fmt.Sprintf("%q", e.Literal)
		case LitInt, LitDecimal, LitBool, LitNull:
			return e.Literal
		case LitTimestamp:
			return fmt.Sprintf("ts(%q)", e.Literal)
		case LitDate:
			return fmt.Sprintf("date(%q)", e.Literal)
		case LitDuration:
			return fmt.Sprintf("dur(%q)", e.Literal)
		case LitAtom:
			return fmt.Sprintf(":%s", e.Literal)
		default:
			return e.Literal
		}

	case *UnaryExpr:
		opStr := "!"
		if e.Op == TokenMinus {
			opStr = "-"
		}
		return fmt.Sprintf("%s%s", opStr, FormatExpr(e.Right))

	case *BinaryExpr:
		opStr := opToString(e.Op)
		return fmt.Sprintf("(%s %s %s)", FormatExpr(e.Left), opStr, FormatExpr(e.Right))

	case *CallExpr:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = FormatExpr(a)
		}
		return fmt.Sprintf("%s(%s)", e.FuncName, strings.Join(args, ", "))

	default:
		return ""
	}
}

func opToString(op TokenType) string {
	switch op {
	case TokenEqual:
		return "=="
	case TokenNotEqual:
		return "!="
	case TokenLess:
		return "<"
	case TokenLessEq:
		return "<="
	case TokenGreater:
		return ">"
	case TokenGreatEq:
		return ">="
	case TokenAnd:
		return "&&"
	case TokenOr:
		return "||"
	case TokenPlus:
		return "+"
	case TokenMinus:
		return "-"
	case TokenStar:
		return "*"
	case TokenSlash:
		return "/"
	case TokenPercent:
		return "%"
	default:
		return "?"
	}
}

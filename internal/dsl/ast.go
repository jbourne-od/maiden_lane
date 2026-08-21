package dsl

// Node represents an AST node.
type Node interface {
	Position() Pos
}

// File represents a parsed .ml source file.
type File struct {
	Pos          Pos
	Declarations []Declaration
}

func (f *File) Position() Pos { return f.Pos }

// Declaration is the interface implemented by top-level declarations.
type Declaration interface {
	Node
	declarationNode()
}

// SchemaDecl represents a schema declaration block.
type SchemaDecl struct {
	Pos       Pos
	Entities  []*EntityDecl
	Relations []*RelationDecl
}

func (d *SchemaDecl) Position() Pos    { return d.Pos }
func (d *SchemaDecl) declarationNode() {}

// FieldDecl represents an entity field in a schema declaration.
type FieldDecl struct {
	Pos      Pos
	Name     string
	Type     string // string, int64, decimal, timestamp, date, duration, atom
	Required bool
}

func (f *FieldDecl) Position() Pos { return f.Pos }

// EntityDecl represents an entity kind in a schema.
type EntityDecl struct {
	Pos    Pos
	Kind   string
	Fields []*FieldDecl
}

func (e *EntityDecl) Position() Pos { return e.Pos }

// RelationDecl represents a relation declaration in a schema.
type RelationDecl struct {
	Pos      Pos
	Kind     string
	FromKind string
	ToKind   string
}

func (r *RelationDecl) Position() Pos { return r.Pos }

// RuleDecl represents a rule declaration block.
type RuleDecl struct {
	Pos            Pos
	ID             string
	DeclaredReads  []string
	DeclaredWrites []string
	DependsOn      []string
	Operator       OperatorNode
}

func (r *RuleDecl) Position() Pos    { return r.Pos }
func (r *RuleDecl) declarationNode() {}

// CheckpointDecl represents a checkpoint declaration `checkpoint <key> after <rule_id>;`.
type CheckpointDecl struct {
	Pos   Pos
	Key   string
	After string
}

func (c *CheckpointDecl) Position() Pos    { return c.Pos }
func (c *CheckpointDecl) declarationNode() {}

// RequirementDecl represents a readiness requirement `require <field> present as <CODE>;`.
type RequirementDecl struct {
	Pos   Pos
	Field string
	Code  string
}

func (r *RequirementDecl) Position() Pos { return r.Pos }

// ProfileDecl represents a completeness profile `profile <key> for entity <kind> [implies: [...]] { ... }`.
type ProfileDecl struct {
	Pos          Pos
	Key          string
	EntityKind   string
	Implies      []string
	Requirements []*RequirementDecl
}

func (p *ProfileDecl) Position() Pos    { return p.Pos }
func (p *ProfileDecl) declarationNode() {}

// OperatorNode represents one transformation operator body.
type OperatorNode interface {
	Node
	operatorNode()
}

// AssignmentNode represents a field assignment `target = value`.
type AssignmentNode struct {
	Pos    Pos
	Target string
	Value  ExprNode
}

func (a *AssignmentNode) Position() Pos { return a.Pos }

// SelectorNode represents a selection query `select kind where ... group by ... having ...`.
type SelectorNode struct {
	Pos              Pos
	Kind             string
	Where            ExprNode
	GroupBy          ExprNode
	Having           ExprNode
	CardinalityKind  string // "any", "exactly", "at_least"
	CardinalityCount uint64
}

func (s *SelectorNode) Position() Pos { return s.Pos }

// SelectAndAssignNode represents `select ... set ...`.
type SelectAndAssignNode struct {
	Pos         Pos
	Selector    *SelectorNode
	Guard       ExprNode
	Assignments []*AssignmentNode
}

func (s *SelectAndAssignNode) Position() Pos { return s.Pos }
func (s *SelectAndAssignNode) operatorNode() {}

// InsertEntityNode represents `insert kind { ... } set ...`.
type InsertEntityNode struct {
	Pos           Pos
	TargetKind    string
	Selector      *SelectorNode
	Discriminator ExprNode
	Guard         ExprNode
	Assignments   []*AssignmentNode
}

func (n *InsertEntityNode) Position() Pos { return n.Pos }
func (n *InsertEntityNode) operatorNode() {}

// DeleteEntityNode represents `delete kind { select ... }`.
type DeleteEntityNode struct {
	Pos      Pos
	Selector *SelectorNode
	Guard    ExprNode
}

func (n *DeleteEntityNode) Position() Pos { return n.Pos }
func (n *DeleteEntityNode) operatorNode() {}

// RelateEntitiesNode represents `relate from to as rel { ... }`.
type RelateEntitiesNode struct {
	Pos          Pos
	RelationKind string
	FromSelector *SelectorNode
	ToSelector   *SelectorNode
	Guard        ExprNode
}

func (n *RelateEntitiesNode) Position() Pos { return n.Pos }
func (n *RelateEntitiesNode) operatorNode() {}

// UnrelateEntitiesNode represents `unrelate from from to as rel { ... }`.
type UnrelateEntitiesNode struct {
	Pos          Pos
	RelationKind string
	FromSelector *SelectorNode
	ToSelector   *SelectorNode
	Guard        ExprNode
}

func (n *UnrelateEntitiesNode) Position() Pos { return n.Pos }
func (n *UnrelateEntitiesNode) operatorNode() {}

// MergeEntitiesNode represents `merge kind into target_kind { ... } set ...`.
type MergeEntitiesNode struct {
	Pos               Pos
	TargetKind        string
	Selector          *SelectorNode
	Discriminator     ExprNode
	Guard             ExprNode
	RetainSources     bool
	ReanchorRelations bool
	Assignments       []*AssignmentNode
}

func (n *MergeEntitiesNode) Position() Pos { return n.Pos }
func (n *MergeEntitiesNode) operatorNode() {}

// PartitionNode represents a split partition branch.
type PartitionNode struct {
	Pos           Pos
	Name          string
	Discriminator ExprNode
	Assignments   []*AssignmentNode
}

func (p *PartitionNode) Position() Pos { return p.Pos }

// SplitEntityNode represents `split kind into target_kind { ... partition ... }`.
type SplitEntityNode struct {
	Pos          Pos
	TargetKind   string
	Selector     *SelectorNode
	Guard        ExprNode
	RetainSource bool
	Partitions   []*PartitionNode
}

func (n *SplitEntityNode) Position() Pos { return n.Pos }
func (n *SplitEntityNode) operatorNode() {}

// ExprNode represents an expression.
type ExprNode interface {
	Node
	exprNode()
}

// IdentExpr represents a field path or identifier (e.g. `driver.depot`, `status`).
type IdentExpr struct {
	Pos  Pos
	Path string
}

func (e *IdentExpr) Position() Pos { return e.Pos }
func (e *IdentExpr) exprNode()     {}

// LiteralKind indicates the data type of the literal value.
type LiteralKind string

const (
	LitString    LiteralKind = "string"
	LitInt       LiteralKind = "int64"
	LitDecimal   LiteralKind = "decimal"
	LitBool      LiteralKind = "bool"
	LitTimestamp LiteralKind = "timestamp"
	LitDate      LiteralKind = "date"
	LitDuration  LiteralKind = "duration"
	LitAtom      LiteralKind = "atom"
	LitNull      LiteralKind = "null"
)

// LiteralExpr represents a constant literal.
type LiteralExpr struct {
	Pos     Pos
	Kind    LiteralKind
	Literal string
}

func (e *LiteralExpr) Position() Pos { return e.Pos }
func (e *LiteralExpr) exprNode()     {}

// UnaryExpr represents a unary operation (e.g. `!a`, `-b`).
type UnaryExpr struct {
	Pos   Pos
	Op    TokenType
	Right ExprNode
}

func (e *UnaryExpr) Position() Pos { return e.Pos }
func (e *UnaryExpr) exprNode()     {}

// BinaryExpr represents a binary operation (e.g. `a + b`, `x == y`).
type BinaryExpr struct {
	Pos   Pos
	Op    TokenType
	Left  ExprNode
	Right ExprNode
}

func (e *BinaryExpr) Position() Pos { return e.Pos }
func (e *BinaryExpr) exprNode()     {}

// CallExpr represents a function call, reduction, or constructor.
type CallExpr struct {
	Pos      Pos
	FuncName string
	Args     []ExprNode
}

func (e *CallExpr) Position() Pos { return e.Pos }
func (e *CallExpr) exprNode()     {}

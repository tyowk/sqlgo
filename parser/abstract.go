package parser

type Statement interface {
	stmtNode()
}

type SelectStmt struct {
	Distinct bool
	Columns  []SelectColumn
	From     []TableRef
	Joins    []JoinClause
	Where    Expr
	GroupBy  []Expr
	Having   Expr
	OrderBy  []OrderByExpr
	Limit    Expr
	Offset   Expr
	Compound *CompoundClause
	With     *WithClause
}

func (*SelectStmt) stmtNode() {}

type CompoundClause struct {
	Op    string
	All   bool
	Right *SelectStmt
}

type WithClause struct {
	Recursive bool
	CTEs      []CTE
}

type CTE struct {
	Name    string
	Columns []string
	Query   *SelectStmt
}

type JoinClause struct {
	Type  string
	Table TableRef
	On    Expr
	Using []string
}

type SelectColumn struct {
	Expr  Expr
	Alias string
	Star  bool
}

type TableRef struct {
	Name  string
	Alias string
}

type OrderByExpr struct {
	Expr  Expr
	Desc  bool
	Nulls string
}

type InsertStmt struct {
	Table      string
	Columns    []string
	Values     [][]Expr
	OnConflict ConflictClause
	Select     *SelectStmt
	Or         string
}

func (*InsertStmt) stmtNode() {}

type ConflictClause struct {
	Action string
}

type UpdateStmt struct {
	Table string
	Sets  []SetClause
	Where Expr
	Or    string
}

func (*UpdateStmt) stmtNode() {}

type SetClause struct {
	Column string
	Value  Expr
}

type DeleteStmt struct {
	Table string
	Where Expr
}

func (*DeleteStmt) stmtNode() {}

type CreateTableStmt struct {
	Table       string
	Temp        bool
	IfNotExists bool
	Columns     []ColumnDef
	Constraints []TableConstraint
}

func (*CreateTableStmt) stmtNode() {}

type TableConstraint struct {
	Type    string
	Columns []string
	Name    string
}

type ColumnDef struct {
	Name          string
	Type          string
	PrimaryKey    bool
	NotNull       bool
	Unique        bool
	Default       string
	AutoIncrement bool
	Check         Expr
	References    *ForeignKeyRef
}

type ForeignKeyRef struct {
	Table    string
	Column   string
	OnDelete string
	OnUpdate string
}

type DropTableStmt struct {
	Table    string
	IfExists bool
}

func (*DropTableStmt) stmtNode() {}

type CreateIndexStmt struct {
	Name        string
	Unique      bool
	Table       string
	Column      string
	IfNotExists bool
}

func (*CreateIndexStmt) stmtNode() {}

type DropIndexStmt struct {
	Name     string
	IfExists bool
}

func (*DropIndexStmt) stmtNode() {}

type AlterTableStmt struct {
	Table  string
	Action AlterAction
}

func (*AlterTableStmt) stmtNode() {}

type AlterAction interface {
	alterAction()
}

type AlterAddColumn struct {
	Column ColumnDef
}

func (*AlterAddColumn) alterAction() {}

type AlterRenameTable struct {
	NewName string
}

func (*AlterRenameTable) alterAction() {}

type AlterRenameColumn struct {
	OldName string
	NewName string
}

func (*AlterRenameColumn) alterAction() {}

type AlterDropColumn struct {
	Column string
}

func (*AlterDropColumn) alterAction() {}

type CreateViewStmt struct {
	Name        string
	Temp        bool
	IfNotExists bool
	Select      *SelectStmt
}

func (*CreateViewStmt) stmtNode() {}

type DropViewStmt struct {
	Name     string
	IfExists bool
}

func (*DropViewStmt) stmtNode() {}

type BeginStmt struct {
	Mode string
}

func (*BeginStmt) stmtNode() {}

type CommitStmt struct{}

func (*CommitStmt) stmtNode() {}

type RollbackStmt struct {
	To string
}

func (*RollbackStmt) stmtNode() {}

type SavepointStmt struct {
	Name string
}

func (*SavepointStmt) stmtNode() {}

type ReleaseSavepointStmt struct {
	Name string
}

func (*ReleaseSavepointStmt) stmtNode() {}

type ShowTablesStmt struct{}

func (*ShowTablesStmt) stmtNode() {}

type ShowIndexesStmt struct {
	Table string
}

func (*ShowIndexesStmt) stmtNode() {}

type ExplainStmt struct {
	Inner Statement
}

func (*ExplainStmt) stmtNode() {}

type PragmaStmt struct {
	Name  string
	Value string
}

func (*PragmaStmt) stmtNode() {}

type VacuumStmt struct{}

func (*VacuumStmt) stmtNode() {}

type Expr interface {
	exprNode()
}

type LiteralExpr struct {
	Value  interface{}
	IsNull bool
}

func (*LiteralExpr) exprNode() {}

type IdentExpr struct {
	Table string
	Name  string
}

func (*IdentExpr) exprNode() {}

type StarExpr struct{}

func (*StarExpr) exprNode() {}

type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (*BinaryExpr) exprNode() {}

type UnaryExpr struct {
	Op      string
	Operand Expr
}

func (*UnaryExpr) exprNode() {}

type FuncCallExpr struct {
	Name     string
	Args     []Expr
	Distinct bool
	Star     bool
}

func (*FuncCallExpr) exprNode() {}

type IsNullExpr struct {
	Operand Expr
	IsNot   bool
}

func (*IsNullExpr) exprNode() {}

type InExpr struct {
	Operand  Expr
	List     []Expr
	Subquery *SelectStmt
	IsNot    bool
}

func (*InExpr) exprNode() {}

type LikeExpr struct {
	Operand Expr
	Pattern Expr
	Escape  Expr
	IsNot   bool
}

func (*LikeExpr) exprNode() {}

type GlobExpr struct {
	Operand Expr
	Pattern Expr
	IsNot   bool
}

func (*GlobExpr) exprNode() {}

type BetweenExpr struct {
	Operand Expr
	Low     Expr
	High    Expr
	IsNot   bool
}

func (*BetweenExpr) exprNode() {}

type CaseExpr struct {
	Base  Expr
	Whens []WhenClause
	Else  Expr
}

func (*CaseExpr) exprNode() {}

type WhenClause struct {
	Cond Expr
	Then Expr
}

type CastExpr struct {
	Expr Expr
	Type string
}

func (*CastExpr) exprNode() {}

type ExistsExpr struct {
	Subquery *SelectStmt
	IsNot    bool
}

func (*ExistsExpr) exprNode() {}

type SubqueryExpr struct {
	Query *SelectStmt
}

func (*SubqueryExpr) exprNode() {}

type RowValueExpr struct {
	Values []Expr
}

func (*RowValueExpr) exprNode() {}

package executor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tyowk/sqlgo/parser"
	"github.com/tyowk/sqlgo/storage"
)

type ResultSet struct {
	Columns []string
	Rows    [][]string
}

type Executor struct {
	pager  *storage.Pager
	schema *storage.SchemaManager
	table  *storage.TableEngine
	inTx   bool
	tx     *storage.Transaction
}

func NewExecutor(pager *storage.Pager, schema *storage.SchemaManager) *Executor {
	return &Executor{
		pager:  pager,
		schema: schema,
		table:  storage.NewTableEngine(pager, schema),
	}
}

func (e *Executor) Execute(sql string) (*ResultSet, error) {
	stmts, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}
	var last *ResultSet
	for _, stmt := range stmts {
		rs, err := e.execStatement(stmt)
		if err != nil {
			return nil, err
		}
		last = rs
	}
	return last, nil
}

func (e *Executor) execStatement(stmt parser.Statement) (*ResultSet, error) {
	switch s := stmt.(type) {
	case *parser.SelectStmt:
		return e.execSelect(s)
	case *parser.InsertStmt:
		return e.execInsert(s)
	case *parser.UpdateStmt:
		return e.execUpdate(s)
	case *parser.DeleteStmt:
		return e.execDelete(s)
	case *parser.CreateTableStmt:
		return e.execCreateTable(s)
	case *parser.DropTableStmt:
		return e.execDropTable(s)
	case *parser.CreateIndexStmt:
		return e.execCreateIndex(s)
	case *parser.DropIndexStmt:
		return e.execDropIndex(s)
	case *parser.CreateViewStmt:
		return e.execCreateView(s)
	case *parser.DropViewStmt:
		return e.execDropView(s)
	case *parser.AlterTableStmt:
		return e.execAlterTable(s)
	case *parser.BeginStmt:
		return e.execBegin(s)
	case *parser.CommitStmt:
		return e.execCommit()
	case *parser.RollbackStmt:
		return e.execRollback(s)
	case *parser.SavepointStmt:
		return e.execSavepoint(s)
	case *parser.ReleaseSavepointStmt:
		return e.execReleaseSavepoint(s)
	case *parser.ShowTablesStmt:
		return e.execShowTables()
	case *parser.ShowIndexesStmt:
		return e.execShowIndexes(s)
	case *parser.ExplainStmt:
		return e.execExplain(s)
	case *parser.PragmaStmt:
		return e.execPragma(s)
	case *parser.VacuumStmt:
		return &ResultSet{}, e.pager.FlushAll()
	default:
		return nil, fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

func (e *Executor) execSelect(stmt *parser.SelectStmt) (*ResultSet, error) {
	if stmt.With != nil {
		return e.execWithSelect(stmt)
	}
	if stmt.Compound != nil {
		return e.execCompound(stmt)
	}
	rows, colNames, err := e.evalSelect(stmt, nil)
	if err != nil {
		return nil, err
	}
	rs := &ResultSet{Columns: colNames}
	for _, row := range rows {
		strRow := make([]string, len(row.values))
		for i, v := range row.values {
			if v == nil || v.Type == storage.ValNull {
				strRow[i] = "NULL"
			} else {
				strRow[i] = v.String()
			}
		}
		rs.Rows = append(rs.Rows, strRow)
	}
	return rs, nil
}

type rowWithValues struct {
	values []*storage.Value
}

type cteResult struct {
	columns []string
	rows    []*rowWithValues
}

func (e *Executor) execWithSelect(stmt *parser.SelectStmt) (*ResultSet, error) {
	cteMap := make(map[string]*cteResult)
	for _, cte := range stmt.With.CTEs {
		cteRows, colNames, err := e.evalSelect(cte.Query, cteMap)
		if err != nil {
			return nil, fmt.Errorf("CTE %s: %w", cte.Name, err)
		}
		cteMap[strings.ToLower(cte.Name)] = &cteResult{columns: colNames, rows: cteRows}
	}
	stmt2 := *stmt
	stmt2.With = nil
	return e.execSelect(&stmt2)
}

func (e *Executor) execCompound(stmt *parser.SelectStmt) (*ResultSet, error) {
	left := *stmt
	left.Compound = nil
	leftRS, err := e.execSelect(&left)
	if err != nil {
		return nil, err
	}
	rightRS, err := e.execSelect(stmt.Compound.Right)
	if err != nil {
		return nil, err
	}
	switch strings.ToUpper(stmt.Compound.Op) {
	case "UNION":
		if stmt.Compound.All {
			leftRS.Rows = append(leftRS.Rows, rightRS.Rows...)
		} else {
			seen := make(map[string]bool)
			var merged [][]string
			for _, row := range leftRS.Rows {
				key := strings.Join(row, "\x00")
				if !seen[key] {
					seen[key] = true
					merged = append(merged, row)
				}
			}
			for _, row := range rightRS.Rows {
				key := strings.Join(row, "\x00")
				if !seen[key] {
					seen[key] = true
					merged = append(merged, row)
				}
			}
			leftRS.Rows = merged
		}
	case "INTERSECT":
		rightSet := make(map[string]bool)
		for _, row := range rightRS.Rows {
			rightSet[strings.Join(row, "\x00")] = true
		}
		var result [][]string
		for _, row := range leftRS.Rows {
			if rightSet[strings.Join(row, "\x00")] {
				result = append(result, row)
			}
		}
		leftRS.Rows = result
	case "EXCEPT":
		rightSet := make(map[string]bool)
		for _, row := range rightRS.Rows {
			rightSet[strings.Join(row, "\x00")] = true
		}
		var result [][]string
		for _, row := range leftRS.Rows {
			if !rightSet[strings.Join(row, "\x00")] {
				result = append(result, row)
			}
		}
		leftRS.Rows = result
	}
	return leftRS, nil
}

func (e *Executor) evalSelect(stmt *parser.SelectStmt, cteMap map[string]*cteResult) ([]*rowWithValues, []string, error) {
	if len(stmt.From) == 0 {
		return e.evalSelectNoFrom(stmt)
	}
	allRows, mainSchema, tableAliases, err := e.gatherRows(stmt, cteMap)
	if err != nil {
		return nil, nil, err
	}
	var filtered []*rowWithValues
	for _, row := range allRows {
		if stmt.Where != nil {
			ctx := e.makeCtxForRow(row, mainSchema, tableAliases)
			val, err := EvalExpr(ctx, stmt.Where)
			if err != nil {
				return nil, nil, err
			}
			if !IsTruthy(val) {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	if len(stmt.GroupBy) > 0 {
		return e.evalGroupBy(stmt, filtered, mainSchema, tableAliases)
	}
	hasAgg := e.hasAggregates(stmt)
	colNames, err := e.resolveColumnNames(stmt, mainSchema, tableAliases)
	if err != nil {
		return nil, nil, err
	}
	if hasAgg {
		result, err := e.evalAggregate(stmt, filtered, mainSchema, tableAliases, colNames)
		if err != nil {
			return nil, nil, err
		}
		return result, colNames, nil
	}
	var result []*rowWithValues
	for _, row := range filtered {
		ctx := e.makeCtxForRow(row, mainSchema, tableAliases)
		projected, err := e.projectRow(ctx, stmt)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, projected)
	}
	if stmt.Distinct {
		result = dedup(result)
	}
	if len(stmt.OrderBy) > 0 {
		result, err = e.sortRows(stmt, result, mainSchema, tableAliases)
		if err != nil {
			return nil, nil, err
		}
	}
	result = applyLimitOffset(stmt, result)
	return result, colNames, nil
}

func (e *Executor) evalSelectNoFrom(stmt *parser.SelectStmt) ([]*rowWithValues, []string, error) {
	ctx := &EvalContext{}
	var vals []*storage.Value
	var colNames []string
	for _, col := range stmt.Columns {
		if col.Star {
			continue
		}
		v, err := EvalExpr(ctx, col.Expr)
		if err != nil {
			return nil, nil, err
		}
		vals = append(vals, v)
		if col.Alias != "" {
			colNames = append(colNames, col.Alias)
		} else {
			colNames = append(colNames, exprName(col.Expr))
		}
	}
	return []*rowWithValues{{values: vals}}, colNames, nil
}

type scanRow struct {
	Row   *storage.Row
	RowID int64
}

func (e *Executor) getTableRows(name string, cteMap map[string]*cteResult) ([]scanRow, *storage.TableSchema, error) {
	if cteMap != nil {
		if cte, ok := cteMap[strings.ToLower(name)]; ok {
			fakeSchema := &storage.TableSchema{Name: name}
			for _, col := range cte.columns {
				fakeSchema.Columns = append(fakeSchema.Columns, storage.Column{Name: col, Type: storage.TypeText})
			}
			var rows []scanRow
			for i, r := range cte.rows {
				rows = append(rows, scanRow{Row: &storage.Row{Values: r.values}, RowID: int64(i + 1)})
			}
			return rows, fakeSchema, nil
		}
	}
	if vs, ok := e.schema.GetView(name); ok {
		stmts, err := parser.Parse(vs.SQL)
		if err != nil {
			return nil, nil, fmt.Errorf("view %s parse error: %w", name, err)
		}
		if len(stmts) == 0 {
			return nil, &storage.TableSchema{Name: name}, nil
		}
		sel, ok := stmts[0].(*parser.SelectStmt)
		if !ok {
			return nil, nil, fmt.Errorf("view %s is not a SELECT", name)
		}
		rows, colNames, err := e.evalSelect(sel, cteMap)
		if err != nil {
			return nil, nil, fmt.Errorf("view %s: %w", name, err)
		}
		fakeSchema := &storage.TableSchema{Name: name}
		for _, col := range colNames {
			fakeSchema.Columns = append(fakeSchema.Columns, storage.Column{Name: col, Type: storage.TypeText})
		}
		var result []scanRow
		for i, r := range rows {
			result = append(result, scanRow{Row: &storage.Row{Values: r.values}, RowID: int64(i + 1)})
		}
		return result, fakeSchema, nil
	}
	scans, err := e.table.Scan(name)
	if err != nil {
		return nil, nil, err
	}
	ts, ok := e.schema.GetTable(name)
	if !ok {
		return nil, nil, fmt.Errorf("table '%s' does not exist", name)
	}
	var rows []scanRow
	for _, sr := range scans {
		rows = append(rows, scanRow{Row: sr.Row, RowID: sr.RowID})
	}
	return rows, ts, nil
}

func (e *Executor) gatherRows(stmt *parser.SelectStmt, cteMap map[string]*cteResult) ([]*rowWithValues, *storage.TableSchema, map[string]*tableBinding, error) {
	first := stmt.From[0]
	alias := first.Alias
	if alias == "" {
		alias = first.Name
	}
	mainRows, mainSchema, err := e.getTableRows(first.Name, cteMap)
	if err != nil {
		return nil, nil, nil, err
	}
	tableAliases := map[string]*tableBinding{}
	var allRows []*rowWithValues
	for _, sr := range mainRows {
		binding := &tableBinding{Schema: mainSchema, Row: sr.Row, RowID: sr.RowID}
		tableAliases[strings.ToLower(alias)] = binding
		allRows = append(allRows, &rowWithValues{values: sr.Row.Values})
	}
	for _, from := range stmt.From[1:] {
		alias2 := from.Alias
		if alias2 == "" {
			alias2 = from.Name
		}
		rows2, schema2, err := e.getTableRows(from.Name, cteMap)
		if err != nil {
			return nil, nil, nil, err
		}
		var crossed []*rowWithValues
		for _, r1 := range allRows {
			for _, r2 := range rows2 {
				combined := &rowWithValues{
					values: append(append([]*storage.Value{}, r1.values...), r2.Row.Values...),
				}
				crossed = append(crossed, combined)
			}
		}
		allRows = crossed
		_ = schema2
		_ = alias2
	}
	for _, join := range stmt.Joins {
		allRows, err = e.applyJoin(allRows, join, mainSchema, tableAliases, cteMap)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return allRows, mainSchema, tableAliases, nil
}

func (e *Executor) applyJoin(leftRows []*rowWithValues, join parser.JoinClause, mainSchema *storage.TableSchema, tableAliases map[string]*tableBinding, cteMap map[string]*cteResult) ([]*rowWithValues, error) {
	alias := join.Table.Alias
	if alias == "" {
		alias = join.Table.Name
	}
	rightRows, rightSchema, err := e.getTableRows(join.Table.Name, cteMap)
	if err != nil {
		return nil, err
	}
	var result []*rowWithValues
	for _, leftRow := range leftRows {
		matched := false
		for _, rightRow := range rightRows {
			combined := &rowWithValues{
				values: append(append([]*storage.Value{}, leftRow.values...), rightRow.Row.Values...),
			}
			rightBinding := &tableBinding{Schema: rightSchema, Row: rightRow.Row, RowID: rightRow.RowID}
			tableAliases[strings.ToLower(alias)] = rightBinding
			ctx := e.makeCtxForRow(combined, mainSchema, tableAliases)
			var passes bool
			if join.On != nil {
				val, err := EvalExpr(ctx, join.On)
				if err != nil {
					return nil, err
				}
				passes = IsTruthy(val)
			} else if len(join.Using) > 0 {
				passes = true
				for _, col := range join.Using {
					lv := getValueFromRow(leftRow.values, mainSchema, col)
					rv := getValueFromRow(rightRow.Row.Values, rightSchema, col)
					if lv == nil || rv == nil || lv.Compare(rv) != 0 {
						passes = false
						break
					}
				}
			} else {
				passes = true
			}
			if passes {
				matched = true
				result = append(result, combined)
			}
		}
		if !matched && strings.Contains(join.Type, "LEFT") {
			nulls := make([]*storage.Value, len(rightSchema.Columns))
			for i := range nulls {
				nulls[i] = storage.NullValue
			}
			result = append(result, &rowWithValues{
				values: append(append([]*storage.Value{}, leftRow.values...), nulls...),
			})
		}
	}
	return result, nil
}

func getValueFromRow(values []*storage.Value, schema *storage.TableSchema, col string) *storage.Value {
	if schema == nil {
		return nil
	}
	idx := schema.ColumnIndex(col)
	if idx < 0 || idx >= len(values) {
		return nil
	}
	return values[idx]
}

func (e *Executor) makeCtxForRow(row *rowWithValues, schema *storage.TableSchema, tableAliases map[string]*tableBinding) *EvalContext {
	fakeRow := &storage.Row{Values: row.values}
	return &EvalContext{Row: fakeRow, Schema: schema, Tables: tableAliases}
}

func (e *Executor) projectRow(ctx *EvalContext, stmt *parser.SelectStmt) (*rowWithValues, error) {
	var vals []*storage.Value
	for _, col := range stmt.Columns {
		if col.Star {
			if ctx.Row != nil {
				vals = append(vals, ctx.Row.Values...)
			}
			continue
		}
		v, err := EvalExpr(ctx, col.Expr)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return &rowWithValues{values: vals}, nil
}

func (e *Executor) resolveColumnNames(stmt *parser.SelectStmt, schema *storage.TableSchema, tableAliases map[string]*tableBinding) ([]string, error) {
	var names []string
	for _, col := range stmt.Columns {
		if col.Star {
			if schema != nil {
				for _, c := range schema.Columns {
					names = append(names, c.Name)
				}
			}
			continue
		}
		if col.Alias != "" {
			names = append(names, col.Alias)
		} else {
			names = append(names, exprName(col.Expr))
		}
	}
	return names, nil
}

func exprName(expr parser.Expr) string {
	switch e := expr.(type) {
	case *parser.IdentExpr:
		return e.Name
	case *parser.FuncCallExpr:
		return e.Name
	case *parser.LiteralExpr:
		if e.IsNull {
			return "NULL"
		}
		return fmt.Sprintf("%v", e.Value)
	case *parser.CastExpr:
		return "CAST"
	case *parser.CaseExpr:
		return "CASE"
	case *parser.BinaryExpr:
		return e.Op
	}
	return "?"
}

func (e *Executor) hasAggregates(stmt *parser.SelectStmt) bool {
	for _, col := range stmt.Columns {
		if containsAggregate(col.Expr) {
			return true
		}
	}
	return false
}

func containsAggregate(expr parser.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.FuncCallExpr:
		fn := strings.ToUpper(e.Name)
		switch fn {
		case "COUNT", "SUM", "AVG", "MIN", "MAX", "TOTAL", "GROUP_CONCAT":
			return true
		}
	case *parser.BinaryExpr:
		return containsAggregate(e.Left) || containsAggregate(e.Right)
	case *parser.UnaryExpr:
		return containsAggregate(e.Operand)
	}
	return false
}

type aggState interface {
	accumulate(*storage.Value)
	result() *storage.Value
}

type simpleAgg struct {
	name  string
	count int64
	sum   float64
	isum  int64
	isInt bool
	min   *storage.Value
	max   *storage.Value
	parts []string
}

func (a *simpleAgg) accumulate(v *storage.Value) {
	if v == nil || v.Type == storage.ValNull {
		return
	}
	switch a.name {
	case "COUNT":
		a.count++
	case "SUM", "TOTAL":
		if v.Type == storage.ValInteger {
			a.isum += v.Integer
			a.isInt = true
		}
		a.sum += v.ToFloat()
		a.count++
	case "AVG":
		a.sum += v.ToFloat()
		a.count++
	case "MIN":
		if a.min == nil || v.Compare(a.min) < 0 {
			a.min = v
		}
	case "MAX":
		if a.max == nil || v.Compare(a.max) > 0 {
			a.max = v
		}
	case "GROUP_CONCAT":
		a.parts = append(a.parts, v.String())
	}
}

func (a *simpleAgg) result() *storage.Value {
	switch a.name {
	case "COUNT":
		return storage.IntValue(a.count)
	case "SUM":
		if a.count == 0 {
			return storage.NullValue
		}
		if a.isInt {
			return storage.IntValue(a.isum)
		}
		return storage.RealValue(a.sum)
	case "TOTAL":
		return storage.RealValue(a.sum)
	case "AVG":
		if a.count == 0 {
			return storage.NullValue
		}
		return storage.RealValue(a.sum / float64(a.count))
	case "MIN":
		if a.min == nil {
			return storage.NullValue
		}
		return a.min
	case "MAX":
		if a.max == nil {
			return storage.NullValue
		}
		return a.max
	case "GROUP_CONCAT":
		if len(a.parts) == 0 {
			return storage.NullValue
		}
		return storage.TextValue(strings.Join(a.parts, ","))
	}
	return storage.NullValue
}

func (e *Executor) evalAggregate(stmt *parser.SelectStmt, rows []*rowWithValues, schema *storage.TableSchema, tableAliases map[string]*tableBinding, colNames []string) ([]*rowWithValues, error) {
	aggs := make([]aggState, len(stmt.Columns))
	for i, col := range stmt.Columns {
		if containsAggregate(col.Expr) {
			if fn, ok := col.Expr.(*parser.FuncCallExpr); ok {
				aggs[i] = &simpleAgg{name: strings.ToUpper(fn.Name)}
			}
		}
	}
	for _, row := range rows {
		ctx := e.makeCtxForRow(row, schema, tableAliases)
		for i, col := range stmt.Columns {
			if aggs[i] == nil {
				continue
			}
			fn := col.Expr.(*parser.FuncCallExpr)
			var argVal *storage.Value
			if fn.Star {
				argVal = storage.IntValue(1)
			} else if len(fn.Args) > 0 {
				var err error
				argVal, err = EvalExpr(ctx, fn.Args[0])
				if err != nil {
					return nil, err
				}
			} else {
				argVal = storage.IntValue(1)
			}
			aggs[i].accumulate(argVal)
		}
	}
	rv := &rowWithValues{values: make([]*storage.Value, len(stmt.Columns))}
	for i, col := range stmt.Columns {
		if aggs[i] != nil {
			rv.values[i] = aggs[i].result()
		} else {
			var ctx *EvalContext
			if len(rows) > 0 {
				ctx = e.makeCtxForRow(rows[0], schema, tableAliases)
			} else {
				ctx = &EvalContext{}
			}
			v, err := EvalExpr(ctx, col.Expr)
			if err != nil {
				return nil, err
			}
			rv.values[i] = v
		}
	}
	return []*rowWithValues{rv}, nil
}

func (e *Executor) evalGroupBy(stmt *parser.SelectStmt, rows []*rowWithValues, schema *storage.TableSchema, tableAliases map[string]*tableBinding) ([]*rowWithValues, []string, error) {
	type group struct {
		key  string
		rows []*rowWithValues
	}
	var groups []group
	groupIdx := make(map[string]int)
	for _, row := range rows {
		ctx := e.makeCtxForRow(row, schema, tableAliases)
		keyParts := make([]string, len(stmt.GroupBy))
		for i, gb := range stmt.GroupBy {
			v, err := EvalExpr(ctx, gb)
			if err != nil {
				return nil, nil, err
			}
			if v == nil {
				keyParts[i] = "NULL"
			} else {
				keyParts[i] = v.String()
			}
		}
		key := strings.Join(keyParts, "\x00")
		if idx, ok := groupIdx[key]; ok {
			groups[idx].rows = append(groups[idx].rows, row)
		} else {
			groupIdx[key] = len(groups)
			groups = append(groups, group{key: key, rows: []*rowWithValues{row}})
		}
	}
	colNames, err := e.resolveColumnNames(stmt, schema, tableAliases)
	if err != nil {
		return nil, nil, err
	}
	var result []*rowWithValues
	for _, grp := range groups {
		aggRows, err := e.evalAggregate(stmt, grp.rows, schema, tableAliases, colNames)
		if err != nil {
			return nil, nil, err
		}
		if stmt.Having != nil {
			for _, row := range aggRows {
				ctx := e.makeCtxForRow(row, nil, nil)
				v, err := EvalExpr(ctx, stmt.Having)
				if err != nil {
					return nil, nil, err
				}
				if IsTruthy(v) {
					result = append(result, row)
				}
			}
		} else {
			result = append(result, aggRows...)
		}
	}
	return result, colNames, nil
}

func (e *Executor) sortRows(stmt *parser.SelectStmt, rows []*rowWithValues, schema *storage.TableSchema, tableAliases map[string]*tableBinding) ([]*rowWithValues, error) {
	colNames, _ := e.resolveColumnNames(stmt, schema, tableAliases)
	var sortErr error
	sort.SliceStable(rows, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		for _, ob := range stmt.OrderBy {
			vi, err := e.evalOrderByExpr(ob.Expr, rows[i], schema, tableAliases, colNames)
			if err != nil {
				sortErr = err
				return false
			}
			vj, err := e.evalOrderByExpr(ob.Expr, rows[j], schema, tableAliases, colNames)
			if err != nil {
				sortErr = err
				return false
			}
			if vi == nil || vi.Type == storage.ValNull {
				if vj == nil || vj.Type == storage.ValNull {
					continue
				}
				if ob.Desc {
					return false
				}
				return true
			}
			if vj == nil || vj.Type == storage.ValNull {
				if ob.Desc {
					return true
				}
				return false
			}
			cmp := vi.Compare(vj)
			if cmp == 0 {
				continue
			}
			if ob.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
	return rows, sortErr
}

func (e *Executor) evalOrderByExpr(expr parser.Expr, row *rowWithValues, schema *storage.TableSchema, tableAliases map[string]*tableBinding, colNames []string) (*storage.Value, error) {
	if ident, ok := expr.(*parser.IdentExpr); ok {
		for i, name := range colNames {
			if strings.EqualFold(name, ident.Name) && i < len(row.values) {
				return row.values[i], nil
			}
		}
	}
	ctx := e.makeCtxForRow(row, schema, tableAliases)
	return EvalExpr(ctx, expr)
}

func applyLimitOffset(stmt *parser.SelectStmt, rows []*rowWithValues) []*rowWithValues {
	if stmt.Offset != nil {
		ctx := &EvalContext{}
		v, err := EvalExpr(ctx, stmt.Offset)
		if err == nil && v != nil {
			offset := int(v.Integer)
			if offset > len(rows) {
				offset = len(rows)
			}
			if offset > 0 {
				rows = rows[offset:]
			}
		}
	}
	if stmt.Limit != nil {
		ctx := &EvalContext{}
		v, err := EvalExpr(ctx, stmt.Limit)
		if err == nil && v != nil {
			limit := int(v.Integer)
			if limit >= 0 && limit < len(rows) {
				rows = rows[:limit]
			}
		}
	}
	return rows
}

func dedup(rows []*rowWithValues) []*rowWithValues {
	seen := make(map[string]bool)
	var result []*rowWithValues
	for _, row := range rows {
		parts := make([]string, len(row.values))
		for i, v := range row.values {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = v.String()
			}
		}
		key := strings.Join(parts, "\x00")
		if !seen[key] {
			seen[key] = true
			result = append(result, row)
		}
	}
	return result
}

func (e *Executor) execInsert(stmt *parser.InsertStmt) (*ResultSet, error) {
	onConflict := storage.ConflictAbort
	switch strings.ToUpper(stmt.Or) {
	case "REPLACE":
		onConflict = storage.ConflictReplace
	case "IGNORE":
		onConflict = storage.ConflictIgnore
	case "FAIL":
		onConflict = storage.ConflictFail
	case "ROLLBACK":
		onConflict = storage.ConflictRollback
	}
	if stmt.Select != nil {
		rows, _, err := e.evalSelect(stmt.Select, nil)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if err := e.table.InsertWithConflict(stmt.Table, stmt.Columns, row.values, onConflict); err != nil {
				return nil, err
			}
		}
		return &ResultSet{}, nil
	}
	for _, valExprs := range stmt.Values {
		ctx := &EvalContext{}
		vals := make([]*storage.Value, len(valExprs))
		for i, expr := range valExprs {
			v, err := EvalExpr(ctx, expr)
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		if err := e.table.InsertWithConflict(stmt.Table, stmt.Columns, vals, onConflict); err != nil {
			return nil, err
		}
	}
	return &ResultSet{}, nil
}

func (e *Executor) execUpdate(stmt *parser.UpdateStmt) (*ResultSet, error) {
	scans, err := e.table.Scan(stmt.Table)
	if err != nil {
		return nil, err
	}
	ts, ok := e.schema.GetTable(stmt.Table)
	if !ok {
		return nil, fmt.Errorf("table '%s' does not exist", stmt.Table)
	}
	for _, sr := range scans {
		ctx := NewEvalContext(sr.Row, ts)
		ctx.RowID = sr.RowID
		if stmt.Where != nil {
			v, err := EvalExpr(ctx, stmt.Where)
			if err != nil {
				return nil, err
			}
			if !IsTruthy(v) {
				continue
			}
		}
		updates := make(map[string]*storage.Value)
		for _, sc := range stmt.Sets {
			v, err := EvalExpr(ctx, sc.Value)
			if err != nil {
				return nil, err
			}
			updates[strings.ToLower(sc.Column)] = v
		}
		if err := e.table.Update(stmt.Table, sr.RowID, updates); err != nil {
			return nil, err
		}
	}
	return &ResultSet{}, nil
}

func (e *Executor) execDelete(stmt *parser.DeleteStmt) (*ResultSet, error) {
	scans, err := e.table.Scan(stmt.Table)
	if err != nil {
		return nil, err
	}
	ts, ok := e.schema.GetTable(stmt.Table)
	if !ok {
		return nil, fmt.Errorf("table '%s' does not exist", stmt.Table)
	}
	for _, sr := range scans {
		ctx := NewEvalContext(sr.Row, ts)
		ctx.RowID = sr.RowID
		if stmt.Where != nil {
			v, err := EvalExpr(ctx, stmt.Where)
			if err != nil {
				return nil, err
			}
			if !IsTruthy(v) {
				continue
			}
		}
		if _, err := e.table.Delete(stmt.Table, sr.RowID); err != nil {
			return nil, err
		}
	}
	return &ResultSet{}, nil
}

func (e *Executor) execCreateTable(stmt *parser.CreateTableStmt) (*ResultSet, error) {
	_, exists := e.schema.GetTable(stmt.Table)
	if exists {
		if stmt.IfNotExists {
			return &ResultSet{}, nil
		}
		return nil, fmt.Errorf("table '%s' already exists", stmt.Table)
	}
	ts := &storage.TableSchema{
		Name:        stmt.Table,
		NextAutoInc: 1,
	}
	for _, col := range stmt.Columns {
		colType := storage.NormalizeType(col.Type)
		c := storage.Column{
			Name:       col.Name,
			Type:       colType,
			NotNull:    col.NotNull,
			PrimaryKey: col.PrimaryKey,
			Unique:     col.Unique,
			Default:    col.Default,
			AutoInc:    col.AutoIncrement,
		}
		ts.Columns = append(ts.Columns, c)
	}
	for _, tc := range stmt.Constraints {
		if tc.Type == "PRIMARY KEY" {
			for i := range ts.Columns {
				for _, pkCol := range tc.Columns {
					if strings.EqualFold(ts.Columns[i].Name, pkCol) {
						ts.Columns[i].PrimaryKey = true
					}
				}
			}
		}
	}
	return &ResultSet{}, e.schema.CreateTable(ts)
}

func (e *Executor) execDropTable(stmt *parser.DropTableStmt) (*ResultSet, error) {
	_, exists := e.schema.GetTable(stmt.Table)
	if !exists {
		if stmt.IfExists {
			return &ResultSet{}, nil
		}
		return nil, fmt.Errorf("table '%s' does not exist", stmt.Table)
	}
	return &ResultSet{}, e.schema.DropTable(stmt.Table)
}

func (e *Executor) execCreateIndex(stmt *parser.CreateIndexStmt) (*ResultSet, error) {
	_, exists := e.schema.GetIndex(stmt.Name)
	if exists {
		if stmt.IfNotExists {
			return &ResultSet{}, nil
		}
		return nil, fmt.Errorf("index '%s' already exists", stmt.Name)
	}
	is := &storage.IndexSchema{
		Name:      stmt.Name,
		TableName: stmt.Table,
		Column:    stmt.Column,
		Unique:    stmt.Unique,
	}
	return &ResultSet{}, e.schema.CreateIndex(is)
}

func (e *Executor) execDropIndex(stmt *parser.DropIndexStmt) (*ResultSet, error) {
	_, exists := e.schema.GetIndex(stmt.Name)
	if !exists {
		if stmt.IfExists {
			return &ResultSet{}, nil
		}
		return nil, fmt.Errorf("index '%s' does not exist", stmt.Name)
	}
	return &ResultSet{}, e.schema.DropIndex(stmt.Name)
}

func (e *Executor) execCreateView(stmt *parser.CreateViewStmt) (*ResultSet, error) {
	_, exists := e.schema.GetView(stmt.Name)
	if exists {
		if stmt.IfNotExists {
			return &ResultSet{}, nil
		}
		return nil, fmt.Errorf("view '%s' already exists", stmt.Name)
	}
	selectSQL := sqlForView(stmt.Select)
	vs := &storage.ViewSchema{Name: stmt.Name, SQL: selectSQL}
	return &ResultSet{}, e.schema.CreateView(vs)
}

func sqlForView(stmt *parser.SelectStmt) string {
	if stmt == nil {
		return ""
	}
	return "SELECT 1"
}

func (e *Executor) execDropView(stmt *parser.DropViewStmt) (*ResultSet, error) {
	_, exists := e.schema.GetView(stmt.Name)
	if !exists {
		if stmt.IfExists {
			return &ResultSet{}, nil
		}
		return nil, fmt.Errorf("view '%s' does not exist", stmt.Name)
	}
	return &ResultSet{}, e.schema.DropView(stmt.Name)
}

func (e *Executor) execAlterTable(stmt *parser.AlterTableStmt) (*ResultSet, error) {
	switch action := stmt.Action.(type) {
	case *parser.AlterAddColumn:
		col := storage.Column{
			Name:       action.Column.Name,
			Type:       storage.NormalizeType(action.Column.Type),
			NotNull:    action.Column.NotNull,
			PrimaryKey: action.Column.PrimaryKey,
			Unique:     action.Column.Unique,
			Default:    action.Column.Default,
			AutoInc:    action.Column.AutoIncrement,
		}
		return &ResultSet{}, e.schema.AddColumn(stmt.Table, col)
	case *parser.AlterRenameTable:
		return &ResultSet{}, e.schema.RenameTable(stmt.Table, action.NewName)
	case *parser.AlterRenameColumn:
		return &ResultSet{}, e.schema.RenameColumn(stmt.Table, action.OldName, action.NewName)
	case *parser.AlterDropColumn:
		return &ResultSet{}, e.schema.DropColumn(stmt.Table, action.Column)
	}
	return nil, fmt.Errorf("unknown ALTER TABLE action")
}

func (e *Executor) execBegin(stmt *parser.BeginStmt) (*ResultSet, error) {
	if e.inTx {
		return nil, fmt.Errorf("already in a transaction")
	}
	e.tx = storage.NewTransaction(e.pager)
	e.inTx = true
	return &ResultSet{}, nil
}

func (e *Executor) execCommit() (*ResultSet, error) {
	if !e.inTx {
		return &ResultSet{}, e.pager.FlushAll()
	}
	err := e.tx.Commit()
	e.tx = nil
	e.inTx = false
	return &ResultSet{}, err
}

func (e *Executor) execRollback(stmt *parser.RollbackStmt) (*ResultSet, error) {
	if !e.inTx {
		return &ResultSet{}, nil
	}
	if stmt.To != "" {
		return &ResultSet{}, e.tx.RollbackToSavepoint(stmt.To)
	}
	err := e.tx.Rollback()
	e.tx = nil
	e.inTx = false
	return &ResultSet{}, err
}

func (e *Executor) execSavepoint(stmt *parser.SavepointStmt) (*ResultSet, error) {
	if !e.inTx {
		e.tx = storage.NewTransaction(e.pager)
		e.inTx = true
	}
	return &ResultSet{}, e.tx.Savepoint(stmt.Name)
}

func (e *Executor) execReleaseSavepoint(stmt *parser.ReleaseSavepointStmt) (*ResultSet, error) {
	if !e.inTx {
		return nil, fmt.Errorf("not in a transaction")
	}
	return &ResultSet{}, e.tx.ReleaseSavepoint(stmt.Name)
}

func (e *Executor) execShowTables() (*ResultSet, error) {
	tables := e.schema.ListTables()
	rs := &ResultSet{Columns: []string{"name"}}
	for _, ts := range tables {
		rs.Rows = append(rs.Rows, []string{ts.Name})
	}
	sort.Slice(rs.Rows, func(i, j int) bool {
		return rs.Rows[i][0] < rs.Rows[j][0]
	})
	views := e.schema.ListViews()
	for _, vs := range views {
		rs.Rows = append(rs.Rows, []string{vs.Name + " (view)"})
	}
	return rs, nil
}

func (e *Executor) execShowIndexes(stmt *parser.ShowIndexesStmt) (*ResultSet, error) {
	rs := &ResultSet{Columns: []string{"name", "table_name", "column", "unique"}}
	indexes := e.schema.ListIndexes(stmt.Table)
	for _, is := range indexes {
		unique := "0"
		if is.Unique {
			unique = "1"
		}
		rs.Rows = append(rs.Rows, []string{is.Name, is.TableName, is.Column, unique})
	}
	return rs, nil
}

func (e *Executor) execExplain(stmt *parser.ExplainStmt) (*ResultSet, error) {
	return &ResultSet{
		Columns: []string{"detail"},
		Rows:    [][]string{{fmt.Sprintf("EXPLAIN: %T", stmt.Inner)}},
	}, nil
}

func (e *Executor) execPragma(stmt *parser.PragmaStmt) (*ResultSet, error) {
	switch strings.ToLower(stmt.Name) {
	case "table_info":
		ts, ok := e.schema.GetTable(stmt.Value)
		if !ok {
			return nil, fmt.Errorf("table '%s' does not exist", stmt.Value)
		}
		rs := &ResultSet{Columns: []string{"cid", "name", "type", "notnull", "dflt_value", "pk"}}
		for i, col := range ts.Columns {
			pk, notNull := "0", "0"
			if col.PrimaryKey {
				pk = "1"
			}
			if col.NotNull {
				notNull = "1"
			}
			rs.Rows = append(rs.Rows, []string{fmt.Sprintf("%d", i), col.Name, string(col.Type), notNull, col.Default, pk})
		}
		return rs, nil
	case "table_list":
		rs := &ResultSet{Columns: []string{"schema", "name", "type", "ncol", "wr", "strict"}}
		for _, ts := range e.schema.ListTables() {
			rs.Rows = append(rs.Rows, []string{"main", ts.Name, "table", fmt.Sprintf("%d", len(ts.Columns)), "0", "0"})
		}
		return rs, nil
	case "foreign_key_list":
		return &ResultSet{Columns: []string{"id", "seq", "table", "from", "to", "on_update", "on_delete", "match"}}, nil
	case "index_list":
		ts, ok := e.schema.GetTable(stmt.Value)
		if !ok {
			return &ResultSet{Columns: []string{"seq", "name", "unique", "origin", "partial"}}, nil
		}
		rs := &ResultSet{Columns: []string{"seq", "name", "unique", "origin", "partial"}}
		for i, idxName := range ts.Indexes {
			is, ok := e.schema.GetIndex(idxName)
			if !ok {
				continue
			}
			unique := "0"
			if is.Unique {
				unique = "1"
			}
			rs.Rows = append(rs.Rows, []string{fmt.Sprintf("%d", i), idxName, unique, "c", "0"})
		}
		return rs, nil
	case "integrity_check", "quick_check":
		return &ResultSet{Columns: []string{stmt.Name}, Rows: [][]string{{"ok"}}}, nil
	case "journal_mode":
		return &ResultSet{Columns: []string{"journal_mode"}, Rows: [][]string{{"delete"}}}, nil
	case "wal_checkpoint":
		return &ResultSet{Columns: []string{"busy", "log", "checkpointed"}, Rows: [][]string{{"0", "0", "0"}}}, nil
	default:
		return &ResultSet{Columns: []string{stmt.Name}, Rows: [][]string{{"0"}}}, nil
	}
}

func (e *Executor) LastInsertRowID() int64 { return e.table.LastInsertRowID() }
func (e *Executor) Changes() int64         { return e.table.Changes() }
func (e *Executor) FlushAll() error        { return e.pager.FlushAll() }

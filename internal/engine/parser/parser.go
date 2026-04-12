package parser

import (
	"fmt"
	"strconv"
	"strings"
)

type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens}
}

func Parse(sql string) ([]Statement, error) {
	l := NewLexer(sql)
	tokens, err := l.Tokenize()
	if err != nil {
		return nil, err
	}
	p := NewParser(tokens)
	return p.ParseAll()
}

func (p *Parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peekAt(offset int) Token {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return Token{Type: TOKEN_EOF}
	}
	return p.tokens[idx]
}

func (p *Parser) advance() Token {
	tok := p.peek()
	if tok.Type != TOKEN_EOF {
		p.pos++
	}
	return tok
}

func (p *Parser) expect(tt TokenType) (Token, error) {
	tok := p.peek()
	if tok.Type != tt {
		return tok, fmt.Errorf("expected token %d, got %d (%q) at line %d col %d", tt, tok.Type, tok.Value, tok.Line, tok.Col)
	}
	return p.advance(), nil
}

func (p *Parser) expectIdent() (string, error) {
	tok := p.peek()
	switch tok.Type {
	case TOKEN_IDENT:
		return p.advance().Value, nil
	case KW_SELECT, KW_FROM, KW_WHERE, KW_AS, KW_JOIN, KW_ON, KW_SET,
		KW_TABLES, KW_INDEXES, KW_VIEW, KW_COLUMN, KW_ADD, KW_RENAME,
		KW_TO, KW_KEY, KW_INDEX, KW_TABLE, KW_REPLACE, KW_IGNORE,
		KW_ABORT, KW_FAIL, KW_CONFLICT, KW_ACTION, KW_CASCADE, KW_NO,
		KW_RESTRICT, KW_MATCH, KW_ROWID, KW_STORED, KW_VIRTUAL, KW_ALWAYS,
		KW_NOTHING, KW_DO, KW_RETURNING, KW_NULLS, KW_FIRST, KW_LAST,
		KW_RANGE, KW_ROWS, KW_GROUPS, KW_CURRENT, KW_PRECEDING, KW_FOLLOWING,
		KW_UNBOUNDED, KW_TIES, KW_EXCLUDE, KW_OTHERS, KW_OVER, KW_PARTITION,
		KW_FILTER, KW_WINDOW, KW_BEFORE, KW_AFTER, KW_INSTEAD, KW_OF,
		KW_FOR, KW_EACH, KW_ROW, KW_STATEMENT, KW_TRIGGER, KW_REFERENCES,
		KW_FOREIGN, KW_CHECK, KW_CONSTRAINT, KW_GENERATED, KW_DEFERRABLE,
		KW_INITIALLY, KW_DEFERRED, KW_IMMEDIATE, KW_EXCLUSIVE, KW_BEGIN,
		KW_COMMIT, KW_ROLLBACK, KW_TRANSACTION, KW_SAVEPOINT, KW_RELEASE,
		KW_PRAGMA, KW_VACUUM, KW_RECURSIVE, KW_OUTER, KW_FULL, KW_NATURAL,
		KW_CROSS, KW_USING, KW_RIGHT:
		return p.advance().Value, nil
	}
	return "", fmt.Errorf("expected identifier, got %q at line %d col %d", tok.Value, tok.Line, tok.Col)
}

func (p *Parser) ParseAll() ([]Statement, error) {
	var stmts []Statement
	for p.peek().Type != TOKEN_EOF {
		if p.peek().Type == TOKEN_SEMICOLON {
			p.advance()
			continue
		}
		stmt, err := p.ParseStatement()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, stmt)
		if p.peek().Type == TOKEN_SEMICOLON {
			p.advance()
		}
	}
	return stmts, nil
}

func (p *Parser) ParseStatement() (Statement, error) {
	tok := p.peek()
	switch tok.Type {
	case KW_SELECT:
		return p.parseSelect()
	case KW_WITH:
		return p.parseWith()
	case KW_INSERT:
		return p.parseInsert()
	case KW_UPDATE:
		return p.parseUpdate()
	case KW_DELETE:
		return p.parseDelete()
	case KW_CREATE:
		return p.parseCreate()
	case KW_DROP:
		return p.parseDrop()
	case KW_ALTER:
		return p.parseAlter()
	case KW_BEGIN:
		p.advance()
		mode := ""
		switch p.peek().Type {
		case KW_DEFERRED, KW_IMMEDIATE, KW_EXCLUSIVE:
			mode = p.advance().Value
		}
		if p.peek().Type == KW_TRANSACTION {
			p.advance()
		}
		return &BeginStmt{Mode: mode}, nil
	case KW_COMMIT:
		p.advance()
		if p.peek().Type == KW_TRANSACTION {
			p.advance()
		}
		return &CommitStmt{}, nil
	case KW_ROLLBACK:
		p.advance()
		if p.peek().Type == KW_TRANSACTION {
			p.advance()
		}
		toName := ""
		if p.peek().Type == KW_TO {
			p.advance()
			if p.peek().Type == KW_SAVEPOINT {
				p.advance()
			}
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			toName = name
		}
		return &RollbackStmt{To: toName}, nil
	case KW_SAVEPOINT:
		p.advance()
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return &SavepointStmt{Name: name}, nil
	case KW_RELEASE:
		p.advance()
		if p.peek().Type == KW_SAVEPOINT {
			p.advance()
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return &ReleaseSavepointStmt{Name: name}, nil
	case KW_SHOW:
		return p.parseShow()
	case KW_EXPLAIN:
		p.advance()
		if p.peek().Type == TOKEN_IDENT && strings.ToUpper(p.peek().Value) == "QUERY" {
			p.advance()
			if p.peek().Type == TOKEN_IDENT && strings.ToUpper(p.peek().Value) == "PLAN" {
				p.advance()
			}
		}
		inner, err := p.ParseStatement()
		if err != nil {
			return nil, err
		}
		return &ExplainStmt{Inner: inner}, nil
	case KW_PRAGMA:
		return p.parsePragma()
	case KW_VACUUM:
		p.advance()
		return &VacuumStmt{}, nil
	default:
		return nil, fmt.Errorf("unexpected token %q at line %d col %d", tok.Value, tok.Line, tok.Col)
	}
}

func (p *Parser) parseWith() (Statement, error) {
	p.advance()
	wc := &WithClause{}
	if p.peek().Type == KW_RECURSIVE {
		p.advance()
		wc.Recursive = true
	}
	for {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		cte := CTE{Name: name}
		if p.peek().Type == TOKEN_LPAREN {
			p.advance()
			for {
				col, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				cte.Columns = append(cte.Columns, col)
				if p.peek().Type == TOKEN_RPAREN {
					p.advance()
					break
				}
				if _, err := p.expect(TOKEN_COMMA); err != nil {
					return nil, err
				}
			}
		}
		if _, err := p.expect(KW_AS); err != nil {
			return nil, err
		}
		if _, err := p.expect(TOKEN_LPAREN); err != nil {
			return nil, err
		}
		sel, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		cte.Query = sel
		wc.CTEs = append(wc.CTEs, cte)
		if p.peek().Type != TOKEN_COMMA {
			break
		}
		p.advance()
	}
	sel, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	sel.With = wc
	return sel, nil
}

func (p *Parser) parseSelect() (*SelectStmt, error) {
	p.advance()
	stmt := &SelectStmt{}
	if p.peek().Type == KW_DISTINCT {
		p.advance()
		stmt.Distinct = true
	} else if p.peek().Type == KW_ALL {
		p.advance()
	}
	cols, err := p.parseSelectColumns()
	if err != nil {
		return nil, err
	}
	stmt.Columns = cols
	if p.peek().Type == KW_FROM {
		p.advance()
		refs, err := p.parseTableRefs()
		if err != nil {
			return nil, err
		}
		stmt.From = refs
		joins, err := p.parseJoins()
		if err != nil {
			return nil, err
		}
		stmt.Joins = joins
	}
	if p.peek().Type == KW_WHERE {
		p.advance()
		where, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}
	if p.peek().Type == KW_GROUP {
		p.advance()
		if _, err := p.expect(KW_BY); err != nil {
			return nil, err
		}
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			stmt.GroupBy = append(stmt.GroupBy, expr)
			if p.peek().Type != TOKEN_COMMA {
				break
			}
			p.advance()
		}
	}
	if p.peek().Type == KW_HAVING {
		p.advance()
		having, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Having = having
	}
	if p.peek().Type == KW_UNION || p.peek().Type == KW_INTERSECT || p.peek().Type == KW_EXCEPT {
		op := p.advance().Value
		all := false
		if p.peek().Type == KW_ALL {
			p.advance()
			all = true
		}
		right, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		stmt.Compound = &CompoundClause{Op: op, All: all, Right: right}
	}
	if p.peek().Type == KW_ORDER {
		p.advance()
		if _, err := p.expect(KW_BY); err != nil {
			return nil, err
		}
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			ob := OrderByExpr{Expr: expr}
			if p.peek().Type == KW_DESC {
				p.advance()
				ob.Desc = true
			} else if p.peek().Type == KW_ASC {
				p.advance()
			}
			if p.peek().Type == KW_NULLS {
				p.advance()
				if p.peek().Type == KW_FIRST {
					p.advance()
					ob.Nulls = "FIRST"
				} else if p.peek().Type == KW_LAST {
					p.advance()
					ob.Nulls = "LAST"
				}
			}
			stmt.OrderBy = append(stmt.OrderBy, ob)
			if p.peek().Type != TOKEN_COMMA {
				break
			}
			p.advance()
		}
	}
	if p.peek().Type == KW_LIMIT {
		p.advance()
		limit, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Limit = limit
	}
	if p.peek().Type == KW_OFFSET {
		p.advance()
		offset, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Offset = offset
	}
	return stmt, nil
}

func (p *Parser) parseJoins() ([]JoinClause, error) {
	var joins []JoinClause
	for {
		joinType := ""
		switch p.peek().Type {
		case KW_INNER:
			p.advance()
			joinType = "INNER"
		case KW_LEFT:
			p.advance()
			joinType = "LEFT"
			if p.peek().Type == KW_OUTER {
				p.advance()
			}
		case KW_RIGHT:
			p.advance()
			joinType = "RIGHT"
			if p.peek().Type == KW_OUTER {
				p.advance()
			}
		case KW_FULL:
			p.advance()
			joinType = "FULL"
			if p.peek().Type == KW_OUTER {
				p.advance()
			}
		case KW_CROSS:
			p.advance()
			joinType = "CROSS"
		case KW_NATURAL:
			p.advance()
			joinType = "NATURAL"
		case KW_JOIN:
			joinType = "INNER"
		default:
			return joins, nil
		}
		if p.peek().Type != KW_JOIN {
			return joins, fmt.Errorf("expected JOIN keyword")
		}
		p.advance()
		tableName, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		tref := TableRef{Name: tableName}
		if p.peek().Type == KW_AS {
			p.advance()
			alias, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			tref.Alias = alias
		} else if p.peek().Type == TOKEN_IDENT {
			tref.Alias = p.advance().Value
		}
		jc := JoinClause{Type: joinType, Table: tref}
		if p.peek().Type == KW_ON {
			p.advance()
			cond, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			jc.On = cond
		} else if p.peek().Type == KW_USING {
			p.advance()
			if _, err := p.expect(TOKEN_LPAREN); err != nil {
				return nil, err
			}
			for {
				col, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				jc.Using = append(jc.Using, col)
				if p.peek().Type == TOKEN_RPAREN {
					p.advance()
					break
				}
				if _, err := p.expect(TOKEN_COMMA); err != nil {
					return nil, err
				}
			}
		}
		joins = append(joins, jc)
	}
}

func (p *Parser) parseSelectColumns() ([]SelectColumn, error) {
	var cols []SelectColumn
	for {
		if p.peek().Type == TOKEN_STAR {
			p.advance()
			cols = append(cols, SelectColumn{Star: true})
		} else {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			col := SelectColumn{Expr: expr}
			if p.peek().Type == KW_AS {
				p.advance()
				alias, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				col.Alias = alias
			} else if p.peek().Type == TOKEN_IDENT {
				col.Alias = p.advance().Value
			}
			cols = append(cols, col)
		}
		if p.peek().Type != TOKEN_COMMA {
			break
		}
		p.advance()
	}
	return cols, nil
}

func (p *Parser) parseTableRefs() ([]TableRef, error) {
	var refs []TableRef
	for {
		if p.peek().Type == TOKEN_LPAREN {
			break
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		ref := TableRef{Name: name}
		if p.peek().Type == KW_AS {
			p.advance()
			alias, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			ref.Alias = alias
		} else if p.peek().Type == TOKEN_IDENT {
			ref.Alias = p.advance().Value
		}
		refs = append(refs, ref)
		if p.peek().Type != TOKEN_COMMA {
			break
		}
		next := p.peekAt(1)
		switch next.Type {
		case KW_INNER, KW_LEFT, KW_RIGHT, KW_FULL, KW_CROSS, KW_NATURAL, KW_JOIN:
			break
		}
		p.advance()
	}
	return refs, nil
}

func (p *Parser) parseInsert() (*InsertStmt, error) {
	p.advance()
	or := ""
	if p.peek().Type == KW_OR {
		p.advance()
		switch p.peek().Type {
		case KW_REPLACE:
			or = "REPLACE"
			p.advance()
		case KW_IGNORE:
			or = "IGNORE"
			p.advance()
		case KW_ABORT:
			or = "ABORT"
			p.advance()
		case KW_FAIL:
			or = "FAIL"
			p.advance()
		case KW_ROLLBACK:
			or = "ROLLBACK"
			p.advance()
		}
	} else if p.peek().Type == KW_REPLACE {
		p.advance()
		or = "REPLACE"
	}
	if _, err := p.expect(KW_INTO); err != nil {
		return nil, err
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt := &InsertStmt{Table: table, Or: or}
	if p.peek().Type == TOKEN_LPAREN {
		p.advance()
		for {
			col, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			stmt.Columns = append(stmt.Columns, col)
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
				break
			}
			if _, err := p.expect(TOKEN_COMMA); err != nil {
				return nil, err
			}
		}
	}
	if p.peek().Type == KW_SELECT {
		sel, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		stmt.Select = sel
		return stmt, nil
	}
	if _, err := p.expect(KW_VALUES); err != nil {
		return nil, err
	}
	for {
		if _, err := p.expect(TOKEN_LPAREN); err != nil {
			return nil, err
		}
		var row []Expr
		for {
			expr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			row = append(row, expr)
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
				break
			}
			if _, err := p.expect(TOKEN_COMMA); err != nil {
				return nil, err
			}
		}
		stmt.Values = append(stmt.Values, row)
		if p.peek().Type != TOKEN_COMMA {
			break
		}
		p.advance()
	}
	if p.peek().Type == KW_ON {
		p.advance()
		if _, err := p.expect(KW_CONFLICT); err != nil {
			return nil, err
		}
		p.advance()
		action := p.advance().Value
		stmt.OnConflict = ConflictClause{Action: action}
	}
	return stmt, nil
}

func (p *Parser) parseUpdate() (*UpdateStmt, error) {
	p.advance()
	or := ""
	if p.peek().Type == KW_OR {
		p.advance()
		switch p.peek().Type {
		case KW_REPLACE, KW_IGNORE, KW_ABORT, KW_FAIL, KW_ROLLBACK:
			or = p.advance().Value
		}
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(KW_SET); err != nil {
		return nil, err
	}
	stmt := &UpdateStmt{Table: table, Or: or}
	for {
		col, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TOKEN_EQ); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Sets = append(stmt.Sets, SetClause{Column: col, Value: val})
		if p.peek().Type != TOKEN_COMMA {
			break
		}
		p.advance()
	}
	if p.peek().Type == KW_WHERE {
		p.advance()
		where, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}
	return stmt, nil
}

func (p *Parser) parseDelete() (*DeleteStmt, error) {
	p.advance()
	if _, err := p.expect(KW_FROM); err != nil {
		return nil, err
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt := &DeleteStmt{Table: table}
	if p.peek().Type == KW_WHERE {
		p.advance()
		where, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}
	return stmt, nil
}

func (p *Parser) parseCreate() (Statement, error) {
	p.advance()
	temp := false
	if p.peek().Type == KW_TEMP || p.peek().Type == KW_TEMPORARY {
		p.advance()
		temp = true
	}
	switch p.peek().Type {
	case KW_TABLE:
		return p.parseCreateTable(temp)
	case KW_UNIQUE:
		p.advance()
		if _, err := p.expect(KW_INDEX); err != nil {
			return nil, err
		}
		return p.parseCreateIndexBody(true)
	case KW_INDEX:
		p.advance()
		return p.parseCreateIndexBody(false)
	case KW_VIEW:
		p.advance()
		return p.parseCreateView(temp)
	default:
		return nil, fmt.Errorf("expected TABLE, INDEX, or VIEW after CREATE, got %q", p.peek().Value)
	}
}

func (p *Parser) parseCreateTable(temp bool) (*CreateTableStmt, error) {
	p.advance()
	stmt := &CreateTableStmt{Temp: temp}
	if p.peek().Type == KW_IF {
		p.advance()
		if _, err := p.expect(KW_NOT); err != nil {
			return nil, err
		}
		if p.peek().Value == "EXISTS" || p.peek().Type == TOKEN_IDENT {
			p.advance()
		}
		stmt.IfNotExists = true
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt.Table = name
	if _, err := p.expect(TOKEN_LPAREN); err != nil {
		return nil, err
	}
	for {
		if p.peek().Type == KW_PRIMARY || p.peek().Type == KW_UNIQUE ||
			p.peek().Type == KW_CHECK || p.peek().Type == KW_FOREIGN ||
			p.peek().Type == KW_CONSTRAINT {
			tc, err := p.parseTableConstraint()
			if err != nil {
				return nil, err
			}
			stmt.Constraints = append(stmt.Constraints, tc)
		} else {
			col, err := p.parseColumnDef()
			if err != nil {
				return nil, err
			}
			stmt.Columns = append(stmt.Columns, col)
		}
		if p.peek().Type == TOKEN_RPAREN {
			p.advance()
			break
		}
		if _, err := p.expect(TOKEN_COMMA); err != nil {
			return nil, err
		}
		if p.peek().Type == TOKEN_RPAREN {
			p.advance()
			break
		}
	}
	return stmt, nil
}

func (p *Parser) parseTableConstraint() (TableConstraint, error) {
	tc := TableConstraint{}
	if p.peek().Type == KW_CONSTRAINT {
		p.advance()
		name, _ := p.expectIdent()
		tc.Name = name
	}
	switch p.peek().Type {
	case KW_PRIMARY:
		p.advance()
		p.expect(KW_KEY)
		p.expect(TOKEN_LPAREN)
		for {
			col, _ := p.expectIdent()
			tc.Columns = append(tc.Columns, col)
			if p.peek().Type == KW_ASC || p.peek().Type == KW_DESC {
				p.advance()
			}
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
				break
			}
			p.expect(TOKEN_COMMA)
		}
		tc.Type = "PRIMARY KEY"
	case KW_UNIQUE:
		p.advance()
		p.expect(TOKEN_LPAREN)
		for {
			col, _ := p.expectIdent()
			tc.Columns = append(tc.Columns, col)
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
				break
			}
			p.expect(TOKEN_COMMA)
		}
		tc.Type = "UNIQUE"
	case KW_CHECK:
		p.advance()
		p.expect(TOKEN_LPAREN)
		p.skipBalancedParens()
		tc.Type = "CHECK"
	case KW_FOREIGN:
		p.advance()
		p.expect(KW_KEY)
		p.expect(TOKEN_LPAREN)
		for {
			col, _ := p.expectIdent()
			tc.Columns = append(tc.Columns, col)
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
				break
			}
			p.expect(TOKEN_COMMA)
		}
		p.skipForeignKeyRef()
		tc.Type = "FOREIGN KEY"
	}
	return tc, nil
}

func (p *Parser) skipBalancedParens() {
	depth := 1
	p.expect(TOKEN_LPAREN)
	for depth > 0 && p.peek().Type != TOKEN_EOF {
		if p.peek().Type == TOKEN_LPAREN {
			depth++
		} else if p.peek().Type == TOKEN_RPAREN {
			depth--
		}
		p.advance()
	}
}

func (p *Parser) skipForeignKeyRef() {
	if p.peek().Type != KW_REFERENCES {
		return
	}
	p.advance()
	p.expectIdent()
	if p.peek().Type == TOKEN_LPAREN {
		p.advance()
		for p.peek().Type != TOKEN_RPAREN && p.peek().Type != TOKEN_EOF {
			p.advance()
		}
		if p.peek().Type == TOKEN_RPAREN {
			p.advance()
		}
	}
	for {
		if p.peek().Type == KW_ON {
			p.advance()
			p.advance()
			p.advance()
		} else if p.peek().Type == KW_MATCH {
			p.advance()
			p.advance()
		} else if p.peek().Type == KW_NOT || p.peek().Type == KW_DEFERRABLE {
			if p.peek().Type == KW_NOT {
				p.advance()
			}
			p.advance()
			if p.peek().Type == KW_INITIALLY {
				p.advance()
				p.advance()
			}
		} else {
			break
		}
	}
}

func (p *Parser) parseColumnDef() (ColumnDef, error) {
	name, err := p.expectIdent()
	if err != nil {
		return ColumnDef{}, err
	}
	typeName, err := p.parseTypeName()
	if err != nil {
		return ColumnDef{}, err
	}
	col := ColumnDef{Name: name, Type: typeName}
	for {
		tok := p.peek()
		switch tok.Type {
		case KW_PRIMARY:
			p.advance()
			if _, err := p.expect(KW_KEY); err != nil {
				return col, err
			}
			col.PrimaryKey = true
			if p.peek().Type == KW_ASC || p.peek().Type == KW_DESC {
				p.advance()
			}
			if p.peek().Type == KW_AUTOINCREMENT {
				p.advance()
				col.AutoIncrement = true
			}
		case KW_NOT:
			p.advance()
			if _, err := p.expect(KW_NULL); err != nil {
				return col, err
			}
			col.NotNull = true
			if p.peek().Type == KW_ON {
				p.advance()
				p.expect(KW_CONFLICT)
				p.advance()
			}
		case KW_UNIQUE:
			p.advance()
			col.Unique = true
			if p.peek().Type == KW_ON {
				p.advance()
				p.expect(KW_CONFLICT)
				p.advance()
			}
		case KW_DEFAULT:
			p.advance()
			defVal := p.parseDefaultValue()
			col.Default = defVal
		case KW_AUTOINCREMENT:
			p.advance()
			col.AutoIncrement = true
		case KW_CHECK:
			p.advance()
			p.skipBalancedParens()
		case KW_REFERENCES:
			p.skipForeignKeyRef()
		case KW_GENERATED, KW_AS:
			if tok.Type == KW_GENERATED {
				p.advance()
				p.expect(KW_ALWAYS)
			}
			p.expect(KW_AS)
			p.skipBalancedParens()
			if p.peek().Type == KW_STORED || p.peek().Type == KW_VIRTUAL {
				p.advance()
			}
		case KW_COLLATE:
			p.advance()
			p.expectIdent()
		default:
			return col, nil
		}
	}
}

func (p *Parser) parseDefaultValue() string {
	switch p.peek().Type {
	case TOKEN_LPAREN:
		var sb strings.Builder
		sb.WriteString("(")
		p.advance()
		depth := 1
		for depth > 0 && p.peek().Type != TOKEN_EOF {
			if p.peek().Type == TOKEN_LPAREN {
				depth++
			} else if p.peek().Type == TOKEN_RPAREN {
				depth--
				if depth == 0 {
					break
				}
			}
			sb.WriteString(p.advance().Value)
		}
		sb.WriteString(")")
		p.advance()
		return sb.String()
	case KW_NULL:
		p.advance()
		return "NULL"
	case KW_TRUE:
		p.advance()
		return "1"
	case KW_FALSE:
		p.advance()
		return "0"
	case TOKEN_MINUS:
		p.advance()
		val := p.advance().Value
		return "-" + val
	default:
		return p.advance().Value
	}
}

func (p *Parser) parseTypeName() (string, error) {
	tok := p.peek()
	switch tok.Type {
	case KW_INTEGER, KW_TEXT, KW_REAL, KW_BLOB:
		p.advance()
		name := tok.Value
		if p.peek().Type == TOKEN_LPAREN {
			p.advance()
			for p.peek().Type != TOKEN_RPAREN && p.peek().Type != TOKEN_EOF {
				p.advance()
			}
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
			}
		}
		return name, nil
	case TOKEN_IDENT:
		p.advance()
		name := tok.Value
		upper := strings.ToUpper(name)
		if upper == "UNSIGNED" {
			if p.peek().Type == TOKEN_IDENT && strings.ToUpper(p.peek().Value) == "BIG" {
				p.advance()
				if p.peek().Type == KW_INTEGER || (p.peek().Type == TOKEN_IDENT && strings.ToUpper(p.peek().Value) == "INT") {
					p.advance()
				}
			}
			return "INTEGER", nil
		}
		if upper == "DOUBLE" || upper == "VARYING" || upper == "NATIVE" {
			if p.peek().Type == TOKEN_IDENT {
				p.advance()
			}
		}
		if p.peek().Type == TOKEN_LPAREN {
			p.advance()
			for p.peek().Type != TOKEN_RPAREN && p.peek().Type != TOKEN_EOF {
				p.advance()
			}
			if p.peek().Type == TOKEN_RPAREN {
				p.advance()
			}
		}
		return name, nil
	default:
		return "TEXT", nil
	}
}

func (p *Parser) parseCreateIndexBody(unique bool) (*CreateIndexStmt, error) {
	stmt := &CreateIndexStmt{Unique: unique}
	if p.peek().Type == KW_IF {
		p.advance()
		if _, err := p.expect(KW_NOT); err != nil {
			return nil, err
		}
		p.advance()
		stmt.IfNotExists = true
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt.Name = name
	if _, err := p.expect(KW_ON); err != nil {
		return nil, err
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt.Table = table
	if _, err := p.expect(TOKEN_LPAREN); err != nil {
		return nil, err
	}
	col, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt.Column = col
	if p.peek().Type == KW_ASC || p.peek().Type == KW_DESC {
		p.advance()
	}
	for p.peek().Type != TOKEN_RPAREN && p.peek().Type != TOKEN_EOF {
		p.advance()
	}
	if _, err := p.expect(TOKEN_RPAREN); err != nil {
		return nil, err
	}
	if p.peek().Type == KW_WHERE {
		p.advance()
		for p.peek().Type != TOKEN_SEMICOLON && p.peek().Type != TOKEN_EOF {
			p.advance()
		}
	}
	return stmt, nil
}

func (p *Parser) parseCreateView(temp bool) (*CreateViewStmt, error) {
	stmt := &CreateViewStmt{Temp: temp}
	if p.peek().Type == KW_IF {
		p.advance()
		p.expect(KW_NOT)
		p.advance()
		stmt.IfNotExists = true
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt.Name = name
	if _, err := p.expect(KW_AS); err != nil {
		return nil, err
	}
	sel, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	stmt.Select = sel
	return stmt, nil
}

func (p *Parser) parseDrop() (Statement, error) {
	p.advance()
	switch p.peek().Type {
	case KW_TABLE:
		p.advance()
		ifExists := false
		if p.peek().Type == KW_IF {
			p.advance()
			p.advance()
			ifExists = true
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return &DropTableStmt{Table: name, IfExists: ifExists}, nil
	case KW_INDEX:
		p.advance()
		ifExists := false
		if p.peek().Type == KW_IF {
			p.advance()
			p.advance()
			ifExists = true
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return &DropIndexStmt{Name: name, IfExists: ifExists}, nil
	case KW_VIEW:
		p.advance()
		ifExists := false
		if p.peek().Type == KW_IF {
			p.advance()
			p.advance()
			ifExists = true
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return &DropViewStmt{Name: name, IfExists: ifExists}, nil
	default:
		return nil, fmt.Errorf("expected TABLE, INDEX, or VIEW after DROP")
	}
}

func (p *Parser) parseAlter() (Statement, error) {
	p.advance()
	if _, err := p.expect(KW_TABLE); err != nil {
		return nil, err
	}
	table, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	stmt := &AlterTableStmt{Table: table}
	switch p.peek().Type {
	case KW_ADD:
		p.advance()
		if p.peek().Type == KW_COLUMN {
			p.advance()
		}
		col, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		stmt.Action = &AlterAddColumn{Column: col}
	case KW_RENAME:
		p.advance()
		if p.peek().Type == KW_TO {
			p.advance()
			newName, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			stmt.Action = &AlterRenameTable{NewName: newName}
		} else {
			if p.peek().Type == KW_COLUMN {
				p.advance()
			}
			oldName, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(KW_TO); err != nil {
				return nil, err
			}
			newName, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			stmt.Action = &AlterRenameColumn{OldName: oldName, NewName: newName}
		}
	case KW_DROP:
		p.advance()
		if p.peek().Type == KW_COLUMN {
			p.advance()
		}
		colName, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		stmt.Action = &AlterDropColumn{Column: colName}
	default:
		return nil, fmt.Errorf("expected ADD, RENAME, or DROP after ALTER TABLE")
	}
	return stmt, nil
}

func (p *Parser) parseShow() (Statement, error) {
	p.advance()
	switch p.peek().Type {
	case KW_TABLES:
		p.advance()
		return &ShowTablesStmt{}, nil
	case KW_INDEXES:
		p.advance()
		if p.peek().Type == KW_FROM || p.peek().Type == KW_ON {
			p.advance()
		}
		table, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		return &ShowIndexesStmt{Table: table}, nil
	default:
		return nil, fmt.Errorf("expected TABLES or INDEXES after SHOW")
	}
}

func (p *Parser) parsePragma() (Statement, error) {
	p.advance()
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	val := ""
	if p.peek().Type == TOKEN_EQ {
		p.advance()
		val = p.advance().Value
	} else if p.peek().Type == TOKEN_LPAREN {
		p.advance()
		val = p.advance().Value
		p.expect(TOKEN_RPAREN)
	}
	return &PragmaStmt{Name: name, Value: val}, nil
}

func (p *Parser) parseExpr() (Expr, error) {
	return p.parseOr()
}

func (p *Parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == KW_OR {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == KW_AND {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseNot() (Expr, error) {
	if p.peek().Type == KW_NOT {
		p.advance()
		operand, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "NOT", Operand: operand}, nil
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() (Expr, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		switch tok.Type {
		case TOKEN_EQ, TOKEN_NEQ, TOKEN_LT, TOKEN_LTE, TOKEN_GT, TOKEN_GTE:
			op := p.advance().Value
			right, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: op, Left: left, Right: right}
		case KW_IS:
			p.advance()
			isNot := false
			if p.peek().Type == KW_NOT {
				p.advance()
				isNot = true
			}
			if p.peek().Type == KW_NULL {
				p.advance()
				left = &IsNullExpr{Operand: left, IsNot: isNot}
			} else if p.peek().Type == KW_TRUE || p.peek().Type == KW_FALSE {
				val := p.advance().Value
				var v Expr
				if strings.ToUpper(val) == "TRUE" {
					v = &LiteralExpr{Value: int64(1)}
				} else {
					v = &LiteralExpr{Value: int64(0)}
				}
				op := "="
				if isNot {
					op = "!="
				}
				left = &BinaryExpr{Op: op, Left: left, Right: v}
			} else {
				right, err := p.parseConcat()
				if err != nil {
					return nil, err
				}
				op := "="
				if isNot {
					op = "!="
				}
				left = &BinaryExpr{Op: op, Left: left, Right: right}
			}
		case KW_IN:
			p.advance()
			isNot := false
			if _, err := p.expect(TOKEN_LPAREN); err != nil {
				return nil, err
			}
			if p.peek().Type == KW_SELECT {
				sel, err := p.parseSelect()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(TOKEN_RPAREN); err != nil {
					return nil, err
				}
				left = &InExpr{Operand: left, Subquery: sel, IsNot: isNot}
			} else {
				var list []Expr
				for p.peek().Type != TOKEN_RPAREN && p.peek().Type != TOKEN_EOF {
					expr, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					list = append(list, expr)
					if p.peek().Type != TOKEN_COMMA {
						break
					}
					p.advance()
				}
				if _, err := p.expect(TOKEN_RPAREN); err != nil {
					return nil, err
				}
				left = &InExpr{Operand: left, List: list, IsNot: isNot}
			}
		case KW_NOT:
			p.advance()
			switch p.peek().Type {
			case KW_IN:
				p.advance()
				if _, err := p.expect(TOKEN_LPAREN); err != nil {
					return nil, err
				}
				if p.peek().Type == KW_SELECT {
					sel, err := p.parseSelect()
					if err != nil {
						return nil, err
					}
					if _, err := p.expect(TOKEN_RPAREN); err != nil {
						return nil, err
					}
					left = &InExpr{Operand: left, Subquery: sel, IsNot: true}
				} else {
					var list []Expr
					for p.peek().Type != TOKEN_RPAREN && p.peek().Type != TOKEN_EOF {
						expr, err := p.parseExpr()
						if err != nil {
							return nil, err
						}
						list = append(list, expr)
						if p.peek().Type != TOKEN_COMMA {
							break
						}
						p.advance()
					}
					if _, err := p.expect(TOKEN_RPAREN); err != nil {
						return nil, err
					}
					left = &InExpr{Operand: left, List: list, IsNot: true}
				}
			case KW_LIKE:
				p.advance()
				pattern, err := p.parseConcat()
				if err != nil {
					return nil, err
				}
				le := &LikeExpr{Operand: left, Pattern: pattern, IsNot: true}
				if p.peek().Type == KW_ESCAPE {
					p.advance()
					esc, err := p.parseConcat()
					if err != nil {
						return nil, err
					}
					le.Escape = esc
				}
				left = le
			case KW_GLOB:
				p.advance()
				pattern, err := p.parseConcat()
				if err != nil {
					return nil, err
				}
				left = &GlobExpr{Operand: left, Pattern: pattern, IsNot: true}
			case KW_BETWEEN:
				p.advance()
				low, err := p.parseConcat()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(KW_AND); err != nil {
					return nil, err
				}
				high, err := p.parseConcat()
				if err != nil {
					return nil, err
				}
				left = &BetweenExpr{Operand: left, Low: low, High: high, IsNot: true}
			default:
				operand, err := p.parseComparison()
				if err != nil {
					return nil, err
				}
				return &BinaryExpr{Op: "AND", Left: left, Right: &UnaryExpr{Op: "NOT", Operand: operand}}, nil
			}
		case KW_LIKE:
			p.advance()
			pattern, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			le := &LikeExpr{Operand: left, Pattern: pattern}
			if p.peek().Type == KW_ESCAPE {
				p.advance()
				esc, err := p.parseConcat()
				if err != nil {
					return nil, err
				}
				le.Escape = esc
			}
			left = le
		case KW_GLOB:
			p.advance()
			pattern, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			left = &GlobExpr{Operand: left, Pattern: pattern}
		case KW_BETWEEN:
			p.advance()
			low, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(KW_AND); err != nil {
				return nil, err
			}
			high, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			left = &BetweenExpr{Operand: left, Low: low, High: high}
		default:
			return left, nil
		}
	}
}

func (p *Parser) parseConcat() (Expr, error) {
	left, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TOKEN_PIPE_PIPE {
		p.advance()
		right, err := p.parseBitOr()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseBitOr() (Expr, error) {
	left, err := p.parseBitAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TOKEN_PIPE {
		p.advance()
		right, err := p.parseBitAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "|", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseBitAnd() (Expr, error) {
	left, err := p.parseBitShift()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TOKEN_AMPERSAND {
		p.advance()
		right, err := p.parseBitShift()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "&", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseBitShift() (Expr, error) {
	left, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TOKEN_LSHIFT || p.peek().Type == TOKEN_RSHIFT {
		op := p.advance().Value
		right, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseAddSub() (Expr, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TOKEN_PLUS || p.peek().Type == TOKEN_MINUS {
		op := p.advance().Value
		right, err := p.parseMulDiv()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseMulDiv() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == TOKEN_STAR || p.peek().Type == TOKEN_SLASH || p.peek().Type == TOKEN_PERCENT {
		op := p.advance().Value
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) parseUnary() (Expr, error) {
	switch p.peek().Type {
	case TOKEN_MINUS:
		p.advance()
		operand, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "-", Operand: operand}, nil
	case TOKEN_PLUS:
		p.advance()
		return p.parsePostfix()
	case TOKEN_TILDE:
		p.advance()
		operand, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "~", Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimaryExpr()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Type {
		case KW_IS:
			p.advance()
			isNot := false
			if p.peek().Type == KW_NOT {
				p.advance()
				isNot = true
			}
			if p.peek().Type == KW_NULL {
				p.advance()
				expr = &IsNullExpr{Operand: expr, IsNot: isNot}
			} else {
				right, err := p.parsePrimaryExpr()
				if err != nil {
					return nil, err
				}
				op := "="
				if isNot {
					op = "!="
				}
				expr = &BinaryExpr{Op: op, Left: expr, Right: right}
			}
		default:
			return expr, nil
		}
	}
}

func (p *Parser) parsePrimaryExpr() (Expr, error) {
	tok := p.peek()
	switch tok.Type {
	case TOKEN_NUMBER:
		p.advance()
		if strings.Contains(tok.Value, ".") || strings.ContainsAny(tok.Value, "eE") {
			f, err := strconv.ParseFloat(tok.Value, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number: %s", tok.Value)
			}
			return &LiteralExpr{Value: f}, nil
		}
		if strings.HasPrefix(strings.ToLower(tok.Value), "0x") {
			i, err := strconv.ParseInt(tok.Value[2:], 16, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid hex number: %s", tok.Value)
			}
			return &LiteralExpr{Value: i}, nil
		}
		i, err := strconv.ParseInt(tok.Value, 10, 64)
		if err != nil {
			f, err2 := strconv.ParseFloat(tok.Value, 64)
			if err2 != nil {
				return nil, fmt.Errorf("invalid number: %s", tok.Value)
			}
			return &LiteralExpr{Value: f}, nil
		}
		return &LiteralExpr{Value: i}, nil

	case TOKEN_STRING:
		p.advance()
		return &LiteralExpr{Value: tok.Value}, nil

	case KW_NULL:
		p.advance()
		return &LiteralExpr{IsNull: true}, nil

	case KW_TRUE:
		p.advance()
		return &LiteralExpr{Value: int64(1)}, nil

	case KW_FALSE:
		p.advance()
		return &LiteralExpr{Value: int64(0)}, nil

	case TOKEN_LPAREN:
		p.advance()
		if p.peek().Type == KW_SELECT {
			sel, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TOKEN_RPAREN); err != nil {
				return nil, err
			}
			return &SubqueryExpr{Query: sel}, nil
		}
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().Type == TOKEN_COMMA {
			vals := []Expr{expr}
			for p.peek().Type == TOKEN_COMMA {
				p.advance()
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				vals = append(vals, e)
			}
			if _, err := p.expect(TOKEN_RPAREN); err != nil {
				return nil, err
			}
			return &RowValueExpr{Values: vals}, nil
		}
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return expr, nil

	case KW_CASE:
		return p.parseCaseExpr()

	case KW_CAST:
		p.advance()
		if _, err := p.expect(TOKEN_LPAREN); err != nil {
			return nil, err
		}
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(KW_AS); err != nil {
			return nil, err
		}
		typeName, err := p.parseTypeName()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return &CastExpr{Expr: expr, Type: typeName}, nil

	case KW_EXISTS:
		p.advance()
		if _, err := p.expect(TOKEN_LPAREN); err != nil {
			return nil, err
		}
		sel, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return &ExistsExpr{Subquery: sel}, nil

	case KW_NOT:
		next := p.peekAt(1)
		if next.Type == KW_EXISTS {
			p.advance()
			p.advance()
			if _, err := p.expect(TOKEN_LPAREN); err != nil {
				return nil, err
			}
			sel, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TOKEN_RPAREN); err != nil {
				return nil, err
			}
			return &ExistsExpr{Subquery: sel, IsNot: true}, nil
		}
		p.advance()
		operand, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "NOT", Operand: operand}, nil

	case TOKEN_STAR:
		p.advance()
		return &StarExpr{}, nil

	case TOKEN_IDENT:
		name := p.advance().Value
		if p.peek().Type == TOKEN_DOT {
			p.advance()
			field, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			return &IdentExpr{Table: name, Name: field}, nil
		}
		if p.peek().Type == TOKEN_LPAREN {
			return p.parseFuncCall(name)
		}
		return &IdentExpr{Name: name}, nil

	default:
		if isIdentLike(tok.Type) {
			name := p.advance().Value
			if p.peek().Type == TOKEN_DOT {
				p.advance()
				field, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				return &IdentExpr{Table: name, Name: field}, nil
			}
			if p.peek().Type == TOKEN_LPAREN {
				return p.parseFuncCall(name)
			}
			return &IdentExpr{Name: name}, nil
		}
		return nil, fmt.Errorf("unexpected token %q at line %d col %d", tok.Value, tok.Line, tok.Col)
	}
}

func isIdentLike(tt TokenType) bool {
	switch tt {
	case KW_COUNT, KW_SUM, KW_AVG, KW_MIN, KW_MAX,
		KW_REPLACE, KW_IGNORE, KW_ROWID:
		return true
	}
	return false
}

func (p *Parser) parseFuncCall(name string) (Expr, error) {
	p.advance()
	fn := &FuncCallExpr{Name: name}
	if p.peek().Type == TOKEN_RPAREN {
		p.advance()
		return fn, nil
	}
	if p.peek().Type == TOKEN_STAR {
		p.advance()
		fn.Star = true
		if _, err := p.expect(TOKEN_RPAREN); err != nil {
			return nil, err
		}
		return fn, nil
	}
	if p.peek().Type == KW_DISTINCT {
		p.advance()
		fn.Distinct = true
	}
	for {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		fn.Args = append(fn.Args, arg)
		if p.peek().Type == TOKEN_RPAREN {
			p.advance()
			break
		}
		if _, err := p.expect(TOKEN_COMMA); err != nil {
			return nil, err
		}
	}
	return fn, nil
}

func (p *Parser) parseCaseExpr() (*CaseExpr, error) {
	p.advance()
	ce := &CaseExpr{}
	if p.peek().Type != KW_WHEN {
		base, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		ce.Base = base
	}
	for p.peek().Type == KW_WHEN {
		p.advance()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(KW_THEN); err != nil {
			return nil, err
		}
		then, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		ce.Whens = append(ce.Whens, WhenClause{Cond: cond, Then: then})
	}
	if p.peek().Type == KW_ELSE {
		p.advance()
		els, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		ce.Else = els
	}
	if _, err := p.expect(KW_END); err != nil {
		return nil, err
	}
	return ce, nil
}

const KW_ESCAPE = KW_MATCH
const KW_TYPE = KW_STATEMENT
const KW_COLLATE = KW_MATCH

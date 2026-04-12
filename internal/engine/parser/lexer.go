package parser

import (
	"fmt"
	"strings"
	"unicode"
)

type TokenType int

const (
	TOKEN_EOF TokenType = iota
	TOKEN_ILLEGAL
	TOKEN_IDENT
	TOKEN_NUMBER
	TOKEN_STRING
	TOKEN_COMMA
	TOKEN_SEMICOLON
	TOKEN_LPAREN
	TOKEN_RPAREN
	TOKEN_DOT
	TOKEN_STAR
	TOKEN_EQ
	TOKEN_NEQ
	TOKEN_LT
	TOKEN_LTE
	TOKEN_GT
	TOKEN_GTE
	TOKEN_PLUS
	TOKEN_MINUS
	TOKEN_SLASH
	TOKEN_PERCENT
	TOKEN_PIPE_PIPE
	TOKEN_AMPERSAND
	TOKEN_PIPE
	TOKEN_TILDE
	TOKEN_LSHIFT
	TOKEN_RSHIFT
	TOKEN_AND
	TOKEN_OR
	TOKEN_NOT

	KW_SELECT
	KW_FROM
	KW_WHERE
	KW_INSERT
	KW_INTO
	KW_VALUES
	KW_UPDATE
	KW_SET
	KW_DELETE
	KW_CREATE
	KW_DROP
	KW_TABLE
	KW_INDEX
	KW_ON
	KW_UNIQUE
	KW_PRIMARY
	KW_KEY
	KW_NULL
	KW_NOT
	KW_DEFAULT
	KW_AND
	KW_OR
	KW_IS
	KW_IN
	KW_LIKE
	KW_GLOB
	KW_BETWEEN
	KW_ORDER
	KW_BY
	KW_ASC
	KW_DESC
	KW_LIMIT
	KW_OFFSET
	KW_AS
	KW_INNER
	KW_LEFT
	KW_RIGHT
	KW_FULL
	KW_OUTER
	KW_CROSS
	KW_NATURAL
	KW_JOIN
	KW_USING
	KW_GROUP
	KW_HAVING
	KW_DISTINCT
	KW_ALL
	KW_EXISTS
	KW_BEGIN
	KW_COMMIT
	KW_ROLLBACK
	KW_TRANSACTION
	KW_SAVEPOINT
	KW_RELEASE
	KW_TO
	KW_IF
	KW_SHOW
	KW_TABLES
	KW_INDEXES
	KW_EXPLAIN
	KW_COUNT
	KW_SUM
	KW_AVG
	KW_MIN
	KW_MAX
	KW_INTEGER
	KW_TEXT
	KW_REAL
	KW_BLOB
	KW_AUTOINCREMENT
	KW_CASE
	KW_WHEN
	KW_THEN
	KW_ELSE
	KW_END
	KW_CAST
	KW_ALTER
	KW_ADD
	KW_RENAME
	KW_COLUMN
	KW_VIEW
	KW_TEMP
	KW_TEMPORARY
	KW_UNION
	KW_INTERSECT
	KW_EXCEPT
	KW_WITH
	KW_RECURSIVE
	KW_REPLACE
	KW_IGNORE
	KW_ABORT
	KW_FAIL
	KW_CONFLICT
	KW_OR_REPLACE
	KW_ROWID
	KW_PRAGMA
	KW_VACUUM
	KW_DEFERRED
	KW_IMMEDIATE
	KW_EXCLUSIVE
	KW_TRIGGER
	KW_BEFORE
	KW_AFTER
	KW_INSTEAD
	KW_OF
	KW_FOR
	KW_EACH
	KW_ROW
	KW_STATEMENT
	KW_MATCH
	KW_NO
	KW_ACTION
	KW_RESTRICT
	KW_CASCADE
	KW_SET_NULL
	KW_SET_DEFAULT
	KW_DEFERRABLE
	KW_INITIALLY
	KW_REFERENCES
	KW_FOREIGN
	KW_CHECK
	KW_CONSTRAINT
	KW_GENERATED
	KW_ALWAYS
	KW_STORED
	KW_VIRTUAL
	KW_NOTHING
	KW_DO
	KW_RETURNING
	KW_WINDOW
	KW_OVER
	KW_PARTITION
	KW_FILTER
	KW_NULLS
	KW_FIRST
	KW_LAST
	KW_RANGE
	KW_ROWS
	KW_GROUPS
	KW_CURRENT
	KW_PRECEDING
	KW_FOLLOWING
	KW_UNBOUNDED
	KW_TIES
	KW_EXCLUDE
	KW_OTHERS
	KW_TRUE
	KW_FALSE
)

var keywords = map[string]TokenType{
	"SELECT":        KW_SELECT,
	"FROM":          KW_FROM,
	"WHERE":         KW_WHERE,
	"INSERT":        KW_INSERT,
	"INTO":          KW_INTO,
	"VALUES":        KW_VALUES,
	"UPDATE":        KW_UPDATE,
	"SET":           KW_SET,
	"DELETE":        KW_DELETE,
	"CREATE":        KW_CREATE,
	"DROP":          KW_DROP,
	"TABLE":         KW_TABLE,
	"INDEX":         KW_INDEX,
	"ON":            KW_ON,
	"UNIQUE":        KW_UNIQUE,
	"PRIMARY":       KW_PRIMARY,
	"KEY":           KW_KEY,
	"NULL":          KW_NULL,
	"NOT":           KW_NOT,
	"DEFAULT":       KW_DEFAULT,
	"AND":           KW_AND,
	"OR":            KW_OR,
	"IS":            KW_IS,
	"IN":            KW_IN,
	"LIKE":          KW_LIKE,
	"GLOB":          KW_GLOB,
	"BETWEEN":       KW_BETWEEN,
	"ORDER":         KW_ORDER,
	"BY":            KW_BY,
	"ASC":           KW_ASC,
	"DESC":          KW_DESC,
	"LIMIT":         KW_LIMIT,
	"OFFSET":        KW_OFFSET,
	"AS":            KW_AS,
	"INNER":         KW_INNER,
	"LEFT":          KW_LEFT,
	"RIGHT":         KW_RIGHT,
	"FULL":          KW_FULL,
	"OUTER":         KW_OUTER,
	"CROSS":         KW_CROSS,
	"NATURAL":       KW_NATURAL,
	"JOIN":          KW_JOIN,
	"USING":         KW_USING,
	"GROUP":         KW_GROUP,
	"HAVING":        KW_HAVING,
	"DISTINCT":      KW_DISTINCT,
	"ALL":           KW_ALL,
	"EXISTS":        KW_EXISTS,
	"BEGIN":         KW_BEGIN,
	"COMMIT":        KW_COMMIT,
	"ROLLBACK":      KW_ROLLBACK,
	"TRANSACTION":   KW_TRANSACTION,
	"SAVEPOINT":     KW_SAVEPOINT,
	"RELEASE":       KW_RELEASE,
	"TO":            KW_TO,
	"IF":            KW_IF,
	"SHOW":          KW_SHOW,
	"TABLES":        KW_TABLES,
	"INDEXES":       KW_INDEXES,
	"EXPLAIN":       KW_EXPLAIN,
	"COUNT":         KW_COUNT,
	"SUM":           KW_SUM,
	"AVG":           KW_AVG,
	"MIN":           KW_MIN,
	"MAX":           KW_MAX,
	"INTEGER":       KW_INTEGER,
	"INT":           KW_INTEGER,
	"TEXT":          KW_TEXT,
	"VARCHAR":       KW_TEXT,
	"REAL":          KW_REAL,
	"FLOAT":         KW_REAL,
	"DOUBLE":        KW_REAL,
	"BLOB":          KW_BLOB,
	"AUTOINCREMENT": KW_AUTOINCREMENT,
	"CASE":          KW_CASE,
	"WHEN":          KW_WHEN,
	"THEN":          KW_THEN,
	"ELSE":          KW_ELSE,
	"END":           KW_END,
	"CAST":          KW_CAST,
	"ALTER":         KW_ALTER,
	"ADD":           KW_ADD,
	"RENAME":        KW_RENAME,
	"COLUMN":        KW_COLUMN,
	"VIEW":          KW_VIEW,
	"TEMP":          KW_TEMP,
	"TEMPORARY":     KW_TEMPORARY,
	"UNION":         KW_UNION,
	"INTERSECT":     KW_INTERSECT,
	"EXCEPT":        KW_EXCEPT,
	"WITH":          KW_WITH,
	"RECURSIVE":     KW_RECURSIVE,
	"REPLACE":       KW_REPLACE,
	"IGNORE":        KW_IGNORE,
	"ABORT":         KW_ABORT,
	"FAIL":          KW_FAIL,
	"CONFLICT":      KW_CONFLICT,
	"ROWID":         KW_ROWID,
	"PRAGMA":        KW_PRAGMA,
	"VACUUM":        KW_VACUUM,
	"DEFERRED":      KW_DEFERRED,
	"IMMEDIATE":     KW_IMMEDIATE,
	"EXCLUSIVE":     KW_EXCLUSIVE,
	"TRIGGER":       KW_TRIGGER,
	"BEFORE":        KW_BEFORE,
	"AFTER":         KW_AFTER,
	"INSTEAD":       KW_INSTEAD,
	"OF":            KW_OF,
	"FOR":           KW_FOR,
	"EACH":          KW_EACH,
	"ROW":           KW_ROW,
	"STATEMENT":     KW_STATEMENT,
	"MATCH":         KW_MATCH,
	"NO":            KW_NO,
	"ACTION":        KW_ACTION,
	"RESTRICT":      KW_RESTRICT,
	"CASCADE":       KW_CASCADE,
	"DEFERRABLE":    KW_DEFERRABLE,
	"INITIALLY":     KW_INITIALLY,
	"REFERENCES":    KW_REFERENCES,
	"FOREIGN":       KW_FOREIGN,
	"CHECK":         KW_CHECK,
	"CONSTRAINT":    KW_CONSTRAINT,
	"GENERATED":     KW_GENERATED,
	"ALWAYS":        KW_ALWAYS,
	"STORED":        KW_STORED,
	"VIRTUAL":       KW_VIRTUAL,
	"NOTHING":       KW_NOTHING,
	"DO":            KW_DO,
	"RETURNING":     KW_RETURNING,
	"WINDOW":        KW_WINDOW,
	"OVER":          KW_OVER,
	"PARTITION":     KW_PARTITION,
	"FILTER":        KW_FILTER,
	"NULLS":         KW_NULLS,
	"FIRST":         KW_FIRST,
	"LAST":          KW_LAST,
	"RANGE":         KW_RANGE,
	"ROWS":          KW_ROWS,
	"GROUPS":        KW_GROUPS,
	"CURRENT":       KW_CURRENT,
	"PRECEDING":     KW_PRECEDING,
	"FOLLOWING":     KW_FOLLOWING,
	"UNBOUNDED":     KW_UNBOUNDED,
	"TIES":          KW_TIES,
	"EXCLUDE":       KW_EXCLUDE,
	"OTHERS":        KW_OTHERS,
	"TRUE":          KW_TRUE,
	"FALSE":         KW_FALSE,
	"BOOLEAN":       KW_INTEGER,
	"BOOL":          KW_INTEGER,
	"NUMERIC":       KW_REAL,
	"DECIMAL":       KW_REAL,
	"CHAR":          KW_TEXT,
	"NCHAR":         KW_TEXT,
	"NVARCHAR":      KW_TEXT,
	"CLOB":          KW_TEXT,
	"TINYINT":       KW_INTEGER,
	"SMALLINT":      KW_INTEGER,
	"MEDIUMINT":     KW_INTEGER,
	"BIGINT":        KW_INTEGER,
	"UNSIGNED":      KW_INTEGER,
	"DATE":          KW_TEXT,
	"DATETIME":      KW_TEXT,
	"TIMESTAMP":     KW_TEXT,
}

type Token struct {
	Type  TokenType
	Value string
	Line  int
	Col   int
}

func (t Token) String() string {
	return fmt.Sprintf("Token(%d, %q)", t.Type, t.Value)
}

type Lexer struct {
	input []rune
	pos   int
	line  int
	col   int
}

func NewLexer(input string) *Lexer {
	return &Lexer{input: []rune(input), line: 1, col: 1}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	return l.input[l.pos]
}

func (l *Lexer) peekAt(offset int) rune {
	idx := l.pos + offset
	if idx >= len(l.input) {
		return 0
	}
	return l.input[idx]
}

func (l *Lexer) advance() rune {
	if l.pos >= len(l.input) {
		return 0
	}
	ch := l.input[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) && unicode.IsSpace(l.input[l.pos]) {
		l.advance()
	}
}

func (l *Lexer) skipLineComment() {
	for l.pos < len(l.input) && l.input[l.pos] != '\n' {
		l.advance()
	}
}

func (l *Lexer) skipBlockComment() error {
	l.advance()
	l.advance()
	for l.pos < len(l.input) {
		if l.input[l.pos] == '*' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '/' {
			l.advance()
			l.advance()
			return nil
		}
		l.advance()
	}
	return fmt.Errorf("unterminated block comment")
}

func (l *Lexer) readString(quote rune) (Token, error) {
	line, col := l.line, l.col
	l.advance()
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == quote {
			if l.pos+1 < len(l.input) && l.input[l.pos+1] == quote {
				sb.WriteRune(quote)
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			return Token{Type: TOKEN_STRING, Value: sb.String(), Line: line, Col: col}, nil
		}
		if ch == '\\' && quote == '"' {
			l.advance()
			if l.pos < len(l.input) {
				esc := l.advance()
				switch esc {
				case 'n':
					sb.WriteByte('\n')
				case 't':
					sb.WriteByte('\t')
				case 'r':
					sb.WriteByte('\r')
				case '\\':
					sb.WriteByte('\\')
				case '"':
					sb.WriteByte('"')
				default:
					sb.WriteRune('\\')
					sb.WriteRune(esc)
				}
			}
			continue
		}
		sb.WriteRune(ch)
		l.advance()
	}
	return Token{}, fmt.Errorf("unterminated string literal")
}

func (l *Lexer) readNumber() Token {
	line, col := l.line, l.col
	var sb strings.Builder
	for l.pos < len(l.input) && (unicode.IsDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
		sb.WriteRune(l.advance())
	}
	if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
		sb.WriteRune(l.advance())
		if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
			sb.WriteRune(l.advance())
		}
		for l.pos < len(l.input) && unicode.IsDigit(l.input[l.pos]) {
			sb.WriteRune(l.advance())
		}
	}
	if l.pos < len(l.input) && (l.input[l.pos] == 'x' || l.input[l.pos] == 'X') && sb.String() == "0" {
		sb.WriteRune(l.advance())
		for l.pos < len(l.input) && isHexDigit(l.input[l.pos]) {
			sb.WriteRune(l.advance())
		}
	}
	return Token{Type: TOKEN_NUMBER, Value: sb.String(), Line: line, Col: col}
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func (l *Lexer) readIdent() Token {
	line, col := l.line, l.col
	var sb strings.Builder
	for l.pos < len(l.input) && (unicode.IsLetter(l.input[l.pos]) || unicode.IsDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
		sb.WriteRune(l.advance())
	}
	word := sb.String()
	upper := strings.ToUpper(word)
	if tt, ok := keywords[upper]; ok {
		return Token{Type: tt, Value: upper, Line: line, Col: col}
	}
	return Token{Type: TOKEN_IDENT, Value: word, Line: line, Col: col}
}

func (l *Lexer) readQuotedIdent(quote rune) (Token, error) {
	line, col := l.line, l.col
	l.advance()
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == quote {
			l.advance()
			return Token{Type: TOKEN_IDENT, Value: sb.String(), Line: line, Col: col}, nil
		}
		sb.WriteRune(ch)
		l.advance()
	}
	return Token{}, fmt.Errorf("unterminated quoted identifier")
}

func (l *Lexer) Tokenize() ([]Token, error) {
	var tokens []Token
	for {
		l.skipWhitespace()
		if l.pos >= len(l.input) {
			tokens = append(tokens, Token{Type: TOKEN_EOF})
			break
		}
		ch := l.input[l.pos]
		line, col := l.line, l.col
		if ch == '-' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '-' {
			l.skipLineComment()
			continue
		}
		if ch == '/' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '*' {
			if err := l.skipBlockComment(); err != nil {
				return nil, err
			}
			continue
		}
		switch ch {
		case '\'', '"':
			tok, err := l.readString(ch)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
		case '`':
			tok, err := l.readQuotedIdent('`')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
		case '[':
			tok, err := l.readQuotedIdent(']')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
		case ',':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_COMMA, Value: ",", Line: line, Col: col})
		case ';':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_SEMICOLON, Value: ";", Line: line, Col: col})
		case '(':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_LPAREN, Value: "(", Line: line, Col: col})
		case ')':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_RPAREN, Value: ")", Line: line, Col: col})
		case '.':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_DOT, Value: ".", Line: line, Col: col})
		case '*':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_STAR, Value: "*", Line: line, Col: col})
		case '=':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_EQ, Value: "=", Line: line, Col: col})
		case '<':
			l.advance()
			if l.pos < len(l.input) && l.input[l.pos] == '=' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_LTE, Value: "<=", Line: line, Col: col})
			} else if l.pos < len(l.input) && l.input[l.pos] == '>' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_NEQ, Value: "<>", Line: line, Col: col})
			} else if l.pos < len(l.input) && l.input[l.pos] == '<' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_LSHIFT, Value: "<<", Line: line, Col: col})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_LT, Value: "<", Line: line, Col: col})
			}
		case '>':
			l.advance()
			if l.pos < len(l.input) && l.input[l.pos] == '=' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_GTE, Value: ">=", Line: line, Col: col})
			} else if l.pos < len(l.input) && l.input[l.pos] == '>' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_RSHIFT, Value: ">>", Line: line, Col: col})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_GT, Value: ">", Line: line, Col: col})
			}
		case '!':
			l.advance()
			if l.pos < len(l.input) && l.input[l.pos] == '=' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_NEQ, Value: "!=", Line: line, Col: col})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_ILLEGAL, Value: "!", Line: line, Col: col})
			}
		case '+':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_PLUS, Value: "+", Line: line, Col: col})
		case '-':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_MINUS, Value: "-", Line: line, Col: col})
		case '/':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_SLASH, Value: "/", Line: line, Col: col})
		case '%':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_PERCENT, Value: "%", Line: line, Col: col})
		case '&':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_AMPERSAND, Value: "&", Line: line, Col: col})
		case '~':
			l.advance()
			tokens = append(tokens, Token{Type: TOKEN_TILDE, Value: "~", Line: line, Col: col})
		case '|':
			l.advance()
			if l.pos < len(l.input) && l.input[l.pos] == '|' {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_PIPE_PIPE, Value: "||", Line: line, Col: col})
			} else {
				tokens = append(tokens, Token{Type: TOKEN_PIPE, Value: "|", Line: line, Col: col})
			}
		default:
			if unicode.IsDigit(ch) {
				tokens = append(tokens, l.readNumber())
			} else if unicode.IsLetter(ch) || ch == '_' {
				tokens = append(tokens, l.readIdent())
			} else {
				l.advance()
				tokens = append(tokens, Token{Type: TOKEN_ILLEGAL, Value: string(ch), Line: line, Col: col})
			}
		}
	}
	return tokens, nil
}

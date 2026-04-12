package executor

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tyowk/sqlgo/parser"
	"github.com/tyowk/sqlgo/storage"
)

type EvalContext struct {
	Row    *storage.Row
	Schema *storage.TableSchema
	Tables map[string]*tableBinding
	RowID  int64
}

type tableBinding struct {
	Schema *storage.TableSchema
	Row    *storage.Row
	RowID  int64
}

func NewEvalContext(row *storage.Row, schema *storage.TableSchema) *EvalContext {
	return &EvalContext{Row: row, Schema: schema}
}

func EvalExpr(ctx *EvalContext, expr parser.Expr) (*storage.Value, error) {
	switch e := expr.(type) {
	case *parser.LiteralExpr:
		if e.IsNull {
			return storage.NullValue, nil
		}
		switch v := e.Value.(type) {
		case int64:
			return storage.IntValue(v), nil
		case float64:
			return storage.RealValue(v), nil
		case string:
			return storage.TextValue(v), nil
		}
		return storage.NullValue, nil

	case *parser.IdentExpr:
		return resolveIdent(ctx, e)

	case *parser.StarExpr:
		return storage.IntValue(1), nil

	case *parser.UnaryExpr:
		return evalUnary(ctx, e)

	case *parser.BinaryExpr:
		return evalBinary(ctx, e)

	case *parser.IsNullExpr:
		val, err := EvalExpr(ctx, e.Operand)
		if err != nil {
			return nil, err
		}
		isNull := val.Type == storage.ValNull
		if e.IsNot {
			isNull = !isNull
		}
		if isNull {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil

	case *parser.InExpr:
		return evalIn(ctx, e)

	case *parser.LikeExpr:
		return evalLike(ctx, e)

	case *parser.GlobExpr:
		return evalGlob(ctx, e)

	case *parser.BetweenExpr:
		return evalBetween(ctx, e)

	case *parser.FuncCallExpr:
		return evalFunc(ctx, e)

	case *parser.CaseExpr:
		return evalCase(ctx, e)

	case *parser.CastExpr:
		return evalCast(ctx, e)

	case *parser.ExistsExpr:
		return storage.IntValue(0), nil

	case *parser.SubqueryExpr:
		return storage.NullValue, nil

	case *parser.RowValueExpr:
		if len(e.Values) > 0 {
			return EvalExpr(ctx, e.Values[0])
		}
		return storage.NullValue, nil
	}
	return nil, fmt.Errorf("unknown expression type: %T", expr)
}

func resolveIdent(ctx *EvalContext, e *parser.IdentExpr) (*storage.Value, error) {
	if strings.ToUpper(e.Name) == "ROWID" && e.Table == "" {
		if ctx != nil {
			return storage.IntValue(ctx.RowID), nil
		}
		return storage.NullValue, nil
	}
	if ctx == nil || ctx.Row == nil {
		return storage.NullValue, nil
	}
	schema := ctx.Schema
	if e.Table != "" && ctx.Tables != nil {
		binding, ok := ctx.Tables[strings.ToLower(e.Table)]
		if ok {
			schema = binding.Schema
			colIdx := schema.ColumnIndex(e.Name)
			if colIdx < 0 {
				return storage.NullValue, nil
			}
			if colIdx >= len(binding.Row.Values) {
				return storage.NullValue, nil
			}
			return binding.Row.Values[colIdx], nil
		}
	}
	if schema == nil {
		return storage.NullValue, nil
	}
	colIdx := schema.ColumnIndex(e.Name)
	if colIdx < 0 {
		if ctx.Tables != nil {
			for _, binding := range ctx.Tables {
				ci := binding.Schema.ColumnIndex(e.Name)
				if ci >= 0 && ci < len(binding.Row.Values) {
					return binding.Row.Values[ci], nil
				}
			}
		}
		return storage.NullValue, nil
	}
	if colIdx >= len(ctx.Row.Values) {
		return storage.NullValue, nil
	}
	return ctx.Row.Values[colIdx], nil
}

func evalUnary(ctx *EvalContext, e *parser.UnaryExpr) (*storage.Value, error) {
	val, err := EvalExpr(ctx, e.Operand)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case "-":
		switch val.Type {
		case storage.ValInteger:
			return storage.IntValue(-val.Integer), nil
		case storage.ValReal:
			return storage.RealValue(-val.Real), nil
		case storage.ValNull:
			return storage.NullValue, nil
		}
	case "+":
		return val, nil
	case "NOT":
		if val.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if val.IsTrue() {
			return storage.IntValue(0), nil
		}
		return storage.IntValue(1), nil
	case "~":
		switch val.Type {
		case storage.ValInteger:
			return storage.IntValue(^val.Integer), nil
		}
		return storage.IntValue(^int64(val.ToFloat())), nil
	}
	return storage.NullValue, nil
}

func evalBinary(ctx *EvalContext, e *parser.BinaryExpr) (*storage.Value, error) {
	if e.Op == "AND" {
		left, err := EvalExpr(ctx, e.Left)
		if err != nil {
			return nil, err
		}
		if !left.IsTrue() && left.Type != storage.ValNull {
			return storage.IntValue(0), nil
		}
		right, err := EvalExpr(ctx, e.Right)
		if err != nil {
			return nil, err
		}
		if left.Type == storage.ValNull || right.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if left.IsTrue() && right.IsTrue() {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	}
	if e.Op == "OR" {
		left, err := EvalExpr(ctx, e.Left)
		if err != nil {
			return nil, err
		}
		if left.IsTrue() {
			return storage.IntValue(1), nil
		}
		right, err := EvalExpr(ctx, e.Right)
		if err != nil {
			return nil, err
		}
		if right.IsTrue() {
			return storage.IntValue(1), nil
		}
		if left.Type == storage.ValNull || right.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.IntValue(0), nil
	}
	left, err := EvalExpr(ctx, e.Left)
	if err != nil {
		return nil, err
	}
	right, err := EvalExpr(ctx, e.Right)
	if err != nil {
		return nil, err
	}
	if left.Type == storage.ValNull || right.Type == storage.ValNull {
		switch e.Op {
		case "=", "!=", "<>", "<", "<=", ">", ">=":
			return storage.NullValue, nil
		}
	}
	switch e.Op {
	case "+":
		return arith(left, right, func(a, b float64) float64 { return a + b },
			func(a, b int64) int64 { return a + b })
	case "-":
		return arith(left, right, func(a, b float64) float64 { return a - b },
			func(a, b int64) int64 { return a - b })
	case "*":
		return arith(left, right, func(a, b float64) float64 { return a * b },
			func(a, b int64) int64 { return a * b })
	case "/":
		if (right.Type == storage.ValInteger && right.Integer == 0) ||
			(right.Type == storage.ValReal && right.Real == 0) {
			return storage.NullValue, nil
		}
		return arith(left, right, func(a, b float64) float64 { return a / b },
			func(a, b int64) int64 { return a / b })
	case "%":
		if right.Type == storage.ValInteger && right.Integer == 0 {
			return storage.NullValue, nil
		}
		if left.Type == storage.ValInteger && right.Type == storage.ValInteger {
			return storage.IntValue(left.Integer % right.Integer), nil
		}
		return storage.RealValue(math.Mod(left.ToFloat(), right.ToFloat())), nil
	case "||":
		if left.Type == storage.ValNull || right.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.TextValue(left.String() + right.String()), nil
	case "&":
		return storage.IntValue(int64(left.ToFloat()) & int64(right.ToFloat())), nil
	case "|":
		return storage.IntValue(int64(left.ToFloat()) | int64(right.ToFloat())), nil
	case "<<":
		return storage.IntValue(int64(left.ToFloat()) << uint(right.ToFloat())), nil
	case ">>":
		return storage.IntValue(int64(left.ToFloat()) >> uint(right.ToFloat())), nil
	case "=":
		if left.Compare(right) == 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	case "!=", "<>":
		if left.Compare(right) != 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	case "<":
		if left.Compare(right) < 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	case "<=":
		if left.Compare(right) <= 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	case ">":
		if left.Compare(right) > 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	case ">=":
		if left.Compare(right) >= 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil
	}
	return storage.NullValue, fmt.Errorf("unknown operator: %s", e.Op)
}

func arith(left, right *storage.Value, fop func(float64, float64) float64, iop func(int64, int64) int64) (*storage.Value, error) {
	if left.Type == storage.ValInteger && right.Type == storage.ValInteger {
		return storage.IntValue(iop(left.Integer, right.Integer)), nil
	}
	return storage.RealValue(fop(left.ToFloat(), right.ToFloat())), nil
}

func evalIn(ctx *EvalContext, e *parser.InExpr) (*storage.Value, error) {
	val, err := EvalExpr(ctx, e.Operand)
	if err != nil {
		return nil, err
	}
	if val.Type == storage.ValNull {
		return storage.NullValue, nil
	}
	for _, item := range e.List {
		itemVal, err := EvalExpr(ctx, item)
		if err != nil {
			return nil, err
		}
		if val.Compare(itemVal) == 0 {
			if e.IsNot {
				return storage.IntValue(0), nil
			}
			return storage.IntValue(1), nil
		}
	}
	if e.IsNot {
		return storage.IntValue(1), nil
	}
	return storage.IntValue(0), nil
}

func evalLike(ctx *EvalContext, e *parser.LikeExpr) (*storage.Value, error) {
	val, err := EvalExpr(ctx, e.Operand)
	if err != nil {
		return nil, err
	}
	pat, err := EvalExpr(ctx, e.Pattern)
	if err != nil {
		return nil, err
	}
	if val.Type == storage.ValNull || pat.Type == storage.ValNull {
		return storage.NullValue, nil
	}
	escape := '\\'
	if e.Escape != nil {
		escVal, err := EvalExpr(ctx, e.Escape)
		if err != nil {
			return nil, err
		}
		if escVal.Type != storage.ValNull && len(escVal.Text) > 0 {
			r, _ := utf8.DecodeRuneInString(escVal.Text)
			escape = r
		}
	}
	matched := sqlLikeMatch(strings.ToLower(val.String()), strings.ToLower(pat.String()), escape)
	if e.IsNot {
		matched = !matched
	}
	if matched {
		return storage.IntValue(1), nil
	}
	return storage.IntValue(0), nil
}

func sqlLikeMatch(str, pattern string, escape rune) bool {
	si, pi := 0, 0
	srunes := []rune(str)
	prunes := []rune(pattern)
	for pi < len(prunes) {
		pc := prunes[pi]
		if pc == escape && pi+1 < len(prunes) {
			pi++
			if si >= len(srunes) || srunes[si] != prunes[pi] {
				return false
			}
			si++
			pi++
			continue
		}
		switch pc {
		case '%':
			if pi+1 == len(prunes) {
				return true
			}
			for i := si; i <= len(srunes); i++ {
				if sqlLikeMatch(string(srunes[i:]), string(prunes[pi+1:]), escape) {
					return true
				}
			}
			return false
		case '_':
			if si >= len(srunes) {
				return false
			}
			si++
			pi++
		default:
			if si >= len(srunes) || srunes[si] != pc {
				return false
			}
			si++
			pi++
		}
	}
	return si == len(srunes)
}

func evalGlob(ctx *EvalContext, e *parser.GlobExpr) (*storage.Value, error) {
	val, err := EvalExpr(ctx, e.Operand)
	if err != nil {
		return nil, err
	}
	pat, err := EvalExpr(ctx, e.Pattern)
	if err != nil {
		return nil, err
	}
	if val.Type == storage.ValNull || pat.Type == storage.ValNull {
		return storage.NullValue, nil
	}
	matched := globMatch(val.String(), pat.String())
	if e.IsNot {
		matched = !matched
	}
	if matched {
		return storage.IntValue(1), nil
	}
	return storage.IntValue(0), nil
}

func globMatch(str, pattern string) bool {
	srunes := []rune(str)
	prunes := []rune(pattern)
	return globMatchRunes(srunes, prunes)
}

func globMatchRunes(str, pattern []rune) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(str); i++ {
				if globMatchRunes(str[i:], pattern[1:]) {
					return true
				}
			}
			return false
		case '?':
			if len(str) == 0 {
				return false
			}
			str = str[1:]
			pattern = pattern[1:]
		case '[':
			if len(str) == 0 {
				return false
			}
			i := 1
			negate := false
			if i < len(pattern) && pattern[i] == '^' {
				negate = true
				i++
			}
			matched := false
			for i < len(pattern) && pattern[i] != ']' {
				if i+2 < len(pattern) && pattern[i+1] == '-' {
					if str[0] >= pattern[i] && str[0] <= pattern[i+2] {
						matched = true
					}
					i += 3
				} else {
					if str[0] == pattern[i] {
						matched = true
					}
					i++
				}
			}
			if i < len(pattern) {
				i++
			}
			if matched == negate {
				return false
			}
			str = str[1:]
			pattern = pattern[i:]
		default:
			if len(str) == 0 || str[0] != pattern[0] {
				return false
			}
			str = str[1:]
			pattern = pattern[1:]
		}
	}
	return len(str) == 0
}

func evalBetween(ctx *EvalContext, e *parser.BetweenExpr) (*storage.Value, error) {
	val, err := EvalExpr(ctx, e.Operand)
	if err != nil {
		return nil, err
	}
	low, err := EvalExpr(ctx, e.Low)
	if err != nil {
		return nil, err
	}
	high, err := EvalExpr(ctx, e.High)
	if err != nil {
		return nil, err
	}
	if val.Type == storage.ValNull {
		return storage.NullValue, nil
	}
	inRange := val.Compare(low) >= 0 && val.Compare(high) <= 0
	if e.IsNot {
		inRange = !inRange
	}
	if inRange {
		return storage.IntValue(1), nil
	}
	return storage.IntValue(0), nil
}

func evalCase(ctx *EvalContext, e *parser.CaseExpr) (*storage.Value, error) {
	var base *storage.Value
	if e.Base != nil {
		v, err := EvalExpr(ctx, e.Base)
		if err != nil {
			return nil, err
		}
		base = v
	}
	for _, when := range e.Whens {
		var matched bool
		if base != nil {
			cond, err := EvalExpr(ctx, when.Cond)
			if err != nil {
				return nil, err
			}
			matched = base.Compare(cond) == 0
		} else {
			cond, err := EvalExpr(ctx, when.Cond)
			if err != nil {
				return nil, err
			}
			matched = IsTruthy(cond)
		}
		if matched {
			return EvalExpr(ctx, when.Then)
		}
	}
	if e.Else != nil {
		return EvalExpr(ctx, e.Else)
	}
	return storage.NullValue, nil
}

func evalCast(ctx *EvalContext, e *parser.CastExpr) (*storage.Value, error) {
	val, err := EvalExpr(ctx, e.Expr)
	if err != nil {
		return nil, err
	}
	colType := storage.NormalizeType(e.Type)
	return storage.CoerceValue(val, colType)
}

func evalFunc(ctx *EvalContext, e *parser.FuncCallExpr) (*storage.Value, error) {
	fn := strings.ToUpper(e.Name)
	switch fn {
	case "UPPER":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.TextValue(strings.ToUpper(v.String())), nil

	case "LOWER":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.TextValue(strings.ToLower(v.String())), nil

	case "LENGTH":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if v.Type == storage.ValBlob {
			return storage.IntValue(int64(len(v.Blob))), nil
		}
		return storage.IntValue(int64(utf8.RuneCountInString(v.String()))), nil

	case "ABS":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		switch v.Type {
		case storage.ValNull:
			return storage.NullValue, nil
		case storage.ValInteger:
			if v.Integer < 0 {
				return storage.IntValue(-v.Integer), nil
			}
			return v, nil
		case storage.ValReal:
			return storage.RealValue(math.Abs(v.Real)), nil
		}
		return storage.NullValue, nil

	case "COALESCE":
		for _, arg := range e.Args {
			v, err := EvalExpr(ctx, arg)
			if err != nil {
				return nil, err
			}
			if v.Type != storage.ValNull {
				return v, nil
			}
		}
		return storage.NullValue, nil

	case "IFNULL", "NVL":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type != storage.ValNull {
			return v, nil
		}
		return EvalExpr(ctx, e.Args[1])

	case "NULLIF":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		v1, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		v2, err := EvalExpr(ctx, e.Args[1])
		if err != nil {
			return nil, err
		}
		if v1.Compare(v2) == 0 {
			return storage.NullValue, nil
		}
		return v1, nil

	case "IIF":
		if len(e.Args) < 3 {
			return storage.NullValue, nil
		}
		cond, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if IsTruthy(cond) {
			return EvalExpr(ctx, e.Args[1])
		}
		return EvalExpr(ctx, e.Args[2])

	case "SUBSTR", "SUBSTRING":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		str, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if str.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		start, err := EvalExpr(ctx, e.Args[1])
		if err != nil {
			return nil, err
		}
		s := []rune(str.String())
		st := int(start.Integer) - 1
		if start.Integer < 0 {
			st = len(s) + int(start.Integer)
		}
		if st < 0 {
			st = 0
		}
		if st >= len(s) {
			return storage.TextValue(""), nil
		}
		if len(e.Args) >= 3 {
			length, err := EvalExpr(ctx, e.Args[2])
			if err != nil {
				return nil, err
			}
			end := st + int(length.Integer)
			if end > len(s) {
				end = len(s)
			}
			if end < st {
				end = st
			}
			return storage.TextValue(string(s[st:end])), nil
		}
		return storage.TextValue(string(s[st:])), nil

	case "TRIM":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if len(e.Args) >= 2 {
			chars, _ := EvalExpr(ctx, e.Args[1])
			return storage.TextValue(strings.Trim(v.String(), chars.String())), nil
		}
		return storage.TextValue(strings.TrimSpace(v.String())), nil

	case "LTRIM":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if len(e.Args) >= 2 {
			chars, _ := EvalExpr(ctx, e.Args[1])
			return storage.TextValue(strings.TrimLeft(v.String(), chars.String())), nil
		}
		return storage.TextValue(strings.TrimLeft(v.String(), " \t\n\r")), nil

	case "RTRIM":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if len(e.Args) >= 2 {
			chars, _ := EvalExpr(ctx, e.Args[1])
			return storage.TextValue(strings.TrimRight(v.String(), chars.String())), nil
		}
		return storage.TextValue(strings.TrimRight(v.String(), " \t\n\r")), nil

	case "REPLACE":
		if len(e.Args) < 3 {
			return storage.NullValue, nil
		}
		str, _ := EvalExpr(ctx, e.Args[0])
		old, _ := EvalExpr(ctx, e.Args[1])
		newStr, _ := EvalExpr(ctx, e.Args[2])
		if str.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.TextValue(strings.ReplaceAll(str.String(), old.String(), newStr.String())), nil

	case "ROUND":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		precision := int64(0)
		if len(e.Args) >= 2 {
			p, _ := EvalExpr(ctx, e.Args[1])
			precision = p.Integer
		}
		factor := math.Pow(10, float64(precision))
		return storage.RealValue(math.Round(v.ToFloat()*factor) / factor), nil

	case "FLOOR":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.RealValue(math.Floor(v.ToFloat())), nil

	case "CEIL", "CEILING":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.RealValue(math.Ceil(v.ToFloat())), nil

	case "TYPEOF":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, err := EvalExpr(ctx, e.Args[0])
		if err != nil {
			return nil, err
		}
		switch v.Type {
		case storage.ValNull:
			return storage.TextValue("null"), nil
		case storage.ValInteger:
			return storage.TextValue("integer"), nil
		case storage.ValReal:
			return storage.TextValue("real"), nil
		case storage.ValText:
			return storage.TextValue("text"), nil
		case storage.ValBlob:
			return storage.TextValue("blob"), nil
		}

	case "HEX":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if v.Type == storage.ValBlob {
			var sb strings.Builder
			for _, b := range v.Blob {
				sb.WriteString(fmt.Sprintf("%02X", b))
			}
			return storage.TextValue(sb.String()), nil
		}
		b := []byte(v.String())
		var sb strings.Builder
		for _, by := range b {
			sb.WriteString(fmt.Sprintf("%02X", by))
		}
		return storage.TextValue(sb.String()), nil

	case "UNHEX":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		s := v.String()
		if len(s)%2 != 0 {
			return storage.NullValue, nil
		}
		buf := make([]byte, len(s)/2)
		for i := 0; i < len(s); i += 2 {
			b, err := strconv.ParseUint(s[i:i+2], 16, 8)
			if err != nil {
				return storage.NullValue, nil
			}
			buf[i/2] = byte(b)
		}
		return storage.BlobValue(buf), nil

	case "CHAR":
		var sb strings.Builder
		for _, arg := range e.Args {
			v, _ := EvalExpr(ctx, arg)
			sb.WriteRune(rune(v.Integer))
		}
		return storage.TextValue(sb.String()), nil

	case "UNICODE":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull || len(v.Text) == 0 {
			return storage.NullValue, nil
		}
		r, _ := utf8.DecodeRuneInString(v.Text)
		return storage.IntValue(int64(r)), nil

	case "INSTR":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		haystack, _ := EvalExpr(ctx, e.Args[0])
		needle, _ := EvalExpr(ctx, e.Args[1])
		if haystack.Type == storage.ValNull || needle.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		idx := strings.Index(haystack.String(), needle.String())
		if idx < 0 {
			return storage.IntValue(0), nil
		}
		return storage.IntValue(int64(utf8.RuneCountInString(haystack.String()[:idx]) + 1)), nil

	case "QUOTE":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.TextValue("NULL"), nil
		}
		if v.Type == storage.ValText {
			escaped := strings.ReplaceAll(v.Text, "'", "''")
			return storage.TextValue("'" + escaped + "'"), nil
		}
		return storage.TextValue(v.String()), nil

	case "PRINTF", "FORMAT":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		fmtv, _ := EvalExpr(ctx, e.Args[0])
		if fmtv.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		result := sqlPrintf(fmtv.String(), e.Args[1:], ctx)
		return storage.TextValue(result), nil

	case "RANDOM":
		return storage.IntValue(rand.Int63()), nil

	case "RANDOMBLOB":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		n, _ := EvalExpr(ctx, e.Args[0])
		size := int(n.Integer)
		if size <= 0 {
			return storage.BlobValue([]byte{}), nil
		}
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = byte(rand.Intn(256))
		}
		return storage.BlobValue(buf), nil

	case "ZEROBLOB":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		n, _ := EvalExpr(ctx, e.Args[0])
		return storage.BlobValue(make([]byte, int(n.Integer))), nil

	case "DATE":
		return storage.TextValue(time.Now().Format("2006-01-02")), nil

	case "TIME":
		return storage.TextValue(time.Now().Format("15:04:05")), nil

	case "DATETIME":
		if len(e.Args) == 0 {
			return storage.TextValue(time.Now().Format("2006-01-02 15:04:05")), nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if strings.ToLower(v.String()) == "now" {
			return storage.TextValue(time.Now().Format("2006-01-02 15:04:05")), nil
		}
		return storage.TextValue(v.String()), nil

	case "JULIANDAY":
		now := time.Now()
		j := 2440587.5 + float64(now.Unix())/86400.0
		return storage.RealValue(j), nil

	case "UNIXEPOCH", "STRFTIME":
		if fn == "UNIXEPOCH" {
			return storage.IntValue(time.Now().Unix()), nil
		}
		if len(e.Args) < 2 {
			return storage.TextValue(time.Now().Format("2006-01-02 15:04:05")), nil
		}
		fmtArg, _ := EvalExpr(ctx, e.Args[0])
		timeArg, _ := EvalExpr(ctx, e.Args[1])
		layout := sqliteStrftimeToGo(fmtArg.String())
		if strings.ToLower(timeArg.String()) == "now" {
			return storage.TextValue(time.Now().Format(layout)), nil
		}
		return storage.TextValue(timeArg.String()), nil

	case "SQLITE_VERSION", "SQLGO_VERSION":
		return storage.TextValue("sqlgo-2.0"), nil

	case "LAST_INSERT_ROWID":
		return storage.IntValue(0), nil

	case "CHANGES":
		return storage.IntValue(0), nil

	case "TOTAL_CHANGES":
		return storage.IntValue(0), nil

	case "MAX":
		if len(e.Args) == 0 {
			return storage.NullValue, nil
		}
		if len(e.Args) == 1 {
			return EvalExpr(ctx, e.Args[0])
		}
		result, _ := EvalExpr(ctx, e.Args[0])
		for _, arg := range e.Args[1:] {
			v, _ := EvalExpr(ctx, arg)
			if v.Type != storage.ValNull && (result.Type == storage.ValNull || v.Compare(result) > 0) {
				result = v
			}
		}
		return result, nil

	case "MIN":
		if len(e.Args) == 0 {
			return storage.NullValue, nil
		}
		if len(e.Args) == 1 {
			return EvalExpr(ctx, e.Args[0])
		}
		result, _ := EvalExpr(ctx, e.Args[0])
		for _, arg := range e.Args[1:] {
			v, _ := EvalExpr(ctx, arg)
			if v.Type != storage.ValNull && (result.Type == storage.ValNull || v.Compare(result) < 0) {
				result = v
			}
		}
		return result, nil

	case "COUNT":
		return storage.IntValue(0), nil

	case "SUM", "TOTAL":
		return storage.IntValue(0), nil

	case "AVG":
		return storage.NullValue, nil

	case "GROUP_CONCAT":
		return storage.NullValue, nil

	case "SIGN":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		f := v.ToFloat()
		if f < 0 {
			return storage.IntValue(-1), nil
		} else if f > 0 {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil

	case "POW", "POWER":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		base, _ := EvalExpr(ctx, e.Args[0])
		exp, _ := EvalExpr(ctx, e.Args[1])
		return storage.RealValue(math.Pow(base.ToFloat(), exp.ToFloat())), nil

	case "SQRT":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		return storage.RealValue(math.Sqrt(v.ToFloat())), nil

	case "LOG", "LOG2", "LOG10":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		switch fn {
		case "LOG2":
			return storage.RealValue(math.Log2(v.ToFloat())), nil
		case "LOG10":
			return storage.RealValue(math.Log10(v.ToFloat())), nil
		default:
			if len(e.Args) >= 2 {
				base, _ := EvalExpr(ctx, e.Args[0])
				val, _ := EvalExpr(ctx, e.Args[1])
				return storage.RealValue(math.Log(val.ToFloat()) / math.Log(base.ToFloat())), nil
			}
			return storage.RealValue(math.Log(v.ToFloat())), nil
		}

	case "EXP":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		return storage.RealValue(math.Exp(v.ToFloat())), nil

	case "SIN", "COS", "TAN", "ASIN", "ACOS", "ATAN", "ATAN2":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		switch fn {
		case "SIN":
			return storage.RealValue(math.Sin(v.ToFloat())), nil
		case "COS":
			return storage.RealValue(math.Cos(v.ToFloat())), nil
		case "TAN":
			return storage.RealValue(math.Tan(v.ToFloat())), nil
		case "ASIN":
			return storage.RealValue(math.Asin(v.ToFloat())), nil
		case "ACOS":
			return storage.RealValue(math.Acos(v.ToFloat())), nil
		case "ATAN":
			return storage.RealValue(math.Atan(v.ToFloat())), nil
		case "ATAN2":
			if len(e.Args) >= 2 {
				v2, _ := EvalExpr(ctx, e.Args[1])
				return storage.RealValue(math.Atan2(v.ToFloat(), v2.ToFloat())), nil
			}
			return storage.RealValue(math.Atan(v.ToFloat())), nil
		}

	case "PI":
		return storage.RealValue(math.Pi), nil

	case "DEGREES":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		return storage.RealValue(v.ToFloat() * 180 / math.Pi), nil

	case "RADIANS":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		return storage.RealValue(v.ToFloat() * math.Pi / 180), nil

	case "CONCAT", "CONCAT_WS":
		if fn == "CONCAT_WS" {
			if len(e.Args) < 1 {
				return storage.NullValue, nil
			}
			sep, _ := EvalExpr(ctx, e.Args[0])
			parts := []string{}
			for _, arg := range e.Args[1:] {
				v, _ := EvalExpr(ctx, arg)
				if v.Type != storage.ValNull {
					parts = append(parts, v.String())
				}
			}
			return storage.TextValue(strings.Join(parts, sep.String())), nil
		}
		var sb strings.Builder
		for _, arg := range e.Args {
			v, _ := EvalExpr(ctx, arg)
			if v.Type == storage.ValNull {
				return storage.NullValue, nil
			}
			sb.WriteString(v.String())
		}
		return storage.TextValue(sb.String()), nil

	case "LPAD":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		str, _ := EvalExpr(ctx, e.Args[0])
		lenVal, _ := EvalExpr(ctx, e.Args[1])
		padChar := " "
		if len(e.Args) >= 3 {
			pc, _ := EvalExpr(ctx, e.Args[2])
			padChar = pc.String()
		}
		s := str.String()
		n := int(lenVal.Integer)
		for len(s) < n {
			s = padChar + s
		}
		return storage.TextValue(s[:n]), nil

	case "RPAD":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		str, _ := EvalExpr(ctx, e.Args[0])
		lenVal, _ := EvalExpr(ctx, e.Args[1])
		padChar := " "
		if len(e.Args) >= 3 {
			pc, _ := EvalExpr(ctx, e.Args[2])
			padChar = pc.String()
		}
		s := str.String()
		n := int(lenVal.Integer)
		for len(s) < n {
			s = s + padChar
		}
		return storage.TextValue(s[:n]), nil

	case "REPEAT":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		str, _ := EvalExpr(ctx, e.Args[0])
		times, _ := EvalExpr(ctx, e.Args[1])
		return storage.TextValue(strings.Repeat(str.String(), int(times.Integer))), nil

	case "REVERSE":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		str, _ := EvalExpr(ctx, e.Args[0])
		runes := []rune(str.String())
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return storage.TextValue(string(runes)), nil

	case "SPACE":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		n, _ := EvalExpr(ctx, e.Args[0])
		return storage.TextValue(strings.Repeat(" ", int(n.Integer))), nil

	case "CHAR_LENGTH", "CHARACTER_LENGTH":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		return storage.IntValue(int64(utf8.RuneCountInString(v.String()))), nil

	case "OCTET_LENGTH":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.NullValue, nil
		}
		if v.Type == storage.ValBlob {
			return storage.IntValue(int64(len(v.Blob))), nil
		}
		return storage.IntValue(int64(len(v.String()))), nil

	case "POSITION":
		if len(e.Args) < 2 {
			return storage.NullValue, nil
		}
		needle, _ := EvalExpr(ctx, e.Args[0])
		haystack, _ := EvalExpr(ctx, e.Args[1])
		idx := strings.Index(haystack.String(), needle.String())
		if idx < 0 {
			return storage.IntValue(0), nil
		}
		return storage.IntValue(int64(idx + 1)), nil

	case "ISNULL":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type == storage.ValNull {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil

	case "NOTNULL", "IFNOTNULL":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if v.Type != storage.ValNull {
			return storage.IntValue(1), nil
		}
		return storage.IntValue(0), nil

	case "NOT":
		if len(e.Args) < 1 {
			return storage.NullValue, nil
		}
		v, _ := EvalExpr(ctx, e.Args[0])
		if IsTruthy(v) {
			return storage.IntValue(0), nil
		}
		return storage.IntValue(1), nil
	}
	return storage.NullValue, fmt.Errorf("unknown function: %s", e.Name)
}

func sqliteStrftimeToGo(format string) string {
	r := strings.NewReplacer(
		"%Y", "2006",
		"%m", "01",
		"%d", "02",
		"%H", "15",
		"%M", "04",
		"%S", "05",
		"%f", "05.000",
		"%j", "002",
	)
	return r.Replace(format)
}

func sqlPrintf(format string, args []parser.Expr, ctx *EvalContext) string {
	var sb strings.Builder
	argIdx := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			sb.WriteByte(format[i])
			continue
		}
		i++
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			sb.WriteByte('%')
			continue
		}
		var v *storage.Value
		if argIdx < len(args) {
			v, _ = EvalExpr(ctx, args[argIdx])
			argIdx++
		} else {
			v = storage.NullValue
		}
		switch format[i] {
		case 'd', 'i':
			sb.WriteString(strconv.FormatInt(v.Integer, 10))
		case 'f':
			sb.WriteString(strconv.FormatFloat(v.ToFloat(), 'f', -1, 64))
		case 'g':
			sb.WriteString(strconv.FormatFloat(v.ToFloat(), 'g', -1, 64))
		case 's':
			sb.WriteString(v.String())
		case 'q':
			escaped := strings.ReplaceAll(v.String(), "'", "''")
			sb.WriteString("'" + escaped + "'")
		default:
			sb.WriteByte('%')
			sb.WriteByte(format[i])
		}
	}
	return sb.String()
}

func IsTruthy(v *storage.Value) bool {
	if v == nil || v.Type == storage.ValNull {
		return false
	}
	return v.IsTrue()
}

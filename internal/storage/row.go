package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

type ValueType byte

const (
	ValNull    ValueType = 0
	ValInteger ValueType = 1
	ValReal    ValueType = 2
	ValText    ValueType = 3
	ValBlob    ValueType = 4
)

type Value struct {
	Type    ValueType
	Integer int64
	Real    float64
	Text    string
	Blob    []byte
}

var NullValue = &Value{Type: ValNull}

func IntValue(v int64) *Value    { return &Value{Type: ValInteger, Integer: v} }
func RealValue(v float64) *Value { return &Value{Type: ValReal, Real: v} }
func TextValue(v string) *Value  { return &Value{Type: ValText, Text: v} }
func BlobValue(v []byte) *Value {
	b := make([]byte, len(v))
	cCopyBytes(b, v)
	return &Value{Type: ValBlob, Blob: b}
}

func (v *Value) String() string {
	switch v.Type {
	case ValNull:
		return "NULL"
	case ValInteger:
		return strconv.FormatInt(v.Integer, 10)
	case ValReal:
		return strconv.FormatFloat(v.Real, 'g', -1, 64)
	case ValText:
		return v.Text
	case ValBlob:
		return fmt.Sprintf("<blob:%d>", len(v.Blob))
	}
	return ""
}

func (v *Value) Compare(other *Value) int {
	if v.Type == ValNull && other.Type == ValNull {
		return 0
	}
	if v.Type == ValNull {
		return -1
	}
	if other.Type == ValNull {
		return 1
	}
	if v.Type == ValInteger && other.Type == ValInteger {
		if v.Integer < other.Integer {
			return -1
		} else if v.Integer > other.Integer {
			return 1
		}
		return 0
	}
	if (v.Type == ValInteger || v.Type == ValReal) && (other.Type == ValInteger || other.Type == ValReal) {
		lf := v.ToFloat()
		rf := other.ToFloat()
		if lf < rf {
			return -1
		} else if lf > rf {
			return 1
		}
		return 0
	}
	ls := v.String()
	rs := other.String()
	if ls < rs {
		return -1
	} else if ls > rs {
		return 1
	}
	return 0
}

func (v *Value) ToFloat() float64 {
	switch v.Type {
	case ValInteger:
		return float64(v.Integer)
	case ValReal:
		return v.Real
	}
	f, _ := strconv.ParseFloat(v.String(), 64)
	return f
}

func (v *Value) IsTrue() bool {
	switch v.Type {
	case ValNull:
		return false
	case ValInteger:
		return v.Integer != 0
	case ValReal:
		return v.Real != 0
	case ValText:
		return v.Text != ""
	case ValBlob:
		return len(v.Blob) > 0
	}
	return false
}

type Row struct {
	Values []*Value
}

func EncodeRow(r *Row) []byte {
	parts := make([][]byte, len(r.Values))
	total := 2
	for i, v := range r.Values {
		parts[i] = encodeValue(v)
		total += 1 + 4 + len(parts[i])
	}
	buf := make([]byte, 0, total)
	colCount := uint16(len(r.Values))
	header := make([]byte, 2)
	binary.BigEndian.PutUint16(header, colCount)
	buf = append(buf, header...)
	for _, p := range parts {
		typeAndLen := make([]byte, 5)
		typeAndLen[0] = p[0]
		binary.BigEndian.PutUint32(typeAndLen[1:5], uint32(len(p)-1))
		buf = append(buf, typeAndLen...)
		buf = append(buf, p[1:]...)
	}
	return buf
}

func DecodeRow(data []byte) (*Row, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("row data too short")
	}
	colCount := binary.BigEndian.Uint16(data[0:2])
	row := &Row{Values: make([]*Value, colCount)}
	offset := 2
	for i := uint16(0); i < colCount; i++ {
		if offset+5 > len(data) {
			return nil, fmt.Errorf("row data truncated at column %d", i)
		}
		vtype := ValueType(data[offset])
		vlen := int(binary.BigEndian.Uint32(data[offset+1 : offset+5]))
		offset += 5
		if offset+vlen > len(data) {
			return nil, fmt.Errorf("value data truncated")
		}
		v, err := decodeValue(vtype, data[offset:offset+vlen])
		if err != nil {
			return nil, err
		}
		row.Values[i] = v
		offset += vlen
	}
	return row, nil
}

func encodeValue(v *Value) []byte {
	switch v.Type {
	case ValNull:
		return []byte{byte(ValNull)}
	case ValInteger:
		buf := make([]byte, 9)
		buf[0] = byte(ValInteger)
		cEncodeUint64BE(buf[1:], uint64(v.Integer))
		return buf
	case ValReal:
		buf := make([]byte, 9)
		buf[0] = byte(ValReal)
		cEncodeUint64BE(buf[1:], math.Float64bits(v.Real))
		return buf
	case ValText:
		b := []byte(v.Text)
		buf := make([]byte, 1+len(b))
		buf[0] = byte(ValText)
		cCopyBytes(buf[1:], b)
		return buf
	case ValBlob:
		buf := make([]byte, 1+len(v.Blob))
		buf[0] = byte(ValBlob)
		cCopyBytes(buf[1:], v.Blob)
		return buf
	}
	return []byte{byte(ValNull)}
}

func decodeValue(vtype ValueType, data []byte) (*Value, error) {
	switch vtype {
	case ValNull:
		return NullValue, nil
	case ValInteger:
		if len(data) < 8 {
			return nil, fmt.Errorf("integer value too short")
		}
		return IntValue(int64(cDecodeUint64BE(data))), nil
	case ValReal:
		if len(data) < 8 {
			return nil, fmt.Errorf("real value too short")
		}
		return RealValue(math.Float64frombits(cDecodeUint64BE(data))), nil
	case ValText:
		return TextValue(string(data)), nil
	case ValBlob:
		return BlobValue(data), nil
	}
	return nil, fmt.Errorf("unknown value type: %d", vtype)
}

func CoerceValue(v *Value, colType ColumnType) (*Value, error) {
	switch colType {
	case TypeInteger:
		switch v.Type {
		case ValNull:
			return v, nil
		case ValInteger:
			return v, nil
		case ValReal:
			return IntValue(int64(v.Real)), nil
		case ValText:
			i, err := strconv.ParseInt(v.Text, 10, 64)
			if err != nil {
				f, err2 := strconv.ParseFloat(v.Text, 64)
				if err2 != nil {
					return nil, fmt.Errorf("cannot convert '%s' to INTEGER", v.Text)
				}
				return IntValue(int64(f)), nil
			}
			return IntValue(i), nil
		}
	case TypeReal:
		switch v.Type {
		case ValNull:
			return v, nil
		case ValReal:
			return v, nil
		case ValInteger:
			return RealValue(float64(v.Integer)), nil
		case ValText:
			f, err := strconv.ParseFloat(v.Text, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot convert '%s' to REAL", v.Text)
			}
			return RealValue(f), nil
		}
	case TypeText:
		if v.Type == ValNull {
			return v, nil
		}
		return TextValue(v.String()), nil
	}
	return v, nil
}

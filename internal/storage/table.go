package storage

import (
	"encoding/binary"
	"fmt"
	"strings"
)

type ConflictAction int

const (
	ConflictAbort    ConflictAction = 0
	ConflictReplace  ConflictAction = 1
	ConflictIgnore   ConflictAction = 2
	ConflictFail     ConflictAction = 3
	ConflictRollback ConflictAction = 4
)

type TableEngine struct {
	pager           *Pager
	schema          *SchemaManager
	lastInsertRowID int64
	changes         int64
}

func NewTableEngine(pager *Pager, schema *SchemaManager) *TableEngine {
	return &TableEngine{pager: pager, schema: schema}
}

func (te *TableEngine) LastInsertRowID() int64 { return te.lastInsertRowID }
func (te *TableEngine) Changes() int64         { return te.changes }

func (te *TableEngine) rowKey(id int64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, uint64(id))
	return key
}

func (te *TableEngine) Insert(tableName string, colNames []string, values []*Value) error {
	return te.InsertWithConflict(tableName, colNames, values, ConflictAbort)
}

func (te *TableEngine) InsertWithConflict(tableName string, colNames []string, values []*Value, onConflict ConflictAction) error {
	ts, ok := te.schema.GetTable(tableName)
	if !ok {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}
	row := &Row{Values: make([]*Value, len(ts.Columns))}
	for i := range row.Values {
		if ts.Columns[i].Default != "" {
			row.Values[i] = TextValue(ts.Columns[i].Default)
		} else {
			row.Values[i] = NullValue
		}
	}
	if len(colNames) == 0 {
		if len(values) != len(ts.Columns) {
			return fmt.Errorf("column count mismatch: expected %d, got %d", len(ts.Columns), len(values))
		}
		for i, v := range values {
			coerced, err := CoerceValue(v, ts.Columns[i].Type)
			if err != nil {
				return err
			}
			row.Values[i] = coerced
		}
	} else {
		if len(colNames) != len(values) {
			return fmt.Errorf("column count mismatch")
		}
		for i, cname := range colNames {
			idx := ts.ColumnIndex(cname)
			if idx < 0 {
				return fmt.Errorf("unknown column '%s'", cname)
			}
			coerced, err := CoerceValue(values[i], ts.Columns[idx].Type)
			if err != nil {
				return err
			}
			row.Values[idx] = coerced
		}
	}
	for i, col := range ts.Columns {
		if col.NotNull && row.Values[i].Type == ValNull {
			if !col.PrimaryKey {
				return fmt.Errorf("NOT NULL constraint failed for column '%s'", col.Name)
			}
		}
	}
	pkIdx, _ := ts.PrimaryKeyColumn()
	var rowID int64
	if pkIdx >= 0 && row.Values[pkIdx].Type != ValNull && row.Values[pkIdx].Integer != 0 {
		rowID = row.Values[pkIdx].Integer
		if rowID >= ts.NextAutoInc {
			ts.NextAutoInc = rowID + 1
		}
	} else {
		rowID = ts.NextAutoInc
		ts.NextAutoInc++
		if pkIdx >= 0 {
			row.Values[pkIdx] = IntValue(rowID)
		}
	}
	if err := te.schema.updateTableSchema(ts); err != nil {
		return err
	}
	bt := NewBTree(te.pager, ts.RootPage)
	key := te.rowKey(rowID)
	existing, found, err := bt.Get(key)
	if err != nil {
		return err
	}
	if found && existing != nil {
		switch onConflict {
		case ConflictIgnore:
			return nil
		case ConflictReplace:
			if _, err := bt.Delete(key); err != nil {
				return err
			}
		default:
			return fmt.Errorf("UNIQUE constraint failed: duplicate primary key %d", rowID)
		}
	}
	encoded := EncodeRow(row)
	if err := bt.Insert(key, encoded); err != nil {
		return err
	}
	if bt.Root() != ts.RootPage {
		ts.RootPage = bt.Root()
		te.schema.updateTableSchema(ts)
	}
	te.lastInsertRowID = rowID
	te.changes++
	for _, idxName := range ts.Indexes {
		is, ok := te.schema.GetIndex(idxName)
		if !ok {
			continue
		}
		colIdx := ts.ColumnIndex(is.Column)
		if colIdx < 0 {
			continue
		}
		idxBt := NewBTree(te.pager, is.RootPage)
		idxKey := te.indexKey(row.Values[colIdx], rowID)
		if err := idxBt.Insert(idxKey, key); err != nil {
			return err
		}
		if idxBt.Root() != is.RootPage {
			is.RootPage = idxBt.Root()
		}
	}
	return nil
}

func (te *TableEngine) indexKey(v *Value, rowID int64) []byte {
	valPart := []byte(v.String())
	idPart := make([]byte, 8)
	binary.BigEndian.PutUint64(idPart, uint64(rowID))
	return append(valPart, idPart...)
}

type ScanResult struct {
	RowID int64
	Row   *Row
}

func (te *TableEngine) Scan(tableName string) ([]*ScanResult, error) {
	ts, ok := te.schema.GetTable(tableName)
	if !ok {
		return nil, fmt.Errorf("table '%s' does not exist", tableName)
	}
	bt := NewBTree(te.pager, ts.RootPage)
	cursor, err := bt.NewCursor()
	if err != nil {
		return nil, err
	}
	var results []*ScanResult
	for !cursor.AtEnd() {
		cell, err := cursor.Current()
		if err != nil {
			return nil, err
		}
		if cell != nil {
			rowID := int64(binary.BigEndian.Uint64(cell.Key))
			row, err := DecodeRow(cell.Value)
			if err != nil {
				return nil, err
			}
			if len(row.Values) < len(ts.Columns) {
				extended := make([]*Value, len(ts.Columns))
				copy(extended, row.Values)
				for i := len(row.Values); i < len(ts.Columns); i++ {
					if ts.Columns[i].Default != "" {
						extended[i] = TextValue(ts.Columns[i].Default)
					} else {
						extended[i] = NullValue
					}
				}
				row.Values = extended
			}
			results = append(results, &ScanResult{RowID: rowID, Row: row})
		}
		if err := cursor.Advance(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (te *TableEngine) GetByID(tableName string, rowID int64) (*Row, bool, error) {
	ts, ok := te.schema.GetTable(tableName)
	if !ok {
		return nil, false, fmt.Errorf("table '%s' does not exist", tableName)
	}
	bt := NewBTree(te.pager, ts.RootPage)
	key := te.rowKey(rowID)
	data, found, err := bt.Get(key)
	if err != nil || !found {
		return nil, false, err
	}
	row, err := DecodeRow(data)
	if err != nil {
		return nil, false, err
	}
	return row, true, nil
}

func (te *TableEngine) Delete(tableName string, rowID int64) (bool, error) {
	ts, ok := te.schema.GetTable(tableName)
	if !ok {
		return false, fmt.Errorf("table '%s' does not exist", tableName)
	}
	bt := NewBTree(te.pager, ts.RootPage)
	key := te.rowKey(rowID)
	data, found, err := bt.Get(key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	row, err := DecodeRow(data)
	if err != nil {
		return false, err
	}
	for _, idxName := range ts.Indexes {
		is, ok := te.schema.GetIndex(idxName)
		if !ok {
			continue
		}
		colIdx := ts.ColumnIndex(is.Column)
		if colIdx < 0 || colIdx >= len(row.Values) {
			continue
		}
		idxBt := NewBTree(te.pager, is.RootPage)
		idxKey := te.indexKey(row.Values[colIdx], rowID)
		idxBt.Delete(idxKey)
	}
	deleted, err := bt.Delete(key)
	if err != nil {
		return false, err
	}
	if deleted {
		te.changes++
	}
	return deleted, nil
}

func (te *TableEngine) Update(tableName string, rowID int64, updates map[string]*Value) error {
	ts, ok := te.schema.GetTable(tableName)
	if !ok {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}
	bt := NewBTree(te.pager, ts.RootPage)
	key := te.rowKey(rowID)
	data, found, err := bt.Get(key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("row %d not found", rowID)
	}
	row, err := DecodeRow(data)
	if err != nil {
		return err
	}
	if len(row.Values) < len(ts.Columns) {
		extended := make([]*Value, len(ts.Columns))
		copy(extended, row.Values)
		for i := len(row.Values); i < len(ts.Columns); i++ {
			if ts.Columns[i].Default != "" {
				extended[i] = TextValue(ts.Columns[i].Default)
			} else {
				extended[i] = NullValue
			}
		}
		row.Values = extended
	}
	oldRow := &Row{Values: make([]*Value, len(row.Values))}
	copy(oldRow.Values, row.Values)
	for colName, newVal := range updates {
		idx := ts.ColumnIndex(colName)
		if idx < 0 {
			return fmt.Errorf("unknown column '%s'", colName)
		}
		coerced, err := CoerceValue(newVal, ts.Columns[idx].Type)
		if err != nil {
			return err
		}
		if ts.Columns[idx].NotNull && coerced.Type == ValNull {
			return fmt.Errorf("NOT NULL constraint failed for column '%s'", colName)
		}
		row.Values[idx] = coerced
	}
	for _, idxName := range ts.Indexes {
		is, ok := te.schema.GetIndex(idxName)
		if !ok {
			continue
		}
		colIdx := ts.ColumnIndex(is.Column)
		if colIdx < 0 {
			continue
		}
		if _, changed := updates[strings.ToLower(is.Column)]; changed {
			idxBt := NewBTree(te.pager, is.RootPage)
			oldIdxKey := te.indexKey(oldRow.Values[colIdx], rowID)
			idxBt.Delete(oldIdxKey)
			newIdxKey := te.indexKey(row.Values[colIdx], rowID)
			idxBt.Insert(newIdxKey, key)
		}
	}
	encoded := EncodeRow(row)
	te.changes++
	return bt.Insert(key, encoded)
}

func (te *TableEngine) Count(tableName string) (int64, error) {
	ts, ok := te.schema.GetTable(tableName)
	if !ok {
		return 0, fmt.Errorf("table '%s' does not exist", tableName)
	}
	bt := NewBTree(te.pager, ts.RootPage)
	cursor, err := bt.NewCursor()
	if err != nil {
		return 0, err
	}
	var count int64
	for !cursor.AtEnd() {
		count++
		if err := cursor.Advance(); err != nil {
			return count, err
		}
	}
	return count, nil
}

package storage

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ColumnType string

const (
	TypeInteger ColumnType = "INTEGER"
	TypeText    ColumnType = "TEXT"
	TypeReal    ColumnType = "REAL"
	TypeBlob    ColumnType = "BLOB"
	TypeNull    ColumnType = "NULL"
)

func NormalizeType(s string) ColumnType {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "INT", "INTEGER", "TINYINT", "SMALLINT", "MEDIUMINT", "BIGINT", "UNSIGNED BIG INT", "INT2", "INT8":
		return TypeInteger
	case "REAL", "DOUBLE", "DOUBLE PRECISION", "FLOAT", "NUMERIC", "DECIMAL":
		return TypeReal
	case "TEXT", "VARCHAR", "CHAR", "CHARACTER", "CLOB", "NCHAR", "NVARCHAR", "VARYING CHARACTER", "NATIVE CHARACTER":
		return TypeText
	case "BLOB":
		return TypeBlob
	case "BOOLEAN", "BOOL":
		return TypeInteger
	case "DATE", "DATETIME", "TIMESTAMP":
		return TypeText
	default:
		return TypeText
	}
}

type Column struct {
	Name       string     `json:"name"`
	Type       ColumnType `json:"type"`
	NotNull    bool       `json:"not_null"`
	PrimaryKey bool       `json:"primary_key"`
	Default    string     `json:"default,omitempty"`
	Unique     bool       `json:"unique"`
	AutoInc    bool       `json:"auto_inc,omitempty"`
}

type TableSchema struct {
	Name        string   `json:"name"`
	Columns     []Column `json:"columns"`
	RootPage    uint32   `json:"root_page"`
	Indexes     []string `json:"indexes"`
	NextAutoInc int64    `json:"next_auto_inc,omitempty"`
}

func (t *TableSchema) ColumnIndex(name string) int {
	for i, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

func (t *TableSchema) PrimaryKeyColumn() (int, *Column) {
	for i, c := range t.Columns {
		if c.PrimaryKey {
			return i, &t.Columns[i]
		}
	}
	return -1, nil
}

type IndexSchema struct {
	Name      string `json:"name"`
	TableName string `json:"table_name"`
	Column    string `json:"column"`
	Unique    bool   `json:"unique"`
	RootPage  uint32 `json:"root_page"`
}

type ViewSchema struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

type SchemaManager struct {
	pager   *Pager
	btree   *BTree
	tables  map[string]*TableSchema
	indexes map[string]*IndexSchema
	views   map[string]*ViewSchema
}

const (
	schemaKeyPrefix      = "t:"
	indexSchemaKeyPrefix = "i:"
	viewSchemaKeyPrefix  = "v:"
)

func NewSchemaManager(pager *Pager) (*SchemaManager, error) {
	sm := &SchemaManager{
		pager:   pager,
		tables:  make(map[string]*TableSchema),
		indexes: make(map[string]*IndexSchema),
		views:   make(map[string]*ViewSchema),
	}
	schemaRoot := pager.header.SchemaPage
	sm.btree = NewBTree(pager, schemaRoot)
	if err := sm.loadAll(); err != nil {
		return nil, err
	}
	return sm, nil
}

func (sm *SchemaManager) loadAll() error {
	cursor, err := sm.btree.NewCursor()
	if err != nil {
		return err
	}
	for !cursor.AtEnd() {
		cell, err := cursor.Current()
		if err != nil {
			return err
		}
		if cell != nil {
			k := string(cell.Key)
			switch {
			case strings.HasPrefix(k, schemaKeyPrefix):
				var ts TableSchema
				if err := json.Unmarshal(cell.Value, &ts); err != nil {
					return fmt.Errorf("corrupted schema for %s: %w", k, err)
				}
				sm.tables[strings.ToLower(ts.Name)] = &ts
			case strings.HasPrefix(k, indexSchemaKeyPrefix):
				var is IndexSchema
				if err := json.Unmarshal(cell.Value, &is); err != nil {
					return fmt.Errorf("corrupted index schema for %s: %w", k, err)
				}
				sm.indexes[strings.ToLower(is.Name)] = &is
			case strings.HasPrefix(k, viewSchemaKeyPrefix):
				var vs ViewSchema
				if err := json.Unmarshal(cell.Value, &vs); err != nil {
					return fmt.Errorf("corrupted view schema for %s: %w", k, err)
				}
				sm.views[strings.ToLower(vs.Name)] = &vs
			}
		}
		if err := cursor.Advance(); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SchemaManager) CreateTable(ts *TableSchema) error {
	lname := strings.ToLower(ts.Name)
	if _, exists := sm.tables[lname]; exists {
		return fmt.Errorf("table '%s' already exists", ts.Name)
	}
	rootPg, err := sm.pager.AllocatePage()
	if err != nil {
		return err
	}
	rootPg.SetType(PageTypeLeaf)
	rootPg.SetCellCount(0)
	rootPg.SetCellContentOffset(PageSize)
	rootPg.SetRightmostPointer(InvalidPage)
	rootPg.Dirty = true
	ts.RootPage = rootPg.ID
	if ts.NextAutoInc == 0 {
		ts.NextAutoInc = 1
	}
	data, err := json.Marshal(ts)
	if err != nil {
		return err
	}
	key := []byte(schemaKeyPrefix + lname)
	if err := sm.btree.Insert(key, data); err != nil {
		return err
	}
	sm.tables[lname] = ts
	return nil
}

func (sm *SchemaManager) DropTable(name string) error {
	lname := strings.ToLower(name)
	if _, exists := sm.tables[lname]; !exists {
		return fmt.Errorf("table '%s' does not exist", name)
	}
	key := []byte(schemaKeyPrefix + lname)
	if _, err := sm.btree.Delete(key); err != nil {
		return err
	}
	delete(sm.tables, lname)
	return nil
}

func (sm *SchemaManager) GetTable(name string) (*TableSchema, bool) {
	ts, ok := sm.tables[strings.ToLower(name)]
	return ts, ok
}

func (sm *SchemaManager) ListTables() []*TableSchema {
	result := make([]*TableSchema, 0, len(sm.tables))
	for _, ts := range sm.tables {
		result = append(result, ts)
	}
	return result
}

func (sm *SchemaManager) UpdateTableSchema(ts *TableSchema) error {
	return sm.updateTableSchema(ts)
}

func (sm *SchemaManager) CreateIndex(is *IndexSchema) error {
	lname := strings.ToLower(is.Name)
	if _, exists := sm.indexes[lname]; exists {
		return fmt.Errorf("index '%s' already exists", is.Name)
	}
	ts, ok := sm.tables[strings.ToLower(is.TableName)]
	if !ok {
		return fmt.Errorf("table '%s' does not exist", is.TableName)
	}
	if ts.ColumnIndex(is.Column) < 0 {
		return fmt.Errorf("column '%s' does not exist in table '%s'", is.Column, is.TableName)
	}
	rootPg, err := sm.pager.AllocatePage()
	if err != nil {
		return err
	}
	rootPg.SetType(PageTypeLeaf)
	rootPg.SetCellCount(0)
	rootPg.SetCellContentOffset(PageSize)
	rootPg.SetRightmostPointer(InvalidPage)
	rootPg.Dirty = true
	is.RootPage = rootPg.ID
	data, err := json.Marshal(is)
	if err != nil {
		return err
	}
	key := []byte(indexSchemaKeyPrefix + lname)
	if err := sm.btree.Insert(key, data); err != nil {
		return err
	}
	sm.indexes[lname] = is
	ts.Indexes = append(ts.Indexes, is.Name)
	return sm.updateTableSchema(ts)
}

func (sm *SchemaManager) GetIndex(name string) (*IndexSchema, bool) {
	is, ok := sm.indexes[strings.ToLower(name)]
	return is, ok
}

func (sm *SchemaManager) ListIndexes(tableName string) []*IndexSchema {
	result := []*IndexSchema{}
	for _, is := range sm.indexes {
		if strings.EqualFold(is.TableName, tableName) {
			result = append(result, is)
		}
	}
	return result
}

func (sm *SchemaManager) updateTableSchema(ts *TableSchema) error {
	lname := strings.ToLower(ts.Name)
	sm.tables[lname] = ts
	data, err := json.Marshal(ts)
	if err != nil {
		return err
	}
	key := []byte(schemaKeyPrefix + lname)
	return sm.btree.Insert(key, data)
}

func (sm *SchemaManager) DropIndex(name string) error {
	lname := strings.ToLower(name)
	is, exists := sm.indexes[lname]
	if !exists {
		return fmt.Errorf("index '%s' does not exist", name)
	}
	key := []byte(indexSchemaKeyPrefix + lname)
	if _, err := sm.btree.Delete(key); err != nil {
		return err
	}
	if ts, ok := sm.tables[strings.ToLower(is.TableName)]; ok {
		newIdxs := []string{}
		for _, idxName := range ts.Indexes {
			if !strings.EqualFold(idxName, name) {
				newIdxs = append(newIdxs, idxName)
			}
		}
		ts.Indexes = newIdxs
		if err := sm.updateTableSchema(ts); err != nil {
			return err
		}
	}
	delete(sm.indexes, lname)
	return nil
}

func (sm *SchemaManager) CreateView(vs *ViewSchema) error {
	lname := strings.ToLower(vs.Name)
	if _, exists := sm.views[lname]; exists {
		return fmt.Errorf("view '%s' already exists", vs.Name)
	}
	data, err := json.Marshal(vs)
	if err != nil {
		return err
	}
	key := []byte(viewSchemaKeyPrefix + lname)
	if err := sm.btree.Insert(key, data); err != nil {
		return err
	}
	sm.views[lname] = vs
	return nil
}

func (sm *SchemaManager) DropView(name string) error {
	lname := strings.ToLower(name)
	if _, exists := sm.views[lname]; !exists {
		return fmt.Errorf("view '%s' does not exist", name)
	}
	key := []byte(viewSchemaKeyPrefix + lname)
	if _, err := sm.btree.Delete(key); err != nil {
		return err
	}
	delete(sm.views, lname)
	return nil
}

func (sm *SchemaManager) GetView(name string) (*ViewSchema, bool) {
	vs, ok := sm.views[strings.ToLower(name)]
	return vs, ok
}

func (sm *SchemaManager) ListViews() []*ViewSchema {
	result := make([]*ViewSchema, 0, len(sm.views))
	for _, vs := range sm.views {
		result = append(result, vs)
	}
	return result
}

func (sm *SchemaManager) RenameTable(oldName, newName string) error {
	lold := strings.ToLower(oldName)
	lnew := strings.ToLower(newName)
	ts, ok := sm.tables[lold]
	if !ok {
		return fmt.Errorf("table '%s' does not exist", oldName)
	}
	if _, exists := sm.tables[lnew]; exists {
		return fmt.Errorf("table '%s' already exists", newName)
	}
	oldKey := []byte(schemaKeyPrefix + lold)
	if _, err := sm.btree.Delete(oldKey); err != nil {
		return err
	}
	delete(sm.tables, lold)
	ts.Name = newName
	data, err := json.Marshal(ts)
	if err != nil {
		return err
	}
	newKey := []byte(schemaKeyPrefix + lnew)
	if err := sm.btree.Insert(newKey, data); err != nil {
		return err
	}
	sm.tables[lnew] = ts
	return nil
}

func (sm *SchemaManager) AddColumn(tableName string, col Column) error {
	lname := strings.ToLower(tableName)
	ts, ok := sm.tables[lname]
	if !ok {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}
	if ts.ColumnIndex(col.Name) >= 0 {
		return fmt.Errorf("column '%s' already exists", col.Name)
	}
	if col.NotNull && col.Default == "" {
		return fmt.Errorf("cannot add NOT NULL column without DEFAULT value")
	}
	ts.Columns = append(ts.Columns, col)
	return sm.updateTableSchema(ts)
}

func (sm *SchemaManager) RenameColumn(tableName, oldColName, newColName string) error {
	lname := strings.ToLower(tableName)
	ts, ok := sm.tables[lname]
	if !ok {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}
	idx := ts.ColumnIndex(oldColName)
	if idx < 0 {
		return fmt.Errorf("column '%s' does not exist", oldColName)
	}
	if ts.ColumnIndex(newColName) >= 0 {
		return fmt.Errorf("column '%s' already exists", newColName)
	}
	ts.Columns[idx].Name = newColName
	return sm.updateTableSchema(ts)
}

func (sm *SchemaManager) DropColumn(tableName, colName string) error {
	lname := strings.ToLower(tableName)
	ts, ok := sm.tables[lname]
	if !ok {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}
	idx := ts.ColumnIndex(colName)
	if idx < 0 {
		return fmt.Errorf("column '%s' does not exist", colName)
	}
	col := ts.Columns[idx]
	if col.PrimaryKey {
		return fmt.Errorf("cannot drop primary key column '%s'", colName)
	}
	ts.Columns = append(ts.Columns[:idx], ts.Columns[idx+1:]...)
	return sm.updateTableSchema(ts)
}

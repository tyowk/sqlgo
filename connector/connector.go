package connector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/tyowk/sqlgo/auth"
	"github.com/tyowk/sqlgo/executor"
	"github.com/tyowk/sqlgo/storage"
)

const (
	defaultDialTimeout  = 10 * time.Second
	defaultReadTimeout  = 30 * time.Second
	defaultWriteTimeout = 10 * time.Second
	defaultMaxRetries   = 3
	scanBufSize         = 4 * 1024 * 1024
)

type Result struct {
	Columns      []string   `json:"columns"`
	Rows         [][]string `json:"rows"`
	Error        string     `json:"error"`
	RowsAffected int64      `json:"rows_affected"`
	LastInsertID int64      `json:"last_insert_id"`
	Elapsed      string     `json:"elapsed,omitempty"`
}

type Options struct {
	DialTimeout time.Duration
	ReadTimeout time.Duration
	Password    string
	Token       string
}

func DefaultOptions() Options {
	return Options{
		DialTimeout: defaultDialTimeout,
		ReadTimeout: defaultReadTimeout,
	}
}

type Connector struct {
	addr    string
	opts    Options
	conn    net.Conn
	scanner *bufio.Scanner
	encoder *json.Encoder
	mu      sync.Mutex
}

func Open(addr string) (*Connector, error) {
	return OpenWithOptions(addr, DefaultOptions())
}

func OpenWithPassword(addr, password string) (*Connector, error) {
	opts := DefaultOptions()
	opts.Password = password
	return OpenWithOptions(addr, opts)
}

func OpenWithToken(addr, token string) (*Connector, error) {
	opts := DefaultOptions()
	opts.Token = token
	return OpenWithOptions(addr, opts)
}

func OpenWithOptions(addr string, opts Options) (*Connector, error) {
	if opts.DialTimeout == 0 {
		opts.DialTimeout = defaultDialTimeout
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = defaultReadTimeout
	}
	c := &Connector{addr: addr, opts: opts}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Connector) dial() error {
	conn, err := net.DialTimeout("tcp", c.addr, c.opts.DialTimeout)
	if err != nil {
		return fmt.Errorf("failed to connect to sqlgo server at %s: %w", c.addr, err)
	}
	if err := auth.ClientHandshake(conn, c.opts.Password, c.opts.Token); err != nil {
		conn.Close()
		return err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, scanBufSize), scanBufSize)
	c.conn = conn
	c.scanner = scanner
	c.encoder = json.NewEncoder(conn)
	return nil
}

func (c *Connector) reconnect() error {
	if c.conn != nil {
		c.conn.Close()
	}
	return c.dial()
}

func (c *Connector) Exec(sql string) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.execWithRetry(sql, defaultMaxRetries)
}

func (c *Connector) execWithRetry(sql string, retries int) (*Result, error) {
	for i := 0; i <= retries; i++ {
		result, err := c.execOnce(sql)
		if err == nil {
			return result, nil
		}
		if i < retries {
			if rerr := c.reconnect(); rerr != nil {
				return nil, rerr
			}
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("max retries exceeded")
}

func (c *Connector) execOnce(sql string) (*Result, error) {
	req := map[string]string{"sql": sql}
	c.conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout))
	if err := c.encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("send error: %w", err)
	}
	c.conn.SetReadDeadline(time.Now().Add(c.opts.ReadTimeout))
	if !c.scanner.Scan() {
		err := c.scanner.Err()
		if err == nil {
			return nil, fmt.Errorf("connection closed")
		}
		return nil, err
	}
	var result Result
	if err := json.Unmarshal(c.scanner.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}
	if result.Error != "" {
		return &result, fmt.Errorf("%s", result.Error)
	}
	return &result, nil
}

func (c *Connector) Query(sql string) (*Result, error) {
	return c.Exec(sql)
}

func (c *Connector) ExecBatch(sqls []string) ([]*Result, error) {
	results := make([]*Result, 0, len(sqls))
	for _, sql := range sqls {
		r, err := c.Exec(sql)
		if err != nil {
			return results, fmt.Errorf("batch error at %q: %w", sql, err)
		}
		results = append(results, r)
	}
	return results, nil
}

func (c *Connector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Connector) Ping() error {
	result, err := c.Exec("SELECT 1")
	if err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("%s", result.Error)
	}
	return nil
}

func (c *Connector) CreateTable(name string, cols map[string]string) error {
	parts := make([]string, 0, len(cols))
	for col, typ := range cols {
		parts = append(parts, col+" "+typ)
	}
	sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", name, strings.Join(parts, ", "))
	_, err := c.Exec(sql)
	return err
}

func (c *Connector) Insert(table string, data map[string]interface{}) (int64, error) {
	cols := make([]string, 0, len(data))
	vals := make([]string, 0, len(data))
	for col, val := range data {
		cols = append(cols, col)
		switch v := val.(type) {
		case string:
			escaped := strings.ReplaceAll(v, "'", "''")
			vals = append(vals, "'"+escaped+"'")
		case nil:
			vals = append(vals, "NULL")
		default:
			vals = append(vals, fmt.Sprintf("%v", v))
		}
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(vals, ", "))
	result, err := c.Exec(sql)
	if err != nil {
		return 0, err
	}
	return result.LastInsertID, nil
}

func (c *Connector) Select(table string, where string) (*Result, error) {
	sql := fmt.Sprintf("SELECT * FROM %s", table)
	if where != "" {
		sql += " WHERE " + where
	}
	return c.Query(sql)
}

type LocalDB struct {
	pager  *storage.Pager
	schema *storage.SchemaManager
	exec   *executor.Executor
	mu     sync.Mutex
}

func OpenLocal(path string) (*LocalDB, error) {
	pager, err := storage.NewPager(path)
	if err != nil {
		return nil, fmt.Errorf("open local db %s: %w", path, err)
	}
	schema, err := storage.NewSchemaManager(pager)
	if err != nil {
		pager.Close()
		return nil, fmt.Errorf("load schema: %w", err)
	}
	return &LocalDB{
		pager:  pager,
		schema: schema,
		exec:   executor.NewExecutor(pager, schema),
	}, nil
}

func (db *LocalDB) Exec(sql string) (*Result, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rs, err := db.exec.Execute(sql)
	if err != nil {
		return &Result{Error: err.Error()}, err
	}
	r := &Result{}
	if rs != nil {
		r.Columns = rs.Columns
		r.Rows = rs.Rows
		r.LastInsertID = db.exec.LastInsertRowID()
		r.RowsAffected = db.exec.Changes()
	}
	return r, nil
}

func (db *LocalDB) Query(sql string) (*Result, error) {
	return db.Exec(sql)
}

func (db *LocalDB) ExecBatch(sqls []string) ([]*Result, error) {
	results := make([]*Result, 0, len(sqls))
	for _, sql := range sqls {
		r, err := db.Exec(sql)
		if err != nil {
			return results, fmt.Errorf("batch error at %q: %w", sql, err)
		}
		results = append(results, r)
	}
	return results, nil
}

func (db *LocalDB) CreateTable(name string, cols map[string]string) error {
	parts := make([]string, 0, len(cols))
	for col, typ := range cols {
		parts = append(parts, col+" "+typ)
	}
	sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", name, strings.Join(parts, ", "))
	_, err := db.Exec(sql)
	return err
}

func (db *LocalDB) Insert(table string, data map[string]interface{}) (int64, error) {
	cols := make([]string, 0, len(data))
	vals := make([]string, 0, len(data))
	for col, val := range data {
		cols = append(cols, col)
		switch v := val.(type) {
		case string:
			escaped := strings.ReplaceAll(v, "'", "''")
			vals = append(vals, "'"+escaped+"'")
		case nil:
			vals = append(vals, "NULL")
		default:
			vals = append(vals, fmt.Sprintf("%v", v))
		}
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table, strings.Join(cols, ", "), strings.Join(vals, ", "))
	result, err := db.Exec(sql)
	if err != nil {
		return 0, err
	}
	return result.LastInsertID, nil
}

func (db *LocalDB) Select(table, where string) (*Result, error) {
	sql := fmt.Sprintf("SELECT * FROM %s", table)
	if where != "" {
		sql += " WHERE " + where
	}
	return db.Query(sql)
}

func (db *LocalDB) Flush() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.pager.FlushAll()
}

func (db *LocalDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.pager.Close()
}

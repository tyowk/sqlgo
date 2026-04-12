package connector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tyowk/sqlgo/internal/engine/executor"
	"github.com/tyowk/sqlgo/internal/storage"
)

const (
	defaultDialTimeout  = 10 * time.Second
	defaultReadTimeout  = 30 * time.Second
	defaultWriteTimeout = 10 * time.Second
	defaultMaxRetries   = 3
	scanBufSize         = 4 * 1024 * 1024
)

type DB interface {
	Exec(sql string) (*Result, error)
	Query(sql string) (*Result, error)
	ExecBatch(sqls []string) ([]*Result, error)
	Ping() error
	Close() error
}

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

type Option func(*Options)

func WithPassword(p string) Option       { return func(o *Options) { o.Password = p } }
func WithToken(t string) Option          { return func(o *Options) { o.Token = t } }
func WithTimeout(d time.Duration) Option { return func(o *Options) { o.DialTimeout = d } }

func DefaultOptions() Options {
	return Options{
		DialTimeout: defaultDialTimeout,
		ReadTimeout: defaultReadTimeout,
	}
}

func Open(target string, opts ...Option) (DB, error) {
	o := DefaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	if isLocal(target) {
		return openLocal(ensureCurrentDir(target))
	}

	return openRemote(target, o)
}

func isLocal(target string) bool {
	if strings.HasPrefix(target, "file://") {
		return true
	}
	if strings.HasSuffix(target, ".db") {
		return true
	}
	if strings.Contains(target, "/") || strings.Contains(target, "\\") {
		return true
	}
	if !strings.Contains(target, ":") {
		return true
	}
	return false
}

func ensureCurrentDir(path string) string {
	path = strings.TrimPrefix(path, "file://")

	if filepath.IsAbs(path) {
		path = filepath.Base(path)
	}

	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "\\")

	pwd, err := os.Getwd()
	if err != nil {
		return path
	}
	return filepath.Join(pwd, path)
}

type Connector struct {
	addr    string
	opts    Options
	conn    net.Conn
	scanner *bufio.Scanner
	encoder *json.Encoder
	mu      sync.Mutex
}

func openRemote(addr string, opts Options) (*Connector, error) {
	c := &Connector{addr: addr, opts: opts}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Connector) dial() error {
	conn, err := net.DialTimeout("tcp", c.addr, c.opts.DialTimeout)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	if err := ClientHandshake(conn, c.opts.Password, c.opts.Token); err != nil {
		conn.Close()
		return err
	}

	c.conn = conn
	c.scanner = bufio.NewScanner(conn)
	c.scanner.Buffer(make([]byte, scanBufSize), scanBufSize)
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

	var lastErr error
	for i := 0; i <= defaultMaxRetries; i++ {
		res, err := c.execOnce(sql)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if i < defaultMaxRetries {
			if err := c.reconnect(); err != nil {
				lastErr = err
				continue
			}
		}
	}
	return nil, lastErr
}

func (c *Connector) execOnce(sql string) (*Result, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}
	c.conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout))
	if err := c.encoder.Encode(map[string]string{"sql": sql}); err != nil {
		return nil, err
	}

	c.conn.SetReadDeadline(time.Now().Add(c.opts.ReadTimeout))
	if !c.scanner.Scan() {
		return nil, fmt.Errorf("server closed connection")
	}

	var res Result
	if err := json.Unmarshal(c.scanner.Bytes(), &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return &res, fmt.Errorf("%s", res.Error)
	}
	return &res, nil
}

func (c *Connector) Query(sql string) (*Result, error) { return c.Exec(sql) }

func (c *Connector) ExecBatch(sqls []string) ([]*Result, error) {
	results := make([]*Result, 0, len(sqls))
	for _, sql := range sqls {
		res, err := c.Exec(sql)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (c *Connector) Ping() error {
	_, err := c.Exec("SELECT 1")
	return err
}

func (c *Connector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type LocalDB struct {
	pager  *storage.Pager
	schema *storage.SchemaManager
	exec   *executor.Executor
	mu     sync.Mutex
}

func openLocal(path string) (*LocalDB, error) {
	pager, err := storage.NewPager(path)
	if err != nil {
		return nil, err
	}
	schema, err := storage.NewSchemaManager(pager)
	if err != nil {
		pager.Close()
		return nil, err
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
		return nil, err
	}
	res := &Result{}
	if rs != nil {
		res.Columns = rs.Columns
		res.Rows = rs.Rows
		res.LastInsertID = db.exec.LastInsertRowID()
		res.RowsAffected = db.exec.Changes()
	}
	return res, nil
}

func (db *LocalDB) Query(sql string) (*Result, error) { return db.Exec(sql) }

func (db *LocalDB) ExecBatch(sqls []string) ([]*Result, error) {
	results := make([]*Result, 0, len(sqls))
	for _, sql := range sqls {
		res, err := db.Exec(sql)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (db *LocalDB) Ping() error { return nil }

func (db *LocalDB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.pager.Close()
}

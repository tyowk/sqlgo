package connector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tyowk/sqlgo/internal/engine/executor"
	"github.com/tyowk/sqlgo/internal/storage"
)

const (
	readTimeout   = 60 * time.Second
	writeTimeout  = 15 * time.Second
	maxConnBuf    = 8 * 1024 * 1024
	maxIdleConns  = 512
	keepAlivePing = 30 * time.Second
)

type Server struct {
	addr       string
	pager      *storage.Pager
	schema     *storage.SchemaManager
	cred       *Credential
	writeMu    sync.Mutex
	listener   net.Listener
	conns      sync.Map
	connCount  int64
	queryCount int64
	authFails  int64
	errorCount int64
	bytesSent  int64
	bytesRecv  int64
	startTime  time.Time
	ctx        context.Context
	cancel     context.CancelFunc
	workerPool chan struct{}
}

type Request struct {
	SQL      string `json:"sql"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type Response struct {
	Columns      []string   `json:"columns,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	Error        string     `json:"error,omitempty"`
	RowsAffected int64      `json:"rows_affected,omitempty"`
	LastInsertID int64      `json:"last_insert_id,omitempty"`
	Elapsed      string     `json:"elapsed,omitempty"`
	QueryID      int64      `json:"query_id,omitempty"`
}

func NewServer(addr string, pager *storage.Pager, schema *storage.SchemaManager, cred *Credential) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	if cred == nil {
		cred = &Credential{Mode: ModeNone}
	}
	workers := runtime.NumCPU() * 2
	if workers < 4 {
		workers = 4
	}
	return &Server{
		addr:       addr,
		pager:      pager,
		schema:     schema,
		cred:       cred,
		ctx:        ctx,
		cancel:     cancel,
		startTime:  time.Now(),
		workerPool: make(chan struct{}, workers),
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		_ = tcpLn
	}
	s.listener = ln
	fmt.Printf("sqlgo server listening on %s (auth: %s, workers: %d)\n",
		s.addr, string(s.cred.Mode), cap(s.workerPool))

	for {
		select {
		case <-s.ctx.Done():
			return nil
		default:
		}

		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return nil
			default:
				atomic.AddInt64(&s.errorCount, 1)
				continue
			}
		}

		count := atomic.AddInt64(&s.connCount, 1)
		if count > maxIdleConns {
			conn.Close()
			atomic.AddInt64(&s.connCount, -1)
			continue
		}

		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(keepAlivePing)
		}

		go s.handleConn(conn)
	}
}

func isReadOnlySQL(sql string) bool {
	trimmed := strings.TrimSpace(strings.ToUpper(sql))
	return strings.HasPrefix(trimmed, "SELECT") ||
		strings.HasPrefix(trimmed, "EXPLAIN") ||
		strings.HasPrefix(trimmed, "SHOW") ||
		strings.HasPrefix(trimmed, "PRAGMA")
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		atomic.AddInt64(&s.connCount, -1)
		s.conns.Delete(conn.RemoteAddr().String())
	}()

	s.conns.Store(conn.RemoteAddr().String(), conn)

	if err := ServerHandshake(conn, s.cred); err != nil {
		atomic.AddInt64(&s.authFails, 1)
		fmt.Printf("[auth] %s: %v\n", conn.RemoteAddr(), err)
		return
	}

	exec := executor.NewExecutor(s.pager, s.schema)
	reader := bufio.NewReaderSize(conn, maxConnBuf)
	encoder := json.NewEncoder(conn)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		atomic.AddInt64(&s.bytesRecv, int64(len(line)))
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			req.SQL = line
		}
		if req.SQL == "" {
			continue
		}

		queryID := atomic.AddInt64(&s.queryCount, 1)
		start := time.Now()

		var rs *executor.ResultSet
		var execErr error

		readOnly := req.ReadOnly || isReadOnlySQL(req.SQL)
		if readOnly {
			rs, execErr = exec.Execute(req.SQL)
		} else {
			s.writeMu.Lock()
			rs, execErr = exec.Execute(req.SQL)
			s.writeMu.Unlock()
		}

		elapsed := time.Since(start)

		var resp Response
		resp.QueryID = queryID
		resp.Elapsed = elapsed.String()
		if execErr != nil {
			resp.Error = execErr.Error()
			atomic.AddInt64(&s.errorCount, 1)
		} else if rs != nil {
			resp.Columns = rs.Columns
			resp.Rows = rs.Rows
			resp.LastInsertID = exec.LastInsertRowID()
			resp.RowsAffected = exec.Changes()
		}

		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if encErr := encoder.Encode(resp); encErr != nil {
			return
		}

		if elapsed > 5*time.Second {
			fmt.Printf("[slow query %dms] %s\n", elapsed.Milliseconds(), req.SQL[:min(len(req.SQL), 100)])
		}
	}
}

func (s *Server) Stop() {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
	s.conns.Range(func(k, v interface{}) bool {
		if c, ok := v.(net.Conn); ok {
			c.Close()
		}
		return true
	})
	s.pager.FlushAll()
}

func (s *Server) Stats() map[string]interface{} {
	return map[string]interface{}{
		"connections": atomic.LoadInt64(&s.connCount),
		"queries":     atomic.LoadInt64(&s.queryCount),
		"auth_fails":  atomic.LoadInt64(&s.authFails),
		"errors":      atomic.LoadInt64(&s.errorCount),
		"bytes_sent":  atomic.LoadInt64(&s.bytesSent),
		"bytes_recv":  atomic.LoadInt64(&s.bytesRecv),
		"uptime_secs": int64(time.Since(s.startTime).Seconds()),
		"worker_cap":  cap(s.workerPool),
		"max_conns":   maxIdleConns,
	}
}

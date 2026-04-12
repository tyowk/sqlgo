package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tyowk/sqlgo/auth"
	"github.com/tyowk/sqlgo/executor"
	"github.com/tyowk/sqlgo/storage"
)

const (
	readTimeout  = 30 * time.Second
	writeTimeout = 10 * time.Second
	maxConnBuf   = 4 * 1024 * 1024
	maxIdleConns = 100
)

type Server struct {
	addr       string
	pager      *storage.Pager
	schema     *storage.SchemaManager
	cred       *auth.Credential
	mu         sync.RWMutex
	listener   net.Listener
	conns      sync.Map
	connCount  int64
	queryCount int64
	authFails  int64
	ctx        context.Context
	cancel     context.CancelFunc
}

type Request struct {
	SQL string `json:"sql"`
}

type Response struct {
	Columns      []string   `json:"columns,omitempty"`
	Rows         [][]string `json:"rows,omitempty"`
	Error        string     `json:"error,omitempty"`
	RowsAffected int64      `json:"rows_affected,omitempty"`
	LastInsertID int64      `json:"last_insert_id,omitempty"`
	Elapsed      string     `json:"elapsed,omitempty"`
}

func New(addr string, pager *storage.Pager, schema *storage.SchemaManager, cred *auth.Credential) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	if cred == nil {
		cred = &auth.Credential{Mode: auth.ModeNone}
	}
	return &Server{
		addr:   addr,
		pager:  pager,
		schema: schema,
		cred:   cred,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}
	s.listener = ln
	fmt.Printf("sqlgo server listening on %s (auth: %s)\n", s.addr, string(s.cred.Mode))

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
				return err
			}
		}

		count := atomic.AddInt64(&s.connCount, 1)
		if count > maxIdleConns {
			conn.Close()
			atomic.AddInt64(&s.connCount, -1)
			continue
		}

		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		atomic.AddInt64(&s.connCount, -1)
		s.conns.Delete(conn.RemoteAddr().String())
	}()

	s.conns.Store(conn.RemoteAddr().String(), conn)

	if err := auth.ServerHandshake(conn, s.cred); err != nil {
		atomic.AddInt64(&s.authFails, 1)
		fmt.Printf("[auth] %v\n", err)
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

		start := time.Now()
		s.mu.Lock()
		rs, execErr := exec.Execute(req.SQL)
		s.mu.Unlock()
		elapsed := time.Since(start)
		atomic.AddInt64(&s.queryCount, 1)

		var resp Response
		resp.Elapsed = elapsed.String()
		if execErr != nil {
			resp.Error = execErr.Error()
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

func (s *Server) Stats() map[string]int64 {
	return map[string]int64{
		"connections": atomic.LoadInt64(&s.connCount),
		"queries":     atomic.LoadInt64(&s.queryCount),
		"auth_fails":  atomic.LoadInt64(&s.authFails),
	}
}

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tyowk/sqlgo/internal/connector"
	"github.com/tyowk/sqlgo/internal/engine/executor"
	"github.com/tyowk/sqlgo/internal/storage"
)

func main() {
	var (
		addr      = flag.String("addr", ":5433", "TCP address to listen on")
		cacheSize = flag.Int("cache", 1024, "Number of pages to cache in memory")
		srvMode   = flag.Bool("server", false, "Run as TCP server")
		authFile  = flag.String("auth", connector.AuthFile, "Path to auth credential file")
		initAuth  = flag.Bool("init-auth", false, "Interactively set up auth credentials and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <database_file> [sql]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -init-auth                            # set up auth\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -server -addr :5433 mydb.db           # TCP server (no auth)\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -server -auth sqlgo.auth mydb.db      # TCP server with auth\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s mydb.db \"SELECT * FROM users\"         # local one-shot query\n", os.Args[0])
	}
	flag.Parse()

	if *initAuth {
		if err := connector.InitAuthInteractive(*authFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(1)
	}
	dbFile := args[0]

	pager, err := storage.NewPagerWithCacheSize(dbFile, *cacheSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}

	schema, err := storage.NewSchemaManager(pager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading schema: %v\n", err)
		pager.Close()
		os.Exit(1)
	}

	if *srvMode {
		cred, err := connector.LoadCredential(*authFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading auth: %v\n", err)
			pager.Close()
			os.Exit(1)
		}

		srv := connector.NewServer(*addr, pager, schema, cred)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			fmt.Println("\nShutting down...")
			srv.Stop()
			os.Exit(0)
		}()
		if err := srv.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Non-server mode requires SQL: %s <db> <sql>\n", os.Args[0])
		pager.Close()
		os.Exit(1)
	}

	exec := executor.NewExecutor(pager, schema)
	defer pager.Close()

	rs, err := exec.Execute(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if rs == nil {
		return
	}
	if len(rs.Columns) > 0 {
		for i, col := range rs.Columns {
			if i > 0 {
				fmt.Print("|")
			}
			fmt.Print(col)
		}
		fmt.Println()
	}
	for _, row := range rs.Rows {
		for i, val := range row {
			if i > 0 {
				fmt.Print("|")
			}
			fmt.Print(val)
		}
		fmt.Println()
	}
}

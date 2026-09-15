# SQLGo

A database server engine written in Go.

SQLGo is a lightweight SQL database project built from scratch, with its own storage layer, SQL parser, executor, and TCP server.

## Features

* SQL parser and query executor
* Local database files
* TCP database server
* Client connector API
* B-tree based storage
* Page-based storage
* Transactions
* Password and token authentication
* Batch SQL execution
* Query statistics
* Connection retry and timeout handling
* Optional read-only query handling

## Requirements

* Go 1.25+
* CGO-enabled toolchain

## Build

Clone the repository:

```bash
git clone https://github.com/tyowk/sqlgo.git
cd sqlgo
```

Build the SQLGo server/CLI:

```bash
go build -o sqlgo ./cmd/server
```

## CLI Usage

### Local Query

Execute a SQL statement directly against a database file:

```bash
./sqlgo mydb.db "SELECT * FROM users"
```

### Start a TCP Server

```bash
./sqlgo -server -addr :5433 mydb.db
```

The default server address is:

```text
:5433
```

### Authentication

Initialize authentication credentials:

```bash
./sqlgo -init-auth
```

Then start the server with the authentication file:

```bash
./sqlgo -server -auth sqlgo.auth mydb.db
```

## Go API

SQLGo can also be used as a Go package.

```go
package main

import (
	"log"

	"github.com/tyowk/sqlgo"
)

func main() {
	db, err := sqlgo.Open("mydb.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	result, err := db.Query("SELECT * FROM users")
	if err != nil {
		log.Fatal(err)
	}

	for _, row := range result.Rows {
		log.Println(row)
	}
}
```

### Remote Connection

`sqlgo.Open` can connect to a remote SQLGo server as well:

```go
db, err := sqlgo.Open(
	"localhost:5433",
	sqlgo.WithPassword("password"),
)
```

Token authentication is also supported:

```go
db, err := sqlgo.Open(
	"localhost:5433",
	sqlgo.WithToken("token"),
)
```

## Database API

The client interface provides:

```go
Exec(sql string)
Query(sql string)
ExecBatch(sqls []string)
Ping()
Close()
```

Results contain information such as:

* Columns
* Rows
* Rows affected
* Last inserted ID
* Errors
* Query execution time

## Architecture

```text
SQLGo
├── Parser
│   ├── Lexer
│   ├── Parser
│   └── AST
│
├── Executor
│   ├── SQL execution
│   └── Expression evaluation
│
├── Storage
│   ├── Pager
│   ├── B-Tree
│   ├── Tables
│   ├── Rows
│   ├── Schema
│   └── Transactions
│
└── Connector
    ├── Local database
    ├── TCP client
    ├── TCP server
    └── Authentication
```

## Project Structure

```text
sqlgo/
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── connector/
│   ├── engine/
│   │   ├── executor/
│   │   └── parser/
│   └── storage/
│
├── main.go
└── go.mod
```

## Status

SQLGo is currently a **work in progress** and is primarily an experimental database engine project.

The SQL implementation, storage format, and APIs may change as development continues.

## License

No license has been specified yet.

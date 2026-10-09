package types

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
)

// PostgresSQLDBHandlerInterface contains the implementable methods for the MySQL DB handler
type PostgresSQLDBHandlerInterface interface {
	// Begin starts a new transaction
	Begin() (*sqlx.Tx, error)
	// BeginTx starts a transaction bounded by the caller's context.
	BeginTx(context.Context) (*sqlx.Tx, error)
	// Connect opens a new connection to the mysql interface
	Connect(params ConnectionParams) error
	// Execute executes the mysql statement following NamedExec
	Execute(stmt string, model interface{}) (sql.Result, error)
	// Query selects rows given by the sql statement
	Query(qstmt string, model interface{}, bindModel interface{}) error
	// QueryRow selects a row given by the sql statement
	QueryRow(qstmt string, model interface{}, bindModel interface{}) error
	// QueryRowContext selects a row and allows the caller to cancel the query.
	QueryRowContext(context.Context, string, interface{}, interface{}) error
}

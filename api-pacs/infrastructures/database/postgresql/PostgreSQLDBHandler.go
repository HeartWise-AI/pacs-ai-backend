package postgresql

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"api-pacs/infrastructures/database/postgresql/types"
)

// PostgreSQLDBHandler handles postgresql operations
type PostgreSQLDBHandler struct {
	Conn *sqlx.DB
}

// Begin starts a new transaction
func (h *PostgreSQLDBHandler) Begin() (*sqlx.Tx, error) {
	tx, err := h.Conn.Beginx()
	if err != nil {
		return nil, err
	}

	return tx, nil
}

// Connect opens a new connection to the postgresql interface
func (h *PostgreSQLDBHandler) Connect(params types.ConnectionParams) error {
	config, err := pgx.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		params.DBUsername, params.DBPassword, params.DBHost, params.DBPort, params.DBDatabase))
	if err != nil {
		return fmt.Errorf("[SERVER] Failed to parse database config: %w", err)
	}

	db := sqlx.NewDb(stdlib.OpenDB(*config), "pgx")

	db.SetConnMaxLifetime(time.Minute * 4)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	err = db.Ping()
	if err != nil {
		return fmt.Errorf("[SERVER] Error connecting to the database! %w", err)
	}

	h.Conn = db

	fmt.Println("[SERVER] Database connected successfully")

	return nil
}

// Execute executes the postgresql statement following NamedExec
// It requires a valid sql statement and its struct
func (h *PostgreSQLDBHandler) Execute(stmt string, model interface{}) (sql.Result, error) {
	res, err := h.Conn.NamedExec(stmt, model)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// Query selects rows given by the sql statement
// It requires the statement, the model to bind the statement, and the target bind model for the results
func (h *PostgreSQLDBHandler) Query(qstmt string, model interface{}, bindModel interface{}) error {
	nstmt, err := h.Conn.PrepareNamed(qstmt)
	if err != nil {
		return err
	}
	defer nstmt.Close()

	return nstmt.Select(bindModel, model)
}

// QueryRow selects a row given by the sql statement
// It requires the statement, the model to bind the statement, and the target bind model for the result
func (h *PostgreSQLDBHandler) QueryRow(qstmt string, model interface{}, bindModel interface{}) error {
	nstmt, err := h.Conn.PrepareNamed(qstmt)
	if err != nil {
		return err
	}
	defer nstmt.Close()

	return nstmt.Get(bindModel, model)
}

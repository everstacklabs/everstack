package database

// Type represents the selected database backend.
type Type string

const (
	TypeMemory     Type = "memory"
	TypePostgres   Type = "postgres"
	TypeClickHouse Type = "clickhouse"
)

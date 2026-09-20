package validator

// DatabaseConfig maps the top-level database section.
type DatabaseConfig struct {
	Mode       string            `mapstructure:"mode"`
	Type       string            `mapstructure:"type"`
	Options    map[string]string `mapstructure:"options"`
	Postgres   PostgresDB        `mapstructure:"postgres"`
	ClickHouse ClickHouseDB      `mapstructure:"clickhouse"`
	Memory     MemoryDB          `mapstructure:"memory"`
}

type PostgresDB struct {
	DSN         string `mapstructure:"dsn"`
	MaxOpen     int    `mapstructure:"max_open_conns"`
	MaxIdle     int    `mapstructure:"max_idle_conns"`
	MaxLifetime string `mapstructure:"conn_max_lifetime"`
}

type ClickHouseDB struct {
	DSN         string `mapstructure:"dsn"`
	MaxOpen     int    `mapstructure:"max_open_conns"`
	MaxIdle     int    `mapstructure:"max_idle_conns"`
	MaxLifetime string `mapstructure:"conn_max_lifetime"`
	// Optional keeps the transactional API available when the analytics store
	// is offline. It is false by default so existing self-hosted deployments
	// retain their current strict startup behavior unless explicitly opted in.
	Optional bool `mapstructure:"optional"`
}

type MemoryDB struct {
	Enabled bool `mapstructure:"enabled"`
}

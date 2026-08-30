package validator

import "time"

// CacheConfig represents the cache configuration
type CacheConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Type    string `mapstructure:"type"` // "memory", "redis"

	Memory   MemoryCacheConfig   `mapstructure:"memory"`
	Redis    RedisCacheConfig    `mapstructure:"redis"`
	Semantic SemanticCacheConfig `mapstructure:"semantic"`
}

// MemoryCacheConfig configures in-memory caching
type MemoryCacheConfig struct {
	MaxSize int           `mapstructure:"max_size"`
	TTL     time.Duration `mapstructure:"ttl"`
}

// RedisCacheConfig configures Redis-based caching
type RedisCacheConfig struct {
	Address  string        `mapstructure:"address"`
	Password string        `mapstructure:"password"`
	DB       int           `mapstructure:"db"`
	TTL      time.Duration `mapstructure:"ttl"`
	PoolSize int           `mapstructure:"pool_size"`

	// RedisSearch configuration for vector similarity
	Search RedisSearchConfig `mapstructure:"search"`
}

// RedisSearchConfig configures RedisSearch module settings
type RedisSearchConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	IndexName string `mapstructure:"index_name"`
}

// SemanticCacheConfig configures semantic similarity caching
type SemanticCacheConfig struct {
	Enabled             bool          `mapstructure:"enabled"`
	MaxEntries          int           `mapstructure:"max_entries"`
	TTL                 time.Duration `mapstructure:"ttl"`
	SimilarityThreshold float64       `mapstructure:"similarity_threshold"`
	Backend             string        `mapstructure:"backend"` // "redis", "memory", "auto"

	Embedding EmbeddingCacheConfig `mapstructure:"embedding"`
	Tokenizer TokenizerConfig      `mapstructure:"tokenizer"`
}

// EmbeddingCacheConfig configures embedding generation for semantic cache
type EmbeddingCacheConfig struct {
	// Model name to use for embeddings (resolved via router from gateway.models)
	// Examples: "text-embedding-3-small", "embed-english-v3.0", "nomic-embed-text"
	Model string `mapstructure:"model"`

	// Optional: Dimensions (auto-detected if not specified)
	Dimensions int `mapstructure:"dimensions"`

	// Cache embeddings to avoid re-computation
	CacheEmbeddings bool          `mapstructure:"cache_embeddings"`
	CacheTTL        time.Duration `mapstructure:"cache_ttl"`

	// Batch processing
	BatchSize int           `mapstructure:"batch_size"`
	Timeout   time.Duration `mapstructure:"timeout"`
}

// TokenizerConfig configures text tokenization
type TokenizerConfig struct {
	Type      string `mapstructure:"type"` // "bpe", "simple"
	VocabFile string `mapstructure:"vocab_file"`
}

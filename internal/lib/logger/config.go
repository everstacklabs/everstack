package logger

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// Config is the configuration for the logger
type Config struct {
	Level         string    `json:"level" mapstructure:"level"`
	IncludeLevels []string  `json:"include_levels" mapstructure:"include_levels"` // Only show these specific levels
	ExcludeLevels []string  `json:"exclude_levels" mapstructure:"exclude_levels"` // Exclude these specific levels
	Formatter     formatter `json:"formatter" mapstructure:"formatter"`
	LocalLogger   bool      `json:"localLogger" mapstructure:"localLogger"`
	AddSource     bool      `json:"addSource" mapstructure:"addSource"`
}

type formatter struct {
	Format string                 `json:"format" mapstructure:"format"`
	Data   map[string]interface{} `json:"data" mapstructure:"data"`
}

type loggerConfig Config

func (c *Config) UnmarshalYAML(unmarshal func(interface{}) error) error {
	log = (*logger)(logrus.New())
	err := unmarshal((*loggerConfig)(c))
	if err != nil {
		return err
	}
	return c.SetLogger()
}

func (c *Config) UnmarshalJSON(data []byte) error {
	log = (*logger)(logrus.New())
	err := json.Unmarshal(data, (*loggerConfig)(c))
	if err != nil {
		return err
	}
	return c.SetLogger()
}

func (c *Config) SetLogger() (err error) {
	err = c.parseFormatter()
	if err != nil {
		return err
	}
	err = c.parseLevel()
	if err != nil {
		return err
	}
	err = c.unmarshalFormatter()
	if err != nil {
		return err
	}
	c.setupLevelFiltering() // Add level filtering
	c.setGlobal()
	return nil
}

func (c *Config) setGlobal() {
	if c.LocalLogger {
		return
	}
	// Preserve the current formatter (which might be our FilteringFormatter)
	currentFormatter := log.Formatter
	logrus.SetFormatter(currentFormatter)
	logrus.SetLevel(log.Level)
	logrus.SetReportCaller(log.ReportCaller)
	log = (*logger)(logrus.StandardLogger())
}

func (c *Config) unmarshalFormatter() error {
	formatterData, err := json.Marshal(c.Formatter.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(formatterData, log.Formatter)
}

func (c *Config) parseLevel() error {
	if c.Level == "" {
		log.Level = logrus.InfoLevel
		return nil
	}
	level, err := logrus.ParseLevel(c.Level)
	if err != nil {
		return err
	}
	log.Level = level
	return nil
}

// setupLevelFiltering configures the logger to filter levels based on IncludeLevels and ExcludeLevels
func (c *Config) setupLevelFiltering() {
	// If no filtering is configured, use standard behavior
	if len(c.IncludeLevels) == 0 && len(c.ExcludeLevels) == 0 {
		return
	}

	// Create level maps for filtering
	includeLevels := make(map[logrus.Level]bool)
	excludeLevels := make(map[logrus.Level]bool)

	// Parse include levels
	for _, levelStr := range c.IncludeLevels {
		if level, err := logrus.ParseLevel(strings.ToLower(levelStr)); err == nil {
			includeLevels[level] = true
		}
	}

	// Parse exclude levels
	for _, levelStr := range c.ExcludeLevels {
		if level, err := logrus.ParseLevel(strings.ToLower(levelStr)); err == nil {
			excludeLevels[level] = true
		}
	}

	// Create a custom formatter that filters levels
	originalFormatter := log.Formatter
	log.Formatter = &FilteringFormatter{
		original:      originalFormatter,
		includeLevels: includeLevels,
		excludeLevels: excludeLevels,
	}
}

// FilteringFormatter wraps an existing formatter and filters log levels
type FilteringFormatter struct {
	original      logrus.Formatter
	includeLevels map[logrus.Level]bool
	excludeLevels map[logrus.Level]bool
}

// Format implements logrus.Formatter
func (f *FilteringFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// If include levels are specified, only allow those levels
	if len(f.includeLevels) > 0 {
		if !f.includeLevels[entry.Level] {
			// Return empty bytes to effectively filter out this log entry
			return []byte{}, nil
		}
	}

	// If exclude levels are specified, skip those levels
	if len(f.excludeLevels) > 0 {
		if f.excludeLevels[entry.Level] {
			// Return empty bytes to effectively filter out this log entry
			return []byte{}, nil
		}
	}

	// Use the original formatter
	return f.original.Format(entry)
}

const (
	FormatterText    = "text"
	FormatterJSON    = "json"
	FormatterBracket = "bracket"
)

func (c *Config) parseFormatter() error {
	switch c.Formatter.Format {
	case FormatterJSON:
		log.Formatter = &logrus.JSONFormatter{}
	// case FormatterText, "":
	// 	log.Formatter = &logrus.TextFormatter{}
	case FormatterBracket, FormatterText, "":
		log.Formatter = NewDefaultBracketFormatter()
	default:
		return fmt.Errorf("%s formatter not supported", c.Formatter)
	}
	return nil
}

// Slog constructs a slog.Logger with the Formatter and Level from config.
func (c *Config) Slog() *slog.Logger {
	logger := slog.Default()

	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		logger.Warn("invalid config, using default slog", "err", err)
		return logger
	}
	opts := &slog.HandlerOptions{
		AddSource:   c.AddSource,
		Level:       level,
		ReplaceAttr: c.fieldMapToPlaceKey(),
	}

	switch c.Formatter.Format {
	case FormatterText:
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	case FormatterJSON:
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	case "":
		logger.Warn("no slog format in config, using text handler")
	default:
		logger.Warn("unknown slog format in config, using text handler", "format", c.Formatter.Format)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func (c *Config) fieldMapToPlaceKey() func(groups []string, a slog.Attr) slog.Attr {
	fieldMap, ok := c.Formatter.Data["fieldmap"].(map[string]interface{})
	if !ok {
		return nil
	}
	return func(groups []string, a slog.Attr) slog.Attr {
		for key, newKey := range fieldMap {
			if a.Key == key {
				a.Key = newKey.(string)
				return a
			}
		}
		return a
	}
}

package validator

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// logLevelFrom loads a config that sets server.log.level to info in YAML and
// returns the level after the registered environment variables are bound.
func logLevelFrom(t *testing.T) string {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader("server:\n  log:\n    level: info\n")); err != nil {
		t.Fatalf("read config: %v", err)
	}
	BindEnvVars(v)
	return v.GetString("server.log.level")
}

// EVS_LOG_LEVEL is what the docs, the Helm values and CI all set. It has to
// reach the logger: unbound, an operator asking for warn got info.
func TestLogLevelEnvVarOverridesYAML(t *testing.T) {
	t.Setenv("EVS_LOG_LEVEL", "warn")
	if got := logLevelFrom(t); got != "warn" {
		t.Fatalf("server.log.level = %q with EVS_LOG_LEVEL=warn, want warn", got)
	}
}

func TestLogLevelFallsBackToYAMLWithoutEnvVar(t *testing.T) {
	t.Setenv("EVS_LOG_LEVEL", "")
	if got := logLevelFrom(t); got != "info" {
		t.Fatalf("server.log.level = %q without the env var, want the YAML value", got)
	}
}

// The variable was ignored for a long time, so deployments exist that set it
// to something the logger cannot parse. Honouring it must not turn those into
// a gateway that refuses to start: the value is skipped and YAML applies.
func TestUnparseableLogLevelEnvVarIsIgnored(t *testing.T) {
	for _, value := range []string{"critical", "verbose", "off", " info", "notice"} {
		t.Setenv("EVS_LOG_LEVEL", value)
		if got := logLevelFrom(t); got != "info" {
			t.Fatalf("server.log.level = %q with EVS_LOG_LEVEL=%q, want the YAML value", got, value)
		}
	}
}

// Spellings the logger does accept keep working.
func TestLogLevelEnvVarAcceptsLoggerSpellings(t *testing.T) {
	for _, value := range []string{"WARN", "warning", "Debug", "error", "trace"} {
		t.Setenv("EVS_LOG_LEVEL", value)
		if got := logLevelFrom(t); got != value {
			t.Fatalf("server.log.level = %q with EVS_LOG_LEVEL=%q, want it passed through", got, value)
		}
	}
}

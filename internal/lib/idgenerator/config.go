package idgenerator

const (
	DefaultWebhookPath = "http://metadata.google.internal/computeMetadata/v1/instance/id"
)

type Config struct {
	Identification Identification
}

type Identification struct {
	PrivateIp PrivateIp
	Hostname  Hostname
	Webhook   Webhook
}

type PrivateIp struct {
	Enabled bool
}

type Hostname struct {
	Enabled bool
}

type Webhook struct {
	Enabled  bool
	Url      string
	JSONPath *string
	Headers  *map[string]string
}

func Configure(config *Config) {
	if config != nil {
		GeneratorConfig = config
	}
}

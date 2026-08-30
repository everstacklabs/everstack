package docker

import _ "embed"

//go:embed Dockerfile.base
var EmbeddedDockerfile []byte

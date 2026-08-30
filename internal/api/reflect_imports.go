package api

// Ensure service descriptors are linked into the gateway binary so server
// reflection can expose them even when the service is reverse-proxied.
import (
	_ "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
)

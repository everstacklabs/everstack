package serve

import "time"

const (
	// Public listener timeouts. There is deliberately no WriteTimeout and no
	// blanket ReadTimeout: a WriteTimeout caps the whole response, which would
	// cut long SSE/LLM streams, and a ReadTimeout caps the whole request body,
	// which would cut large uploads. ReadHeaderTimeout closes the slowloris gap
	// (a client that trickles headers) without touching either; IdleTimeout
	// reaps keep-alive connections that sit unused between requests.
	publicReadHeaderTimeout = 10 * time.Second
	publicIdleTimeout       = 120 * time.Second
)

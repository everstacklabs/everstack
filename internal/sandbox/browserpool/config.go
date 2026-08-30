// Package browserpool manages tenant-isolated Chromium pods independently of
// the sandbox backend used to execute an agent.
package browserpool

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Pod-count defaults are deliberately small because browser pods land in the
// sandboxes namespace and draw on the SAME ResourceQuota as ordinary sandbox
// pods. A browser pod requests 500m CPU / 512Mi and is limited to 1000m / 1Gi,
// so the real ceiling is set by whichever quota dimension binds first, not by
// the pod count. Measured against the live clusters (2026-07-22):
//
//	dev  (pods 40, requests.cpu 8,  limits.memory 20Gi) => ~16 browser pods
//	prod (pods 20, requests.cpu 4,  limits.memory 6Gi)  => ~6 browser pods
//
// Those ceilings assume ZERO sandboxes running, so the pool must claim only a
// minority slice or it starves sandbox creation. Operators raise these via
// EVS_BROWSER_POOL_MAX_PODS / EVS_BROWSER_POOL_MAX_IDLE_PER_TENANT after
// raising the namespace quota. Idle pods hold their reservation while doing
// nothing, which is why the per-tenant idle default is 1 rather than 2.
const (
	defaultNamespace   = "everstack-sandboxes"
	defaultIdleTTL     = 5 * time.Minute
	defaultLeaseTTL    = 2 * time.Minute
	defaultMaxIdle     = 1
	defaultMaxPods     = 3
	defaultLabelPrefix = "everstack.browserpool"
	defaultCDPPort     = 9222
	defaultStreamPort  = 6080
	reaperInterval     = 30 * time.Second
	managedByValue     = "everstack-browserpool"
)

type Config struct {
	Namespace        string
	Kubeconfig       string
	Image            string
	IdleTTL          time.Duration
	LeaseTTL         time.Duration
	MaxIdlePerTenant int
	MaxPodsTotal     int
	LabelPrefix      string
}

type Lease struct {
	CDPBaseURL string
	StreamURL  string
	PodName    string
	TenantID   string
	SessionID  string
}

func (c Config) withDefaults() (Config, error) {
	if c.Namespace == "" {
		c.Namespace = defaultNamespace
	}
	if c.Image == "" {
		c.Image = sandbox.DefaultBrowserImage
	}
	if c.IdleTTL == 0 {
		c.IdleTTL = defaultIdleTTL
	}
	if c.LeaseTTL == 0 {
		c.LeaseTTL = defaultLeaseTTL
	}
	if c.MaxIdlePerTenant == 0 {
		c.MaxIdlePerTenant = defaultMaxIdle
	}
	if c.MaxPodsTotal == 0 {
		c.MaxPodsTotal = defaultMaxPods
	}
	if c.LabelPrefix == "" {
		c.LabelPrefix = defaultLabelPrefix
	}

	if c.IdleTTL < 0 {
		return Config{}, fmt.Errorf("browserpool: IdleTTL must not be negative")
	}
	if c.LeaseTTL < 0 {
		return Config{}, fmt.Errorf("browserpool: LeaseTTL must not be negative")
	}
	if c.LeaseTTL <= reaperInterval {
		return Config{}, fmt.Errorf("browserpool: LeaseTTL must be greater than the reaper interval %s", reaperInterval)
	}
	if c.MaxIdlePerTenant < 0 {
		return Config{}, fmt.Errorf("browserpool: MaxIdlePerTenant must not be negative")
	}
	if c.MaxPodsTotal < 1 {
		return Config{}, fmt.Errorf("browserpool: MaxPodsTotal must be positive")
	}
	if problems := validation.IsDNS1123Subdomain(c.LabelPrefix); len(problems) != 0 {
		return Config{}, fmt.Errorf("browserpool: invalid LabelPrefix %q: %s", c.LabelPrefix, problems[0])
	}

	return c, nil
}

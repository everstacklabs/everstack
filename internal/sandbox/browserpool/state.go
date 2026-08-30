package browserpool

import (
	"fmt"
	"sort"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

type podState uint8

const (
	podIdle podState = iota
	podBinding
	podBound
	podResetting
	podDeleting
)

type browserSettings struct {
	headless   bool
	cdpPort    int
	streamPort int
}

func settingsFromConfig(cfg sandbox.BrowserConfig) (browserSettings, error) {
	settings := browserSettings{
		headless:   cfg.Headless,
		cdpPort:    cfg.CDPPort,
		streamPort: cfg.StreamPort,
	}
	if settings.cdpPort == 0 {
		settings.cdpPort = defaultCDPPort
	}
	if settings.streamPort == 0 {
		settings.streamPort = defaultStreamPort
	}
	if settings.cdpPort < 1 || settings.cdpPort > 65535 {
		return browserSettings{}, fmt.Errorf("browserpool: CDP port must be between 1 and 65535")
	}
	if settings.streamPort < 1 || settings.streamPort > 65535 {
		return browserSettings{}, fmt.Errorf("browserpool: stream port must be between 1 and 65535")
	}
	return settings, nil
}

type managedPod struct {
	name        string
	tenantID    string
	sessionID   string
	state       podState
	idleSince   time.Time
	settings    browserSettings
	cdpBaseURL  string
	streamURL   string
	labelValues map[string]string
}

func (pod *managedPod) lease(sessionID string) *Lease {
	streamURL := ""
	if !pod.settings.headless {
		streamURL = pod.streamURL
	}
	return &Lease{
		CDPBaseURL: pod.cdpBaseURL,
		StreamURL:  streamURL,
		PodName:    pod.name,
		TenantID:   pod.tenantID,
		SessionID:  sessionID,
	}
}

func cloneLease(lease *Lease) *Lease {
	if lease == nil {
		return nil
	}
	copy := *lease
	return &copy
}

// selectIdlePod checks the immutable tenant label as well as in-memory state.
// This duplicate check keeps every reuse path fail-closed if state is ever
// populated incorrectly.
func selectIdlePod(pods map[string]*managedPod, tenantLabel, tenantID string, settings browserSettings) *managedPod {
	var selected *managedPod
	for _, pod := range pods {
		if pod.state != podIdle || pod.tenantID != tenantID {
			continue
		}
		if pod.labelValues[tenantLabel] != tenantID || pod.settings != settings {
			continue
		}
		if selected == nil || pod.idleSince.After(selected.idleSince) {
			selected = pod
		}
	}
	return selected
}

func idlePodsToTrim(pods []*managedPod, now time.Time, idleTTL time.Duration, maxIdlePerTenant int) []*managedPod {
	selected := make(map[*managedPod]struct{})
	byTenant := make(map[string][]*managedPod)

	for _, pod := range pods {
		if pod.state != podIdle {
			continue
		}
		if now.Sub(pod.idleSince) > idleTTL {
			selected[pod] = struct{}{}
			continue
		}
		byTenant[pod.tenantID] = append(byTenant[pod.tenantID], pod)
	}

	for _, tenantPods := range byTenant {
		if len(tenantPods) <= maxIdlePerTenant {
			continue
		}
		sort.Slice(tenantPods, func(i, j int) bool {
			return tenantPods[i].idleSince.Before(tenantPods[j].idleSince)
		})
		for _, pod := range tenantPods[:len(tenantPods)-maxIdlePerTenant] {
			selected[pod] = struct{}{}
		}
	}

	result := make([]*managedPod, 0, len(selected))
	for pod := range selected {
		result = append(result, pod)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].idleSince.Before(result[j].idleSince)
	})
	return result
}

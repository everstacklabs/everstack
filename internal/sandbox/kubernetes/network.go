package kubernetes

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// applyNetworkPolicy creates a NetworkPolicy for the sandbox pod based on
// the configured network mode.
func (b *KubernetesBackend) applyNetworkPolicy(ctx context.Context, id string, config sandbox.InstanceConfig) error {
	prefix := b.config.LabelPrefix

	switch config.NetworkMode {
	case sandbox.NetworkAllow:
		// No NetworkPolicy = allow all (default K8s behavior)
		return nil

	case sandbox.NetworkDeny:
		return b.createDenyPolicy(ctx, id, prefix, config.SessionID)

	case sandbox.NetworkWhitelist:
		return b.createWhitelistPolicy(ctx, id, prefix, config.SessionID, config.AllowedHosts)

	default:
		// Default to deny for unknown modes
		return b.createDenyPolicy(ctx, id, prefix, config.SessionID)
	}
}

// createDenyPolicy creates a NetworkPolicy that blocks all egress traffic.
func (b *KubernetesBackend) createDenyPolicy(ctx context.Context, id, prefix, sessionID string) error {
	name := podName(id) + "-deny"

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.config.Namespace,
			Labels: map[string]string{
				prefix + "/id":         id,
				prefix + "/managed-by": "everstack",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					prefix + "/session-id": sessionID,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{}, // empty = deny all
		},
	}

	_, err := b.clientset.NetworkingV1().NetworkPolicies(b.config.Namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create deny NetworkPolicy: %w", err)
	}

	logger.WithFields("sandbox_id", id, "policy", name).
		Debug("k8s_sandbox: deny NetworkPolicy created")
	return nil
}

// createWhitelistPolicy creates a NetworkPolicy that allows egress only to
// specified hosts. K8s NetworkPolicy doesn't support hostname-based rules
// natively, so this uses IP-based CIDR rules. For hostname whitelisting,
// external DNS resolution would be needed.
func (b *KubernetesBackend) createWhitelistPolicy(ctx context.Context, id, prefix, sessionID string, allowedHosts []string) error {
	if len(allowedHosts) == 0 {
		logger.WithFields("sandbox_id", id).
			Warn("k8s_sandbox: whitelist mode with no allowed hosts — applying deny policy")
		return b.createDenyPolicy(ctx, id, prefix, sessionID)
	}

	name := podName(id) + "-whitelist"

	// Build egress rules — allow DNS (port 53) to cluster DNS, plus
	// all ports to allowed CIDRs/IPs.
	var egressRules []networkingv1.NetworkPolicyEgressRule

	// Allow DNS resolution (required for any network access)
	dnsPort := intstr.FromInt32(53)
	udpProto := corev1.ProtocolUDP
	egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Port: &dnsPort, Protocol: &udpProto},
		},
	})

	// Allow traffic to whitelisted hosts.
	// Note: K8s NetworkPolicy only supports CIDR-based rules, not hostnames.
	// Entries that look like CIDRs are used directly; hostnames log a warning.
	var peers []networkingv1.NetworkPolicyPeer
	for _, host := range allowedHosts {
		if isCIDR(host) {
			peers = append(peers, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: host},
			})
		} else {
			logger.WithFields("sandbox_id", id, "host", host).
				Warn("k8s_sandbox: hostname-based whitelist not supported in K8s NetworkPolicy, skipping")
		}
	}

	if len(peers) > 0 {
		egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
			To: peers,
		})
	}

	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.config.Namespace,
			Labels: map[string]string{
				prefix + "/id":         id,
				prefix + "/managed-by": "everstack",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					prefix + "/session-id": sessionID,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
			Egress: egressRules,
		},
	}

	_, err := b.clientset.NetworkingV1().NetworkPolicies(b.config.Namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create whitelist NetworkPolicy: %w", err)
	}

	logger.WithFields("sandbox_id", id, "policy", name, "allowed_cidrs", len(peers)).
		Debug("k8s_sandbox: whitelist NetworkPolicy created")
	return nil
}

// deleteNetworkPolicy removes the NetworkPolicy for a sandbox (best-effort).
func (b *KubernetesBackend) deleteNetworkPolicy(ctx context.Context, id string) error {
	prefix := b.config.LabelPrefix
	selector := prefix + "/id=" + id

	policies, err := b.clientset.NetworkingV1().NetworkPolicies(b.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return err
	}

	for _, p := range policies.Items {
		_ = b.clientset.NetworkingV1().NetworkPolicies(b.config.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{})
	}

	return nil
}

// isCIDR checks if a string looks like a CIDR notation (e.g., "10.0.0.0/8").
func isCIDR(s string) bool {
	for _, ch := range s {
		if ch == '/' {
			return true
		}
	}
	return false
}

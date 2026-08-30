package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// podName converts a sandbox ID into a K8s-safe pod name.
// K8s names: lowercase alphanumeric + hyphens, max 63 chars.
func podName(id string) string {
	name := strings.ReplaceAll(id, "_", "-")
	name = strings.ToLower(name)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// buildPodSpec creates a Pod manifest for a sandbox instance.
func (b *KubernetesBackend) buildPodSpec(id string, config sandbox.InstanceConfig) *corev1.Pod {
	prefix := b.config.LabelPrefix
	name := podName(id)
	now := time.Now()
	expiresAt := now.Add(time.Duration(config.TimeoutSeconds) * time.Second)

	// Marshal config for annotation (enables recreate from pod metadata)
	configJSON, _ := json.Marshal(config)

	labels := map[string]string{
		prefix + "/id":         id,
		prefix + "/session-id": config.SessionID,
		prefix + "/tenant-id":  config.TenantID,
		prefix + "/managed-by": "everstack",
	}
	if config.AgentID != "" {
		labels[prefix+"/persistent"] = "true"
		labels[prefix+"/agent-id"] = config.AgentID
	}

	annotations := map[string]string{
		prefix + "/expires-at":     expiresAt.Format(time.RFC3339),
		prefix + "/idle-retention": fmt.Sprintf("%d", config.TimeoutSeconds),
		prefix + "/config":         string(configJSON),
	}

	// Environment variables — set sensible defaults for package managers so
	// global installs and caches land on the writable trooper tmpfs instead
	// of the read-only root filesystem.
	var envVars []corev1.EnvVar
	workDir := config.WorkDir
	if workDir == "" {
		workDir = "/workspace"
	}
	envVars = append(envVars,
		corev1.EnvVar{Name: "HOME", Value: workDir},
		corev1.EnvVar{Name: "TMPDIR", Value: "/workspace/.tmp"},
		corev1.EnvVar{Name: "NPM_CONFIG_PREFIX", Value: "/workspace/.npm-global"},
		corev1.EnvVar{Name: "NPM_CONFIG_CACHE", Value: "/workspace/.npm-cache"},
		corev1.EnvVar{Name: "PIP_TARGET", Value: "/workspace/.pip-packages"},
		corev1.EnvVar{Name: "CARGO_HOME", Value: "/workspace/.cargo"},
		corev1.EnvVar{Name: "GOPATH", Value: "/workspace/.go"},
		corev1.EnvVar{Name: "PATH", Value: "/workspace/.npm-global/bin:/workspace/.pip-packages/bin:/workspace/.cargo/bin:/workspace/.go/bin:/usr/local/go/bin:/usr/local/cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
	)
	// User/template env vars — skip keys already set by defaults above to
	// avoid K8s "hides previous definition" warnings.
	defaultKeys := map[string]bool{
		"HOME": true, "TMPDIR": true, "NPM_CONFIG_PREFIX": true,
		"NPM_CONFIG_CACHE": true, "PIP_TARGET": true, "CARGO_HOME": true,
		"GOPATH": true, "PATH": true,
	}
	for k, v := range config.EnvVars {
		if defaultKeys[k] {
			continue
		}
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// Resource limits
	cpuLimit := resource.MustParse(fmt.Sprintf("%dm", int64(config.CPULimit*1000)))
	cpuRequest := resource.MustParse(fmt.Sprintf("%dm", int64(config.CPULimit*500)))
	memLimit := resource.MustParse(fmt.Sprintf("%dMi", config.MemoryMB))
	memRequest := resource.MustParse(fmt.Sprintf("%dMi", config.MemoryMB/2))

	diskLimit := resource.MustParse(fmt.Sprintf("%dMi", config.DiskMB))
	tmpLimit := resource.MustParse("256Mi")
	runLimit := resource.MustParse("64Mi")

	// Image pull policy
	pullPolicy := corev1.PullIfNotPresent
	switch b.config.ImagePullPolicy {
	case "Always":
		pullPolicy = corev1.PullAlways
	case "Never":
		pullPolicy = corev1.PullNever
	}

	imageName := config.Image
	if imageName == "" {
		imageName = sandbox.DefaultDevImage
	}

	falseVal := false

	// Configure explicit DNS when servers are provided. Use DNSNone to
	// fully control nameservers (avoids cluster DNS issues in sandboxes)
	// while keeping svc.cluster.local search domain so K8s service names
	// still resolve.
	var dnsPolicy corev1.DNSPolicy
	var dnsConfig *corev1.PodDNSConfig
	if len(config.DNSServers) > 0 {
		dnsPolicy = corev1.DNSNone
		dnsConfig = &corev1.PodDNSConfig{
			Nameservers: config.DNSServers,
			Searches:    []string{"svc.cluster.local", "cluster.local"},
		}
	}

	containers := []corev1.Container{
		{
			Name:            "sandbox",
			Image:           imageName,
			ImagePullPolicy: pullPolicy,
			Command:         []string{"sleep", "infinity"},
			WorkingDir:      workDir,
			Env:             envVars,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    cpuRequest,
					corev1.ResourceMemory: memRequest,
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    cpuLimit,
					corev1.ResourceMemory: memLimit,
				},
			},
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             &falseVal,
				ReadOnlyRootFilesystem:   boolPtr(true),
				AllowPrivilegeEscalation: &falseVal,
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "trooper", MountPath: workDir},
				{Name: "tmp", MountPath: "/tmp"},
				{Name: "run", MountPath: "/run"},
			},
		},
	}

	volumes := []corev1.Volume{
		{
			Name: "trooper",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: &diskLimit,
				},
			},
		},
		{
			Name: "tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: &tmpLimit,
				},
			},
		},
		{
			Name: "run",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: &runLimit,
				},
			},
		},
	}

	// Browser sidecar — Chromium runs in a separate container sharing the
	// pod's network namespace. The agent reaches it on localhost:<cdpPort>.
	if sc := config.BrowserSidecar; sc != nil {
		browserImage := sc.Image
		if browserImage == "" {
			browserImage = sandbox.DefaultBrowserImage
		}
		cdpPort := sc.CDPPort
		if cdpPort == 0 {
			cdpPort = 9222
		}
		streamPort := sc.StreamPort
		if streamPort == 0 {
			streamPort = 6080
		}
		headless := "true"
		if !sc.Headless {
			headless = "false"
		}

		browserShmLimit := resource.MustParse("256Mi")
		browserCPU := resource.MustParse("1000m")
		browserCPUReq := resource.MustParse("500m")
		browserMem := resource.MustParse("1Gi")
		browserMemReq := resource.MustParse("512Mi")

		browserTmpLimit := resource.MustParse("256Mi")

		containers = append(containers, corev1.Container{
			Name:            "browser",
			Image:           browserImage,
			ImagePullPolicy: pullPolicy,
			Env: []corev1.EnvVar{
				{Name: "BROWSER_HEADLESS", Value: headless},
				{Name: "BROWSER_CDP_PORT", Value: fmt.Sprintf("%d", cdpPort)},
				{Name: "BROWSER_STREAM_PORT", Value: fmt.Sprintf("%d", streamPort)},
				{Name: "HOME", Value: "/tmp/browser-home"},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    browserCPUReq,
					corev1.ResourceMemory: browserMemReq,
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    browserCPU,
					corev1.ResourceMemory: browserMem,
				},
			},
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             &falseVal,
				AllowPrivilegeEscalation: &falseVal,
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "browser-shm", MountPath: "/dev/shm"},
				{Name: "browser-tmp", MountPath: "/tmp"},
			},
		})

		// Chromium needs writable /dev/shm larger than the default 64MB
		volumes = append(volumes, corev1.Volume{
			Name: "browser-shm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMediumMemory,
					SizeLimit: &browserShmLimit,
				},
			},
		})
		// Writable /tmp for Chromium profiles, crash dumps, and user data dir
		volumes = append(volumes, corev1.Volume{
			Name: "browser-tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: &browserTmpLimit,
				},
			},
		})
	}

	// Pull secrets for private registries (GHCR, etc.). Without this,
	// pods referencing ghcr.io/<private-org>/* fail with ErrImagePull
	// because GHCR returns 401 for unauthenticated manifest reads on
	// private images.
	var imagePullSecrets []corev1.LocalObjectReference
	for _, s := range b.config.ImagePullSecrets {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: s})
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   b.config.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: b.config.ServiceAccount,
			NodeSelector:       b.config.NodeSelector,
			DNSPolicy:          dnsPolicy,
			DNSConfig:          dnsConfig,
			Containers:         containers,
			Volumes:            volumes,
			ImagePullSecrets:   imagePullSecrets,
		},
	}

	return pod
}

func boolPtr(v bool) *bool { return &v }

package main

// External storage mounts (POR-88 S3/R2, POR-89 GCS/Azure).
//
// The gateway injects SANDBOX_MOUNTS_JSON as a JSON array of mount
// configurations. The sandbox-agent reads this at startup and runs
// the appropriate FUSE mount command for each entry.
//
// Supported types:
//   s3   — AWS S3 via `mount-s3` (Mountpoint for Amazon S3)
//   r2   — Cloudflare R2 via `mount-s3 --endpoint-url <url>`
//   gcs  — Google Cloud Storage via `gcsfuse`
//   azure — Azure Blob Storage via `blobfuse2`
//
// Credentials: S3/R2 mounts may carry per-mount credentials inline
// (access_key_id / secret_access_key / session_token). When present, those are
// applied to the mount subprocess's environment ONLY — not the agent's process
// env — so an unrelated tenant exec doesn't inherit them. When absent, the
// mount falls back to inheriting the agent's environment (legacy behavior).
// GCS/Azure still read GOOGLE_APPLICATION_CREDENTIALS / AZURE_STORAGE_* from env.
//
// Note: a credential delivered into a sandbox is ultimately readable by the
// (root) tenant — isolation comes from SCOPING the credential (a per-tenant,
// bucket-scoped R2 token), not from hiding it.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// s3MountEnv builds the environment for an S3/R2 mount subprocess. With
// per-mount credentials, it starts from a copy of the agent env with any
// inherited AWS_* creds stripped, then injects only this mount's creds — so the
// long-lived mount daemon holds exactly this volume's (tenant-scoped) token and
// nothing else. Without per-mount creds, it inherits the agent env unchanged.
func s3MountEnv(m mountConfig) []string {
	if m.AccessKeyID == "" && m.SecretAccessKey == "" {
		return os.Environ()
	}
	base := os.Environ()
	env := make([]string, 0, len(base)+3)
	for _, e := range base {
		if strings.HasPrefix(e, "AWS_ACCESS_KEY_ID=") ||
			strings.HasPrefix(e, "AWS_SECRET_ACCESS_KEY=") ||
			strings.HasPrefix(e, "AWS_SESSION_TOKEN=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "AWS_ACCESS_KEY_ID="+m.AccessKeyID)
	env = append(env, "AWS_SECRET_ACCESS_KEY="+m.SecretAccessKey)
	if m.SessionToken != "" {
		env = append(env, "AWS_SESSION_TOKEN="+m.SessionToken)
	}
	return env
}

// applyStorageMounts reads SANDBOX_MOUNTS_JSON and mounts each configured store.
func applyStorageMounts() {
	raw := os.Getenv("SANDBOX_MOUNTS_JSON")
	if raw == "" {
		return
	}
	var mounts []mountConfig
	if err := json.Unmarshal([]byte(raw), &mounts); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent: mounts: invalid SANDBOX_MOUNTS_JSON: %v\n", err)
		return
	}
	for _, m := range mounts {
		if err := applyMount(m); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox-agent: mount %s@%s: %v\n", m.Type, m.MountPath, err)
		}
	}
}

func applyMount(m mountConfig) error {
	// Ensure mount path exists.
	if err := os.MkdirAll(m.MountPath, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", m.MountPath, err)
	}

	switch strings.ToLower(m.Type) {
	case "s3", "r2":
		return mountS3(m)
	case "gcs":
		return mountGCS(m)
	case "azure":
		return mountAzure(m)
	default:
		return fmt.Errorf("unsupported mount type: %s", m.Type)
	}
}

func mountS3(m mountConfig) error {
	bin, err := exec.LookPath("mount-s3")
	if err != nil {
		// Fallback: rclone mount.
		return mountViaRclone(m)
	}
	args := []string{m.Bucket, m.MountPath}
	if m.Endpoint != "" {
		args = append([]string{"--endpoint-url", m.Endpoint}, args...)
	}
	if m.SubPath != "" {
		args = append([]string{"--prefix", m.SubPath + "/"}, args...)
	}
	if m.ReadOnly {
		args = append([]string{"--read-only"}, args...)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = s3MountEnv(m)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mount-s3: %w", err)
	}
	fmt.Fprintf(os.Stderr, "sandbox-agent: mounted s3://%s → %s\n", m.Bucket, m.MountPath)
	return nil
}

func mountGCS(m mountConfig) error {
	bin, err := exec.LookPath("gcsfuse")
	if err != nil {
		return fmt.Errorf("gcsfuse not found (install it in the sandbox image)")
	}
	bucket := m.Bucket
	if m.SubPath != "" {
		bucket += ":" + m.SubPath
	}
	args := []string{"--implicit-dirs", "--foreground=false", bucket, m.MountPath}
	if m.ReadOnly {
		args = append([]string{"-o", "ro"}, args...)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gcsfuse: %w", err)
	}
	fmt.Fprintf(os.Stderr, "sandbox-agent: mounted gcs://%s → %s\n", m.Bucket, m.MountPath)
	return nil
}

func mountAzure(m mountConfig) error {
	bin, err := exec.LookPath("blobfuse2")
	if err != nil {
		return fmt.Errorf("blobfuse2 not found (install it in the sandbox image)")
	}
	// Write a temp config file for blobfuse2.
	cfgContent := fmt.Sprintf(`
logging:
  type: syslog
components:
  - libfuse
  - block_cache
  - attr_cache
  - azstorage
azstorage:
  type: block
  account-name: %s
  account-key: %s
  container: %s
  mode: key
`, os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_KEY"), m.Bucket)
	cfgFile := "/tmp/blobfuse2-" + strings.ReplaceAll(m.MountPath, "/", "_") + ".yaml"
	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0600); err != nil {
		return fmt.Errorf("write blobfuse config: %w", err)
	}
	cmd := exec.Command(bin, "mount", m.MountPath, "--config-file="+cfgFile)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("blobfuse2: %w", err)
	}
	fmt.Fprintf(os.Stderr, "sandbox-agent: mounted azure://%s → %s\n", m.Bucket, m.MountPath)
	return nil
}

func mountViaRclone(m mountConfig) error {
	bin, err := exec.LookPath("rclone")
	if err != nil {
		return fmt.Errorf("neither mount-s3 nor rclone found")
	}
	remote := fmt.Sprintf(":s3:%s", m.Bucket)
	if m.SubPath != "" {
		remote += "/" + m.SubPath
	}
	args := []string{"mount", remote, m.MountPath, "--daemon", "--vfs-cache-mode=minimal"}
	if m.Endpoint != "" {
		args = append(args, "--s3-endpoint="+m.Endpoint)
	}
	if m.ReadOnly {
		args = append(args, "--read-only")
	}
	cmd := exec.Command(bin, args...)
	// rclone's :s3: backend reads AWS_ACCESS_KEY_ID/SECRET/SESSION_TOKEN.
	cmd.Env = s3MountEnv(m)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rclone mount: %w", err)
	}
	fmt.Fprintf(os.Stderr, "sandbox-agent: mounted (rclone) s3://%s → %s\n", m.Bucket, m.MountPath)
	return nil
}

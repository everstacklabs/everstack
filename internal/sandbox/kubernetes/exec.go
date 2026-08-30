package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// Exec runs a command inside the sandbox pod via the K8s exec API.
func (b *KubernetesBackend) Exec(ctx context.Context, id string, req sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	start := time.Now()

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name := podName(id)

	// Build the command — if env vars are provided, wrap with env prefix
	cmd := req.Command
	if len(req.Env) > 0 {
		envPrefix := make([]string, 0, len(req.Env)+2)
		envPrefix = append(envPrefix, "env")
		for k, v := range req.Env {
			envPrefix = append(envPrefix, fmt.Sprintf("%s=%s", k, v))
		}
		cmd = append(envPrefix, cmd...)
	}

	// WorkDir handling: wrap in sh -c 'cd <dir> && ...' if non-default
	workDir := req.WorkDir
	if workDir != "" && workDir != "/workspace" {
		shellCmd := fmt.Sprintf("cd '%s' && %s", workDir, strings.Join(cmd, " "))
		cmd = []string{"sh", "-c", shellCmd}
	}

	execReq := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(name).
		Namespace(b.config.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "sandbox",
			Command:   cmd,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", execReq.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	duration := time.Since(start).Milliseconds()

	// Check for timeout
	if execCtx.Err() != nil {
		return &sandbox.ExecResult{
			ExitCode:   -1,
			Stdout:     truncateOutput(stdout.String()),
			Stderr:     truncateOutput(stderr.String()),
			DurationMs: duration,
			TimedOut:   true,
		}, nil
	}

	exitCode := 0
	if err != nil {
		// Try to extract exit code from error
		if exitErr, ok := err.(interface{ ExitStatus() int }); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			// Infrastructure error (pod not found, container not running, etc.)
			// — surface the error message in stderr so the caller sees it.
			exitCode = 1
			if stderr.Len() == 0 {
				stderr.WriteString(err.Error())
			}
		}
	}

	// Capture exec output into the application log buffer, unless this is an
	// internal introspection exec (SilentLog) whose plumbing would just be
	// noise in the user-facing Logs tab.
	if !req.SilentLog {
		sl := b.getOrCreateLogs(id)
		now := time.Now()
		cmdStr := strings.Join(req.Command, " ")
		sl.Append(logbuffer.Entry{Timestamp: now, Stream: "system", Line: fmt.Sprintf("$ %s", cmdStr)})
		if s := stdout.String(); s != "" {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				sl.Append(logbuffer.Entry{Timestamp: now, Stream: "stdout", Line: line})
			}
		}
		if s := stderr.String(); s != "" {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				sl.Append(logbuffer.Entry{Timestamp: now, Stream: "stderr", Line: line})
			}
		}
	}

	return &sandbox.ExecResult{
		ExitCode:   exitCode,
		Stdout:     truncateOutput(stdout.String()),
		Stderr:     truncateOutput(stderr.String()),
		DurationMs: duration,
		TimedOut:   false,
	}, nil
}

// WriteFile writes content to a path inside the sandbox pod.
func (b *KubernetesBackend) WriteFile(ctx context.Context, id string, path string, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	dir := filepath.Dir(path)
	cmd := fmt.Sprintf("mkdir -p '%s' && echo '%s' | base64 -d > '%s'", dir, encoded, path)

	result, err := b.Exec(ctx, id, sandbox.ExecRequest{
		Command: []string{"sh", "-c", cmd},
		Timeout: 15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("write file exec failed: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write file failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}
	return nil
}

// ReadFile reads content from a path inside the sandbox pod.
func (b *KubernetesBackend) ReadFile(ctx context.Context, id string, path string) ([]byte, error) {
	result, err := b.Exec(ctx, id, sandbox.ExecRequest{
		Command: []string{"base64", path},
		Timeout: 15 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("read file exec failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("read file failed: %s", result.Stderr)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result.Stdout))
	if err != nil {
		return nil, fmt.Errorf("failed to decode file content: %w", err)
	}
	return decoded, nil
}

// ListFiles lists directory contents inside the sandbox pod.
func (b *KubernetesBackend) ListFiles(ctx context.Context, id string, path string) ([]sandbox.FileInfo, error) {
	result, err := b.Exec(ctx, id, sandbox.ExecRequest{
		Command: []string{"find", path, "-maxdepth", "1", "-printf", "%f\t%s\t%y\n"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("list files exec failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("list files failed: %s", result.Stderr)
	}

	var files []sandbox.FileInfo
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		name := parts[0]
		if name == "" || name == "." {
			continue // Skip the directory itself
		}
		var size int64
		fmt.Sscanf(parts[1], "%d", &size)
		files = append(files, sandbox.FileInfo{
			Name:  name,
			Path:  filepath.Join(path, name),
			Size:  size,
			IsDir: parts[2] == "d",
		})
	}

	return files, nil
}

// truncateOutput caps output at 1MB to prevent memory exhaustion.
func truncateOutput(s string) string {
	const maxLen = 1 << 20 // 1MB
	if len(s) > maxLen {
		return s[:maxLen] + "\n... (output truncated at 1MB)"
	}
	return s
}

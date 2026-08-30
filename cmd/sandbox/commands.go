package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	var (
		sessionID         string
		name              string
		templateID        string
		image             string
		cpuLimit          float64
		memoryMB          int64
		diskMB            int64
		timeoutSeconds    int
		networkMode       string
		idleRetention     int
		gitRepoURL        string
		fromGitHub        string
		gitBranch         string
		gitInstallationID int64
		sshEnabled        bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a sandbox instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]interface{}{}
			if tenantID != "" {
				payload["tenant_id"] = tenantID
			}
			if sessionID != "" {
				payload["session_id"] = sessionID
			}
			if name != "" {
				payload["name"] = name
			}
			if templateID != "" {
				payload["template_id"] = templateID
			}
			if image != "" {
				payload["image"] = image
			}
			if cpuLimit > 0 {
				payload["cpu_limit"] = cpuLimit
			}
			if memoryMB > 0 {
				payload["memory_mb"] = memoryMB
			}
			if diskMB > 0 {
				payload["disk_mb"] = diskMB
			}
			if timeoutSeconds > 0 {
				payload["timeout_seconds"] = timeoutSeconds
			}
			if networkMode != "" {
				payload["network_mode"] = networkMode
			}
			if idleRetention != 0 {
				payload["idle_retention_seconds"] = idleRetention
			}

			if fromGitHub != "" && gitRepoURL != "" {
				return fmt.Errorf("use either --from-github or --git-repo-url, not both")
			}
			if fromGitHub != "" {
				payload["git_repo_url"] = fmt.Sprintf("https://github.com/%s.git", strings.TrimPrefix(strings.TrimSpace(fromGitHub), "https://github.com/"))
			} else if gitRepoURL != "" {
				payload["git_repo_url"] = gitRepoURL
			}
			if gitBranch != "" {
				payload["git_branch"] = gitBranch
			}
			if gitInstallationID > 0 {
				payload["git_installation_id"] = gitInstallationID
			}
			if cmd.Flags().Changed("ssh-enabled") {
				payload["ssh_enabled"] = sshEnabled
			}

			resp, err := newClient().CreateSandbox(payload)
			if err != nil {
				return err
			}

			if outputJSON {
				return writeJSON(cmd, resp)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sandbox created: %s\n", stringField(resp, "id"))
			fmt.Fprintf(cmd.OutOrStdout(), "Session ID: %s\n", stringField(resp, "session_id", "sessionId"))
			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", stringField(resp, "status"))
			fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s\n", stringField(resp, "backend"))
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID (auto-generated when omitted)")
	cmd.Flags().StringVar(&name, "name", "", "friendly sandbox name")
	cmd.Flags().StringVar(&templateID, "template", "", "sandbox template id/slug")
	cmd.Flags().StringVar(&image, "image", "", "container image")
	cmd.Flags().Float64Var(&cpuLimit, "cpu", 0, "CPU limit")
	cmd.Flags().Int64Var(&memoryMB, "memory-mb", 0, "memory in MB")
	cmd.Flags().Int64Var(&diskMB, "disk-mb", 0, "disk in MB")
	cmd.Flags().IntVar(&timeoutSeconds, "timeout-seconds", 0, "execution timeout in seconds")
	cmd.Flags().StringVar(&networkMode, "network-mode", "", "network mode: deny|whitelist|allow")
	cmd.Flags().IntVar(&idleRetention, "idle-retention-seconds", 0, "idle retention in seconds; 0 uses plan defaults")
	cmd.Flags().StringVar(&fromGitHub, "from-github", "", "GitHub repository in owner/repo format")
	cmd.Flags().StringVar(&gitRepoURL, "git-repo-url", "", "Git repository URL")
	cmd.Flags().StringVar(&gitBranch, "git-branch", "", "Git branch to clone")
	cmd.Flags().Int64Var(&gitInstallationID, "git-installation-id", 0, "GitHub App installation ID")
	cmd.Flags().BoolVar(&sshEnabled, "ssh-enabled", false, "request SSH for sandbox (scaffold for phase 4)")
	return cmd
}

func newListCmd() *cobra.Command {
	var (
		status string
		limit  int
		offset int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sandbox instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			instances, total, err := newClient().ListSandboxInstances(status, limit, offset)
			if err != nil {
				return err
			}

			if outputJSON {
				return writeJSON(cmd, map[string]interface{}{"instances": instances, "total": total})
			}

			if len(instances) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tSESSION\tNAME\tIMAGE\tCREATED")
			for _, inst := range instances {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					stringField(inst, "id"),
					sandboxStatusValue(inst),
					stringField(inst, "session_id", "sessionId"),
					stringField(inst, "name"),
					stringField(inst, "image"),
					stringField(inst, "created_at", "createdAt"),
				)
			}
			_ = w.Flush()
			fmt.Fprintf(cmd.OutOrStdout(), "Total: %d\n", total)
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "filter status: pending|running|stopped|failed")
	cmd.Flags().IntVar(&limit, "limit", 50, "max results")
	cmd.Flags().IntVar(&offset, "offset", 0, "result offset")
	return cmd
}

func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <sandbox-id>",
		Short: "Get sandbox details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := newClient().GetSandboxInstance(args[0])
			if err != nil {
				return err
			}

			if outputJSON {
				return writeJSON(cmd, inst)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "ID: %s\n", stringField(inst, "id"))
			fmt.Fprintf(cmd.OutOrStdout(), "Session ID: %s\n", stringField(inst, "session_id", "sessionId"))
			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", sandboxStatusValue(inst))
			fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s\n", stringField(inst, "backend"))
			fmt.Fprintf(cmd.OutOrStdout(), "Image: %s\n", stringField(inst, "image"))
			fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", stringField(inst, "name"))
			fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", stringField(inst, "created_at", "createdAt"))
			fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", stringField(inst, "expires_at", "expiresAt"))
			return nil
		},
	}
	return cmd
}

func newOverviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "Get sandbox subsystem overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			overview, err := newClient().GetSandboxOverview()
			if err != nil {
				return err
			}
			if outputJSON {
				return writeJSON(cmd, overview)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s\n", stringField(overview, "backend"))
			fmt.Fprintf(cmd.OutOrStdout(), "Healthy: %v\n", boolField(overview, "healthy"))
			fmt.Fprintf(cmd.OutOrStdout(), "Running: %d / %d\n", int(numberField(overview, "running_instances", "runningInstances")), int(numberField(overview, "max_sandboxes", "maxSandboxes")))
			fmt.Fprintf(cmd.OutOrStdout(), "Total Instances: %d\n", int(numberField(overview, "total_instances", "totalInstances")))
			fmt.Fprintf(cmd.OutOrStdout(), "Total Executions: %d\n", int(numberField(overview, "total_executions", "totalExecutions")))
			return nil
		},
	}
	return cmd
}

func newExecCmd() *cobra.Command {
	var (
		workDir        string
		timeoutSeconds int
		envPairs       []string
	)

	cmd := &cobra.Command{
		Use:   "exec <sandbox-id> -- <command...>",
		Short: "Execute a command in a sandbox (phase 0 scaffold endpoint)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireScaffoldScope(); err != nil {
				return err
			}
			env, err := parseEnvPairs(envPairs)
			if err != nil {
				return err
			}

			payload := map[string]interface{}{
				"command": args[1:],
			}
			if workDir != "" {
				payload["work_dir"] = workDir
			}
			if timeoutSeconds > 0 {
				payload["timeout_seconds"] = timeoutSeconds
			}
			if len(env) > 0 {
				payload["env"] = env
			}

			resp, err := newClient().ExecSandboxCommand(args[0], payload)
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox exec", "POST /v1/sandbox/instances/{sandbox_id}/exec")
				}
				return err
			}

			if outputJSON {
				return writeJSON(cmd, resp)
			}

			stdout := stringField(resp, "stdout")
			stderr := stringField(resp, "stderr")
			if stdout != "" {
				fmt.Fprint(cmd.OutOrStdout(), stdout)
				if !strings.HasSuffix(stdout, "\n") {
					fmt.Fprintln(cmd.OutOrStdout())
				}
			}
			if stderr != "" {
				fmt.Fprint(cmd.ErrOrStderr(), stderr)
				if !strings.HasSuffix(stderr, "\n") {
					fmt.Fprintln(cmd.ErrOrStderr())
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exit_code=%d timed_out=%v duration_ms=%d\n",
				int(numberField(resp, "exit_code", "exitCode")),
				boolField(resp, "timed_out", "timedOut"),
				int(numberField(resp, "duration_ms", "durationMs")),
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&workDir, "work-dir", "", "working directory inside sandbox")
	cmd.Flags().IntVar(&timeoutSeconds, "timeout-seconds", 0, "command timeout in seconds")
	cmd.Flags().StringArrayVar(&envPairs, "env", nil, "environment variables (KEY=VALUE)")
	return cmd
}

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <sandbox-id>",
		Short: "Open an interactive shell (CLI transport scaffold)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("interactive shell over CLI is scaffolded only; use `mf sandbox exec` for now")
		},
	}
}

func newLogsCmd() *cobra.Command {
	var (
		sessionID string
		follow    bool
	)

	cmd := &cobra.Command{
		Use:   "logs <sandbox-id>",
		Short: "Stream sandbox logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resolvedSession := sessionID
			if resolvedSession == "" {
				var err error
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}

			ctx := cmd.Context()
			if !follow {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
			}

			return client.streamSSE(ctx, "/v1/sandbox/"+resolvedSession+"/logs/stream", nil, func(eventType, data string) error {
				if eventType == "error" {
					return fmt.Errorf("log stream error: %s", data)
				}
				line := extractSSETextField(data, "line")
				if line == "" {
					line = data
				}
				fmt.Fprintln(cmd.OutOrStdout(), line)
				return nil
			})
		},
	}

	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	cmd.Flags().BoolVar(&follow, "follow", true, "follow log stream")
	return cmd
}

func newStatsCmd() *cobra.Command {
	var (
		sessionID string
		watch     bool
	)

	cmd := &cobra.Command{
		Use:   "stats <sandbox-id>",
		Short: "Get sandbox stats",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resolvedSession := sessionID
			if resolvedSession == "" {
				var err error
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}

			if watch {
				return client.streamSSE(cmd.Context(), "/v1/sandbox/"+resolvedSession+"/stats/stream", nil, func(eventType, data string) error {
					if eventType == "error" {
						return fmt.Errorf("stats stream error: %s", data)
					}
					if outputJSON {
						fmt.Fprintln(cmd.OutOrStdout(), data)
						return nil
					}
					fmt.Fprintln(cmd.OutOrStdout(), summarizeStatsJSON(data))
					return nil
				})
			}

			stats, err := client.GetSandboxStats(resolvedSession)
			if err != nil {
				return err
			}
			if outputJSON {
				return writeJSON(cmd, stats)
			}
			fmt.Fprintln(cmd.OutOrStdout(), summarizeStatsMap(stats))
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	cmd.Flags().BoolVar(&watch, "watch", false, "watch stats stream")
	return cmd
}

func newEventsCmd() *cobra.Command {
	var (
		eventType string
		limit     int
		offset    int
		follow    bool
	)

	cmd := &cobra.Command{
		Use:   "events <sandbox-id>",
		Short: "List or stream sandbox lifecycle events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			sandboxID := args[0]

			if follow {
				return client.streamSSE(cmd.Context(), "/v1/sandbox/"+sandboxID+"/events/stream", nil, func(eventType, data string) error {
					if outputJSON {
						fmt.Fprintln(cmd.OutOrStdout(), data)
						return nil
					}
					fmt.Fprintln(cmd.OutOrStdout(), summarizeEventJSON(data))
					return nil
				})
			}

			events, total, err := client.ListSandboxEvents(sandboxID, eventType, limit, offset)
			if err != nil {
				return err
			}
			if outputJSON {
				return writeJSON(cmd, map[string]interface{}{"events": events, "total": total})
			}

			if len(events) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No events found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "TIME\tTYPE\tMESSAGE\tERROR")
			for _, e := range events {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					stringField(e, "created_at", "createdAt"),
					stringField(e, "event_type", "eventType"),
					stringField(e, "message"),
					stringField(e, "error"),
				)
			}
			_ = w.Flush()
			fmt.Fprintf(cmd.OutOrStdout(), "Total: %d\n", total)
			return nil
		},
	}

	cmd.Flags().StringVar(&eventType, "event-type", "", "event type filter")
	cmd.Flags().IntVar(&limit, "limit", 50, "max results")
	cmd.Flags().IntVar(&offset, "offset", 0, "result offset")
	cmd.Flags().BoolVar(&follow, "follow", false, "stream events")
	return cmd
}

func newDestroyCmd() *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:     "destroy <sandbox-id>",
		Aliases: []string{"delete"},
		Short:   "Destroy sandbox using current session-based API",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resolvedSession := sessionID
			if resolvedSession == "" {
				var err error
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}
			resp, err := client.DestroySandboxBySession(resolvedSession)
			if err != nil {
				return err
			}
			if outputJSON {
				return writeJSON(cmd, resp)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sandbox destroyed (session=%s)\n", resolvedSession)
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	return cmd
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <sandbox-id>",
		Short: "Stop a sandbox (phase 3 scaffold endpoint)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireScaffoldScope(); err != nil {
				return err
			}
			resp, err := newClient().StopSandbox(args[0])
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox stop", "POST /v1/sandbox/instances/{sandbox_id}/stop")
				}
				return err
			}
			return writeCommandResult(cmd, resp, "Stop requested")
		},
	}
}

func newReviveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revive <sandbox-id>",
		Short: "Revive a stopped sandbox (phase 3 scaffold endpoint)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireScaffoldScope(); err != nil {
				return err
			}
			resp, err := newClient().ReviveSandbox(args[0])
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox revive", "POST /v1/sandbox/instances/{sandbox_id}/revive")
				}
				return err
			}
			return writeCommandResult(cmd, resp, "Revive requested")
		},
	}
}

func newTerminateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "terminate <sandbox-id>",
		Short: "Terminate a sandbox permanently (phase 3 scaffold endpoint)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireScaffoldScope(); err != nil {
				return err
			}
			resp, err := newClient().TerminateSandbox(args[0])
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox terminate", "POST /v1/sandbox/instances/{sandbox_id}/terminate")
				}
				return err
			}
			return writeCommandResult(cmd, resp, "Terminate requested")
		},
	}
}

func newRecreateCmd() *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "recreate <sandbox-id>",
		Short: "Recreate a sandbox from stored config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().RecreateSandbox(args[0], sessionID)
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, resp, "Sandbox recreated")
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "new session ID override")
	return cmd
}

func newPortsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ports",
		Short: "Manage sandbox port exposure",
	}
	cmd.AddCommand(newPortsExposeCmd())
	cmd.AddCommand(newPortsUnexposeCmd())
	cmd.AddCommand(newPortsListCmd())
	cmd.AddCommand(newPortsDetectCmd())
	return cmd
}

func newPortsExposeCmd() *cobra.Command {
	var (
		sessionID string
		protocol  string
	)
	cmd := &cobra.Command{
		Use:   "expose <sandbox-id> <port>",
		Short: "Expose a sandbox port",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid port: %w", err)
			}

			client := newClient()
			resolvedSession := sessionID
			if resolvedSession == "" {
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}

			mapping, err := client.ExposePort(resolvedSession, port, protocol)
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, mapping, "Port exposed")
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	cmd.Flags().StringVar(&protocol, "protocol", "http", "protocol")
	return cmd
}

func newPortsUnexposeCmd() *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "unexpose <sandbox-id> <port>",
		Short: "Close an exposed sandbox port",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid port: %w", err)
			}

			client := newClient()
			resolvedSession := sessionID
			if resolvedSession == "" {
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}

			resp, err := client.UnexposePort(resolvedSession, port)
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, resp, "Port closed")
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	return cmd
}

func newPortsListCmd() *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "list <sandbox-id>",
		Short: "List exposed ports for a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resolvedSession := sessionID
			var err error
			if resolvedSession == "" {
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}
			ports, err := client.ListExposedPorts(resolvedSession)
			if err != nil {
				return err
			}

			if outputJSON {
				return writeJSON(cmd, map[string]interface{}{"ports": ports})
			}

			if len(ports) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No exposed ports.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PORT\tPROTOCOL\tURL\tSTATUS")
			for _, p := range ports {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n",
					int(numberField(p, "port")),
					stringField(p, "protocol"),
					stringField(p, "url"),
					stringField(p, "status"),
				)
			}
			_ = w.Flush()
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	return cmd
}

func newPortsDetectCmd() *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "detect <sandbox-id>",
		Short: "Detect listening ports inside sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resolvedSession := sessionID
			var err error
			if resolvedSession == "" {
				resolvedSession, err = client.ResolveSessionID(args[0])
				if err != nil {
					return err
				}
			}
			ports, err := client.DetectListeningPorts(resolvedSession)
			if err != nil {
				return err
			}
			if outputJSON {
				return writeJSON(cmd, map[string]interface{}{"ports": ports})
			}
			if len(ports) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No listening ports detected.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PORT\tPROTOCOL\tADDRESS\tPROCESS\tPID")
			for _, p := range ports {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\n",
					int(numberField(p, "port")),
					stringField(p, "protocol"),
					stringField(p, "address"),
					stringField(p, "process"),
					int(numberField(p, "pid")),
				)
			}
			_ = w.Flush()
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID override")
	return cmd
}

func newSSHInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh-info <sandbox-id-or-name>",
		Short: "Get SSH connection details for a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.ErrOrStderr(), "Fetching SSH info for %q...\n", args[0])
			resp, err := newClient().GetSandboxSSHInfo(args[0])
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox ssh-info", "GET /v1/sandbox/instances/{sandbox_id}/ssh-info")
				}
				return err
			}
			return writeCommandResult(cmd, resp, "SSH info")
		},
	}
	return cmd
}

func newSSHCmd() *cobra.Command {
	var (
		identityFile string
		dryRun       bool
		directSSH    bool
	)
	cmd := &cobra.Command{
		Use:   "ssh <sandbox-id-or-name>",
		Short: "Open interactive shell to a sandbox",
		Long: `Open an interactive shell to a sandbox.

By default, connects via WebSocket through the HTTP API port (firewall-friendly).
Use --direct-ssh to connect via the SSH proxy (requires direct TCP access to port 2223).

Authentication uses your SSH key to sign the connection request.
Keys are discovered automatically from: SSH agent, ~/.ssh/id_ed25519, ~/.ssh/id_ecdsa, ~/.ssh/id_rsa.
Use --identity-file to specify a key explicitly.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !directSSH {
				// Default: WebSocket shell through the HTTP API.
				// Resolve sandbox via ssh-info endpoint (bypasses API key middleware,
				// works on all server versions, returns session_id for the shell route).
				client := newClient()
				if client.initErr != nil {
					return client.initErr
				}
				info := &sandboxInfo{Name: args[0], Status: "connecting"}

				if sshInfo, err := client.GetSandboxSSHInfo(args[0]); err == nil {
					if sid := stringField(sshInfo, "session_id", "sessionId"); sid != "" {
						info.SessionID = sid
					}
					if name := stringField(sshInfo, "name"); name != "" {
						info.Name = name
					}
					if id := stringField(sshInfo, "sandbox_id", "sandboxId"); id != "" {
						info.ID = id
					}
					if img := stringField(sshInfo, "image"); img != "" {
						info.Image = img
					}
					if st := stringField(sshInfo, "status"); st != "" {
						info.Status = sandboxStatusValue(sshInfo)
					} else {
						info.Status = "running"
					}
				}

				// Sign the WebSocket connection with an SSH key.
				// Use sandbox ID if resolved, otherwise fall back to the CLI argument.
				signID := args[0]
				if info.ID != "" {
					signID = info.ID
				}
				auth, err := signShellAuth(signID, identityFile)
				if err != nil {
					return fmt.Errorf("shell authentication failed: %w\n\nEnsure your public key is registered:\n  mf sandbox ssh-keys add --name \"my-key\" --file ~/.ssh/<your-key>.pub\n\nOr specify the key explicitly:\n  mf sandbox ssh %s --identity-file ~/.ssh/<your-key>", err, args[0])
				}

				fmt.Fprintf(cmd.ErrOrStderr(), "  %sauth:%s  %s (%s)\n\n", ansiDim, ansiReset, auth.Fingerprint, auth.Algorithm)

				return runWebSocketShell(cmd.Context(), client.baseURL, args[0], info, auth)
			}

			// --direct-ssh: use SSH proxy (legacy path)
			fmt.Fprintf(cmd.ErrOrStderr(), "Resolving sandbox %q...\n", args[0])
			info, err := newClient().GetSandboxSSHInfo(args[0])
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox ssh", "GET /v1/sandbox/instances/{sandbox_id}/ssh-info")
				}
				return err
			}

			connectionString := strings.TrimSpace(stringField(info, "connection_string", "connectionString"))
			var sshArgs []string
			if connectionString != "" {
				parsedArgs, parseErr := parseSSHConnectionString(connectionString)
				if parseErr != nil {
					return fmt.Errorf("invalid ssh connection string from server: %w", parseErr)
				}
				sshArgs = parsedArgs
			} else {
				host := stringField(info, "host", "hostname")
				user := stringField(info, "username", "user")
				if user == "" {
					user = "sandbox"
				}
				port := int(numberField(info, "port"))
				if host == "" || port == 0 {
					return fmt.Errorf("ssh info response missing host/port")
				}

				sshArgs = []string{"-p", strconv.Itoa(port)}
				if isLoopbackSSHHost(host) {
					// Local ephemeral sandboxes rotate host keys; bypass persistence to avoid false MITM failures.
					sshArgs = append(sshArgs,
						"-o", "StrictHostKeyChecking=no",
						"-o", "UserKnownHostsFile=/dev/null",
						"-o", "GlobalKnownHostsFile=/dev/null",
					)
				} else {
					sshArgs = append(sshArgs, "-o", "StrictHostKeyChecking=accept-new")
				}
				sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", user, host))
			}
			if identityFile != "" {
				sshArgs = append([]string{"-i", identityFile}, sshArgs...)
			}

			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "ssh %s\n", strings.Join(sshArgs, " "))
				return nil
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "Connecting...\n")
			proc := exec.Command("ssh", sshArgs...)
			proc.Stdin = os.Stdin
			proc.Stdout = cmd.OutOrStdout()
			proc.Stderr = cmd.ErrOrStderr()
			return proc.Run()
		},
	}
	cmd.Flags().StringVar(&identityFile, "identity-file", "", "SSH private key path for authentication")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print ssh command without executing (only with --direct-ssh)")
	cmd.Flags().BoolVar(&directSSH, "direct-ssh", false, "use direct SSH proxy instead of WebSocket shell")
	return cmd
}

func isLoopbackSSHHost(host string) bool {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

func parseSSHConnectionString(connectionString string) ([]string, error) {
	fields := strings.Fields(strings.TrimSpace(connectionString))
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty connection string")
	}
	if fields[0] != "ssh" {
		return nil, fmt.Errorf("expected command to start with 'ssh'")
	}
	if len(fields) == 1 {
		return nil, fmt.Errorf("missing ssh arguments")
	}
	return append([]string(nil), fields[1:]...), nil
}

func newSSHKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh-keys",
		Short: "Manage SSH keys for sandbox access (phase 4 scaffold endpoint)",
	}
	cmd.AddCommand(newSSHKeysAddCmd())
	cmd.AddCommand(newSSHKeysListCmd())
	cmd.AddCommand(newSSHKeysDeleteCmd())
	return cmd
}

func newSSHKeysAddCmd() *cobra.Command {
	var (
		name      string
		publicKey string
		keyFile   string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an SSH public key",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if publicKey == "" && keyFile == "" {
				return fmt.Errorf("provide --public-key or --file")
			}
			if publicKey != "" && keyFile != "" {
				return fmt.Errorf("use either --public-key or --file, not both")
			}
			if keyFile != "" {
				data, err := os.ReadFile(keyFile)
				if err != nil {
					return err
				}
				publicKey = strings.TrimSpace(string(data))
			}

			resp, err := newClient().AddSSHKey(name, publicKey)
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox ssh-keys add", "POST /v1/settings/ssh-keys")
				}
				return err
			}
			return writeCommandResult(cmd, resp, "SSH key added")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "key name")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "SSH public key")
	cmd.Flags().StringVar(&keyFile, "file", "", "file containing SSH public key")
	return cmd
}

func newSSHKeysListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SSH keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := newClient().ListSSHKeys()
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox ssh-keys list", "GET /v1/settings/ssh-keys")
				}
				return err
			}
			if outputJSON {
				return writeJSON(cmd, map[string]interface{}{"keys": keys})
			}
			if len(keys) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No SSH keys found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tFINGERPRINT\tCREATED")
			for _, key := range keys {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					stringField(key, "id"),
					stringField(key, "name"),
					stringField(key, "fingerprint"),
					stringField(key, "created_at", "createdAt"),
				)
			}
			_ = w.Flush()
			return nil
		},
	}
	return cmd
}

func newSSHKeysDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <key-id>",
		Short: "Delete an SSH key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().DeleteSSHKey(args[0])
			if err != nil {
				if isEndpointMissing(err) {
					return missingEndpointError("mf sandbox ssh-keys delete", "DELETE /v1/settings/ssh-keys/{key_id}")
				}
				return err
			}
			return writeCommandResult(cmd, resp, "SSH key deleted")
		},
	}
	return cmd
}

func writeCommandResult(cmd *cobra.Command, v interface{}, fallback string) error {
	if outputJSON {
		return writeJSON(cmd, v)
	}
	if m, ok := v.(map[string]interface{}); ok {
		if msg := stringField(m, "message"); msg != "" {
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), fallback)
	return nil
}

func parseEnvPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid env pair %q, expected KEY=VALUE", pair)
		}
		out[k] = v
	}
	return out, nil
}

func extractSSETextField(raw, field string) string {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	return stringField(payload, field)
}

func summarizeStatsJSON(raw string) string {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	return summarizeStatsMap(payload)
}

func summarizeStatsMap(stats map[string]interface{}) string {
	cpu := numberField(stats, "cpu_percent", "cpuPercent")
	memUsage := int64(numberField(stats, "memory_usage", "memoryUsage"))
	memLimit := int64(numberField(stats, "memory_limit", "memoryLimit"))
	pids := int(numberField(stats, "pids"))
	ts := stringField(stats, "timestamp")
	if ts == "" {
		ts = time.Now().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s cpu=%.2f%% mem=%d/%d bytes pids=%d", ts, cpu, memUsage, memLimit, pids)
}

func summarizeEventJSON(raw string) string {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	return fmt.Sprintf("%s %s %s",
		stringField(payload, "created_at", "createdAt"),
		stringField(payload, "event_type", "eventType"),
		stringField(payload, "message"),
	)
}

func sandboxStatusValue(inst map[string]interface{}) string {
	if s := stringField(inst, "status"); s != "" {
		if strings.HasPrefix(s, "SANDBOX_STATUS_") {
			return strings.ToLower(strings.TrimPrefix(s, "SANDBOX_STATUS_"))
		}
		return s
	}
	statusNum := int(numberField(inst, "status"))
	switch statusNum {
	case 1:
		return "pending"
	case 2:
		return "running"
	case 3:
		return "stopped"
	case 4:
		return "failed"
	default:
		return "unspecified"
	}
}

package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	cliruntime "github.com/everstacklabs/everstack/internal/cli/runtime"
	"github.com/spf13/cobra"
)

const openAIPath = "/v1/openai-agents"

type openAIHTTP struct {
	base   string
	client *http.Client
}

func newOpenAIHTTP(options connectionOptions) (*openAIHTTP, error) {
	f, err := requireFactory(options)
	if err != nil {
		return nil, err
	}
	resolved, err := cliruntime.Resolve(cliruntime.Overrides{APIURL: options.apiURL, APIKey: options.apiKey, TenantID: options.tenantID})
	if err != nil {
		return nil, err
	}
	hc := f.HTTPClient()
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &openAIHTTP{base: strings.TrimRight(resolved.APIURL, "/"), client: hc}, nil
}
func (c *openAIHTTP) request(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("credential-bearing redirect rejected")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}
func (c *openAIHTTP) download(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 201<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("credential-bearing redirect rejected")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}
func openAIID(v string) (string, error) {
	if v == "" || v == "." || v == ".." {
		return "", fmt.Errorf("resource ID is required")
	}
	return url.PathEscape(v), nil
}
func printJSON(w io.Writer, b []byte) error {
	if len(bytes.TrimSpace(b)) == 0 {
		_, err := fmt.Fprintln(w, "null")
		return err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func readJSONInput(cmd *cobra.Command, file string) (json.RawMessage, error) {
	var r io.Reader = cmd.InOrStdin()
	if file != "" && file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, 2<<20))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, fmt.Errorf("JSON input is required via --file or stdin")
	}
	var raw json.RawMessage
	if err = json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON input: %w", err)
	}
	return raw, nil
}

func newOpenAICmd(options *connectionOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "openai", Short: "Manage OpenAI Agents API connections and sessions"}
	cmd.AddCommand(newOpenAIConnectionsCmd(options), newOpenAISessionsCmd(options), newOpenAIApprovalsCmd(options))
	return cmd
}
func withClient(options *connectionOptions, fn func(*cobra.Command, *openAIHTTP) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		c, err := newOpenAIHTTP(*options)
		if err != nil {
			return err
		}
		return fn(cmd, c)
	}
}
func resourceCommand(options *connectionOptions, use, short, method, path string, args cobra.PositionalArgs) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, a []string) error {
		p := path
		if len(a) > 0 {
			id, e := openAIID(a[0])
			if e != nil {
				return e
			}
			p += id
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), method, p, nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
}

func newOpenAIConnectionsCmd(options *connectionOptions) *cobra.Command {
	group := &cobra.Command{Use: "connections", Short: "Manage instance-scoped OpenAI application credentials"}
	group.AddCommand(resourceCommand(options, "list", "List connections", http.MethodGet, openAIPath+"/connections", cobra.NoArgs))
	group.AddCommand(resourceCommand(options, "get <id>", "Get safe connection metadata", http.MethodGet, openAIPath+"/connections/", cobra.ExactArgs(1)))
	group.AddCommand(resourceCommand(options, "delete <id>", "Delete a connection", http.MethodDelete, openAIPath+"/connections/", cobra.ExactArgs(1)))
	group.AddCommand(newOpenAIJSONMutation(options, "create", "Create a connection from JSON", http.MethodPost, openAIPath+"/connections", false))
	group.AddCommand(newOpenAIJSONMutation(options, "update <id>", "Update a connection from JSON", http.MethodPatch, openAIPath+"/connections/", true))
	return group
}

func newOpenAIJSONMutation(options *connectionOptions, use, short, method, path string, withID bool) *cobra.Command {
	var file string
	cmd := &cobra.Command{Use: use, Short: short, Long: short + ". Read the full JSON request from --file or stdin. Credential values never appear in command arguments.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if withID {
			if len(args) != 1 {
				return fmt.Errorf("exactly one resource ID is required")
			}
			id, e := openAIID(args[0])
			if e != nil {
				return e
			}
			path += id
		}
		body, e := readJSONInput(cmd, file)
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), method, path, body)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
	if withID {
		cmd.Args = cobra.ExactArgs(1)
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON request file (use - or omit for stdin)")
	return cmd
}

func newOpenAISessionsCmd(options *connectionOptions) *cobra.Command {
	g := &cobra.Command{Use: "sessions", Short: "Create, continue, inspect, and clean up OpenAI agent sessions"}
	g.AddCommand(resourceCommand(options, "list", "List sessions", http.MethodGet, openAIPath+"/sessions", cobra.NoArgs))
	g.AddCommand(resourceCommand(options, "get <id>", "Get session state", http.MethodGet, openAIPath+"/sessions/", cobra.ExactArgs(1)))
	g.AddCommand(resourceCommand(options, "cancel <id>", "Cancel the active turn", http.MethodPost, openAIPath+"/sessions/", cobra.ExactArgs(1)))
	g.Commands()[len(g.Commands())-1].RunE = func(cmd *cobra.Command, args []string) error {
		id, e := openAIID(args[0])
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodPost, openAIPath+"/sessions/"+id+"/cancel", nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}
	g.AddCommand(resourceCommand(options, "delete <id>", "Delete a session", http.MethodDelete, openAIPath+"/sessions/", cobra.ExactArgs(1)))
	g.AddCommand(newOpenAIJSONMutation(options, "create", "Create a session from JSON", http.MethodPost, openAIPath+"/sessions", false))
	g.AddCommand(newOpenAISessionInputCmd(options), newOpenAIEventsCmd(options), newOpenAIArtifactsCmd(options), newOpenAIUsageCmd(options))
	return g
}
func newOpenAISessionInputCmd(options *connectionOptions) *cobra.Command {
	var file string
	cmd := &cobra.Command{Use: "input <session-id>", Short: "Send input using a durable idempotency key", Long: "Send session input from a full JSON request on --file or stdin. The request must include idempotency_key and input.", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, e := openAIID(args[0])
		if e != nil {
			return e
		}
		body, e := readJSONInput(cmd, file)
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodPost, openAIPath+"/sessions/"+id+"/input", body)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON request file (use - or omit for stdin)")
	return cmd
}
func newOpenAIEventsCmd(options *connectionOptions) *cobra.Command {
	var after int64
	var limit int
	cmd := &cobra.Command{Use: "events <session-id>", Short: "Poll durable Everstack events after a local cursor", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, e := openAIID(args[0])
		if e != nil {
			return e
		}
		q := url.Values{}
		if after > 0 {
			q.Set("after", strconv.FormatInt(after, 10))
		}
		if limit > 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodGet, openAIPath+"/sessions/"+id+"/events?"+q.Encode(), nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
	cmd.Flags().Int64Var(&after, "after", 0, "Everstack durable cursor to continue after")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum events to return")
	return cmd
}
func newOpenAIArtifactsCmd(options *connectionOptions) *cobra.Command {
	g := &cobra.Command{Use: "artifacts", Short: "Upload, export, list, download, and delete session artifacts"}
	g.AddCommand(&cobra.Command{Use: "list <session-id>", Short: "List session artifacts", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, e := openAIID(args[0])
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodGet, openAIPath+"/sessions/"+id+"/artifacts", nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}})
	g.AddCommand(newOpenAIArtifactDownloadCmd(options), newOpenAIArtifactJSONCmd(options, "upload <session-id>", "Upload base64 artifact JSON", "/inputs"), newOpenAIArtifactJSONCmd(options, "export <session-id>", "Export a sandbox path", "/artifacts/export"), newOpenAIArtifactDeleteCmd(options))
	return g
}
func newOpenAIArtifactDownloadCmd(options *connectionOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "download <session-id> <artifact-id>", Short: "Get a temporary artifact download URL", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		sid, e := openAIID(args[0])
		if e != nil {
			return e
		}
		aid, e := openAIID(args[1])
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodGet, openAIPath+"/sessions/"+sid+"/artifacts/"+aid+"/download", nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
	return cmd
}

func newOpenAIArtifactJSONCmd(options *connectionOptions, use, short, suffix string) *cobra.Command {
	var file string
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		sid, e := openAIID(args[0])
		if e != nil {
			return e
		}
		body, e := readJSONInput(cmd, file)
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodPost, openAIPath+"/sessions/"+sid+suffix, body)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON request file (use - or omit for stdin)")
	return cmd
}
func newOpenAIArtifactDeleteCmd(options *connectionOptions) *cobra.Command {
	return &cobra.Command{Use: "delete <session-id> <object-id>", Short: "Delete a retained artifact", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		sid, e := openAIID(args[0])
		if e != nil {
			return e
		}
		aid, e := openAIID(args[1])
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodDelete, openAIPath+"/sessions/"+sid+"/artifacts/"+aid, nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
}
func newOpenAIUsageCmd(options *connectionOptions) *cobra.Command {
	return &cobra.Command{Use: "usage <session-id>", Short: "Get provider-reported session token usage", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		sid, e := openAIID(args[0])
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodGet, openAIPath+"/sessions/"+sid+"/usage", nil)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
}
func newOpenAIApprovalsCmd(options *connectionOptions) *cobra.Command {
	g := &cobra.Command{Use: "approvals", Short: "Review sensitive OpenAI agent tool calls"}
	g.AddCommand(resourceCommand(options, "list", "List pending tool approvals", http.MethodGet, openAIPath+"/approvals", cobra.NoArgs))
	var file string
	r := &cobra.Command{Use: "resolve <approval-id>", Short: "Approve or deny a tool call from JSON", Long: "Resolve an approval using JSON such as {\"decision\":\"approved\"}, read from --file or stdin.", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, e := openAIID(args[0])
		if e != nil {
			return e
		}
		body, e := readJSONInput(cmd, file)
		if e != nil {
			return e
		}
		c, e := newOpenAIHTTP(*options)
		if e != nil {
			return e
		}
		b, e := c.request(cmd.Context(), http.MethodPost, openAIPath+"/approvals/"+id+"/resolve", body)
		if e != nil {
			return e
		}
		return printJSON(cmd.OutOrStdout(), b)
	}}
	r.Flags().StringVarP(&file, "file", "f", "", "JSON request file (use - or omit for stdin)")
	g.AddCommand(r)
	return g
}

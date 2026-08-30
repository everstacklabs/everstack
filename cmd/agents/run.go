package agents

import (
	"bufio"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cli/client"
	agentsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/spf13/cobra"
)

func newRunCmd(options *connectionOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "run <name> [input]",
		Short: "Run a turn against a deployed agent and stream the reply",
		Long: `Run a deployed agent. With input as an argument, runs one turn and
exits; without it, reads one prompt from stdin. Streams text as it is
generated and prints tool calls as they happen.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			input := ""
			if len(args) == 2 {
				input = args[1]
			} else {
				fmt.Fprint(cmd.ErrOrStderr(), "> ")
				reader := bufio.NewReader(cmd.InOrStdin())
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				input = strings.TrimSpace(line)
			}
			if input == "" {
				return fmt.Errorf("input is required")
			}

			f, err := requireFactory(*options)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			agent, err := findAgentByName(ctx, f, name)
			if err != nil {
				return err
			}
			if agent == nil {
				return fmt.Errorf("no agent named %q; deploy one with `evs deploy`", name)
			}

			sessResp, err := f.Agents().CreateSession(ctx, connect.NewRequest(&agentsv1.CreateSessionRequest{
				AgentId: agent.GetId(),
			}))
			if err != nil {
				return client.MapError(err)
			}
			sessionID := sessResp.Msg.GetSession().GetId()

			stream, err := f.AgentsStreaming().RunTurnStream(ctx, connect.NewRequest(&agentsv1.RunTurnStreamRequest{
				SessionId:       sessionID,
				UserInput:       input,
				EnableStreaming: true,
			}))
			if err != nil {
				return client.MapError(err)
			}
			defer stream.Close()

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()
			for stream.Receive() {
				ev := stream.Msg()
				switch {
				case ev.GetTextDelta() != "":
					fmt.Fprint(out, ev.GetTextDelta())
				case ev.GetToolName() != "" && ev.GetToolResult() == "":
					fmt.Fprintf(errOut, "\n[tool] %s\n", ev.GetToolName())
				case ev.GetError() != "":
					return fmt.Errorf("agent error: %s", ev.GetError())
				}
			}
			fmt.Fprintln(out)
			if err := stream.Err(); err != nil {
				return client.MapError(err)
			}
			return nil
		},
	}
}

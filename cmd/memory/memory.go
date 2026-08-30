package memory

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	cliclient "github.com/everstacklabs/everstack/internal/cli/client"
	cliruntime "github.com/everstacklabs/everstack/internal/cli/runtime"
	"github.com/spf13/cobra"
)

var (
	apiURL   string
	apiKey   string
	tenantID string
)

func newClient() *Client {
	resolved, err := cliruntime.Resolve(cliruntime.Overrides{
		APIURL:   apiURL,
		APIKey:   apiKey,
		TenantID: tenantID,
	})
	client := NewClient(resolved.APIURL, resolved.APIKey)
	client.initErr = err
	client.tenantID = resolved.TenantID
	if err == nil {
		factory := cliclient.New(cliclient.Options{
			APIURL:            resolved.APIURL,
			AccessToken:       resolved.AccessToken,
			AccessTokenSource: resolved.AccessTokenSource,
			APIKey:            resolved.APIKey,
			OrgID:             resolved.OrgID,
			TenantID:          resolved.TenantID,
		})
		client.httpClient = factory.HTTPClient()
	}
	return client
}

// New creates the `memory` subcommand for managing memory stores.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memory stores, collections, and documents",
		Long:  "CLI for managing vector memory collections, adding documents, querying, and viewing analytics.",
	}

	cmd.PersistentFlags().StringVar(&apiURL, "api-url", "", "API server base URL (env: EVS_API_URL; default: active context)")
	cmd.PersistentFlags().StringVar(&apiKey, "api-key", "", "API key override (env: EVS_API_KEY; default: active login)")
	cmd.PersistentFlags().StringVar(&tenantID, "tenant-id", "", "tenant ID override (env: EVS_TENANT_ID)")

	cmd.AddCommand(newCollectionsCmd())
	cmd.AddCommand(newQueryCmd())
	cmd.AddCommand(newAddCmd())
	cmd.AddCommand(newAnalyticsCmd())
	cmd.AddCommand(newSetupCmd())

	return cmd
}

// ── collections ─────────────────────────────────────────────────────

func newCollectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collections",
		Short: "Manage memory collections",
	}

	cmd.AddCommand(newCollectionsListCmd())
	cmd.AddCommand(newCollectionsGetCmd())
	cmd.AddCommand(newCollectionsCreateCmd())
	cmd.AddCommand(newCollectionsDeleteCmd())

	return cmd
}

func newCollectionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all collections",
		RunE: func(cmd *cobra.Command, args []string) error {
			colls, err := newClient().ListCollections(tenantID)
			if err != nil {
				return err
			}

			if len(colls) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No collections found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tMODEL\tDIMENSION\tMETRIC\tDOCS\tCREATED")
			for _, c := range colls {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d\t%s\n",
					c.Name, c.EmbeddingModel, c.EmbeddingDimension,
					c.DistanceMetric, c.DocumentCount, c.CreatedAt)
			}
			return w.Flush()
		},
	}
}

func newCollectionsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Get collection details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient().GetCollection(tenantID, args[0])
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "ID:          %s\n", c.ID)
			fmt.Fprintf(w, "Name:        %s\n", c.Name)
			fmt.Fprintf(w, "Tenant:      %s\n", c.TenantID)
			fmt.Fprintf(w, "Description: %s\n", c.Description)
			fmt.Fprintf(w, "Model:       %s\n", c.EmbeddingModel)
			fmt.Fprintf(w, "Dimension:   %d\n", c.EmbeddingDimension)
			fmt.Fprintf(w, "Metric:      %s\n", c.DistanceMetric)
			fmt.Fprintf(w, "Documents:   %d\n", c.DocumentCount)
			if len(c.Metadata) > 0 {
				pairs := make([]string, 0, len(c.Metadata))
				for k, v := range c.Metadata {
					pairs = append(pairs, k+"="+v)
				}
				fmt.Fprintf(w, "Metadata:    %s\n", strings.Join(pairs, ", "))
			}
			fmt.Fprintf(w, "Created:     %s\n", c.CreatedAt)
			fmt.Fprintf(w, "Updated:     %s\n", c.UpdatedAt)
			return nil
		},
	}
}

func newCollectionsCreateCmd() *cobra.Command {
	var (
		model       string
		dimension   int
		metric      string
		description string
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			coll, err := newClient().CreateCollection(tenantID, args[0], CreateCollectionOpts{
				Description:        description,
				EmbeddingModel:     model,
				EmbeddingDimension: dimension,
				DistanceMetric:     metric,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created collection %q (model=%s, dimension=%d, metric=%s)\n",
				coll.Name, coll.EmbeddingModel, coll.EmbeddingDimension, coll.DistanceMetric)
			return nil
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "embedding model name")
	cmd.Flags().IntVar(&dimension, "dimension", 0, "embedding dimension")
	cmd.Flags().StringVar(&metric, "metric", "", "distance metric: cosine, euclidean, dot_product")
	cmd.Flags().StringVar(&description, "description", "", "collection description")

	return cmd
}

func newCollectionsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().DeleteCollection(tenantID, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted collection %q\n", args[0])
			return nil
		},
	}
}

// ── query ───────────────────────────────────────────────────────────

func newQueryCmd() *cobra.Command {
	var (
		topK     int
		minScore float32
	)

	cmd := &cobra.Command{
		Use:   "query <collection> <query>",
		Short: "Query a collection",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := newClient().QueryCollection(tenantID, args[0], args[1], topK, minScore)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No results found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SCORE\tCHUNK_TEXT\tDOC_ID")
			for _, r := range results {
				text := r.ChunkText
				if len(text) > 80 {
					text = text[:77] + "..."
				}
				fmt.Fprintf(w, "%.4f\t%s\t%s\n", r.Score, text, r.DocumentID)
			}
			return w.Flush()
		},
	}

	cmd.Flags().IntVar(&topK, "top-k", 5, "number of results to return")
	cmd.Flags().Float32Var(&minScore, "min-score", 0, "minimum similarity score")

	return cmd
}

// ── add ─────────────────────────────────────────────────────────────

func newAddCmd() *cobra.Command {
	var (
		content   string
		source    string
		chunkSize int
	)

	cmd := &cobra.Command{
		Use:   "add <collection>",
		Short: "Add a document to a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := content
			if text == "-" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				text = string(data)
			}

			if text == "" {
				return fmt.Errorf("--content is required (use \"-\" to read from stdin)")
			}

			doc := DocumentInput{
				Content: text,
				Source:  source,
			}
			docIDs, chunks, err := newClient().AddDocuments(tenantID, args[0], []DocumentInput{doc}, chunkSize)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Added %d document(s), %d chunk(s) created\n", len(docIDs), chunks)
			for _, id := range docIDs {
				fmt.Fprintf(cmd.OutOrStdout(), "  document_id: %s\n", id)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&content, "content", "", "document content (use \"-\" for stdin)")
	cmd.Flags().StringVar(&source, "source", "", "document source identifier")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 0, "chunk size in tokens (server default: 512)")
	_ = cmd.MarkFlagRequired("content")

	return cmd
}

// ── analytics ───────────────────────────────────────────────────────

func newAnalyticsCmd() *cobra.Command {
	var (
		from string
		to   string
	)

	cmd := &cobra.Command{
		Use:   "analytics",
		Short: "View memory analytics",
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := newClient().GetAnalytics(tenantID, from, to)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()

			// Summary
			fmt.Fprintf(w, "Total Requests: %d\n", summary.TotalRequests)
			fmt.Fprintf(w, "Total Errors:   %d\n", summary.TotalErrors)
			fmt.Fprintf(w, "Avg Latency:    %.2f ms\n\n", summary.AvgLatencyMs)

			// Buckets
			if len(summary.Buckets) > 0 {
				tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "TIMESTAMP\tQUERIES\tSTORES\tDELETES\tERRORS\tAVG LATENCY")
				for _, b := range summary.Buckets {
					fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%.2f ms\n",
						b.Timestamp, b.QueryCount, b.StoreCount,
						b.DeleteCount, b.ErrorCount, b.AvgLatencyMs)
				}
				tw.Flush()
			}

			// Top collections
			if len(summary.TopCollections) > 0 {
				fmt.Fprintf(w, "\nTop Collections:\n")
				tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "COLLECTION\tREQUESTS")
				for _, c := range summary.TopCollections {
					fmt.Fprintf(tw, "%s\t%d\n", c.CollectionName, c.RequestCount)
				}
				tw.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "start time (RFC3339)")
	cmd.Flags().StringVar(&to, "to", "", "end time (RFC3339)")

	return cmd
}

// ── setup ───────────────────────────────────────────────────────────

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Setup pgvector extension and embedding column",
		RunE: func(cmd *cobra.Command, args []string) error {
			msg, err := newClient().SetupPgVector()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
}

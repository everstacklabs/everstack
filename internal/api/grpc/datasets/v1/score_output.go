package v1

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	eval_runner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
)

// ScoreOutput scores a single output synchronously against the requested
// scorer configs and returns the scores immediately. It is the interactive
// counterpart to the background eval-run scorer loop — the playground calls it
// to fill in per-cell scores live.
//
// Tenant is resolved from the authenticated context (never the request body),
// and score configs are loaded scoped to that tenant, so a caller can never
// score against another tenant's configs. Unlike the eval runner's internal
// gateway calls, this user-facing RPC does not bypass auth.
func (s *EvalServer) ScoreOutput(
	ctx context.Context,
	req *connect.Request[datasetspb.ScoreOutputRequest],
) (*connect.Response[datasetspb.ScoreOutputResponse], error) {
	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("tenant not resolved"))
	}

	if s.samplingRunner == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("eval runner not configured — start_api must call SetSamplingRunner"))
	}

	configIDs := req.Msg.GetScorerConfigIds()
	if len(configIDs) == 0 {
		// No scorers requested: return an empty scores struct rather than error.
		empty, _ := structpb.NewStruct(map[string]interface{}{})
		return connect.NewResponse(&datasetspb.ScoreOutputResponse{Scores: empty}), nil
	}

	// NB: do NOT switch the search_path to a per-tenant schema here. This DB
	// pool enforces tenancy via `WHERE tenant_id` (see internal/database/
	// shared_pool.go), and loadScoreConfigs already filters by tenant_id.
	// Setting a per-tenant schema instead points the runner's query at a
	// non-existent/empty schema and silently returns zero configs.

	in := eval_runner.ScoreInput{
		Input:            valueToInterface(req.Msg.GetInput()),
		Output:           valueToInterface(req.Msg.GetOutput()),
		ExpectedOutput:   valueToInterface(req.Msg.GetExpectedOutput()),
		Metadata:         structToInterface(req.Msg.GetMetadata()),
		RetrievedContext: "", // interactive scoring does not run RAG retrieval
	}

	// Synthetic namespace scopes any sandbox execution for this one-off score.
	namespace := "playground-" + uuid.NewString()

	scores, err := s.samplingRunner.ScoreOutputByConfigIDs(ctx, tenantID, namespace, in, configIDs)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scoring failed: %w", err))
	}

	scoresStruct, err := structpb.NewStruct(sanitizeScores(scores))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to encode scores: %w", err))
	}

	return connect.NewResponse(&datasetspb.ScoreOutputResponse{Scores: scoresStruct}), nil
}

// valueToInterface converts a protobuf Value to a plain Go value, tolerating nil.
func valueToInterface(v *structpb.Value) interface{} {
	if v == nil {
		return nil
	}
	return v.AsInterface()
}

// structToInterface converts a protobuf Struct to a map, tolerating nil.
func structToInterface(s *structpb.Struct) interface{} {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// sanitizeScores coerces scorer values into forms structpb.NewStruct accepts.
// The dispatch returns float64/bool/string values plus string reasons/errors,
// all of which are already valid; any unexpected type is stringified so the
// response never fails to encode.
func sanitizeScores(scores map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(scores))
	for k, v := range scores {
		switch v.(type) {
		case nil, bool, string, float64, float32, int, int32, int64:
			out[k] = v
		default:
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

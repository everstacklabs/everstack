package executors

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// RouterExecutor handles model/provider routing based on config mappings.
type RouterExecutor struct{}

func (e *RouterExecutor) NodeType() string { return "router" }

type routerMapping struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

func (e *RouterExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	// Parse mappings from config
	mappingsRaw, ok := node.Config["mappings"]
	if !ok {
		// No mappings configured, pass through
		return engine.NodeResult{NextHandle: "out"}
	}

	// Mappings can be stored as JSON array
	var mappings []routerMapping
	switch v := mappingsRaw.(type) {
	case []interface{}:
		data, err := json.Marshal(v)
		if err == nil {
			json.Unmarshal(data, &mappings)
		}
	case string:
		json.Unmarshal([]byte(v), &mappings)
	}

	if len(mappings) == 0 {
		return engine.NodeResult{NextHandle: "out"}
	}

	// Use the first mapping as default, or match by requested model
	requestedModel := ec.ResolvedModel
	for _, m := range mappings {
		if requestedModel == "" || m.Model == requestedModel {
			ec.ResolvedModel = m.Model
			ec.ResolvedProvider = m.Provider
			logger.WithFields("model", m.Model, "provider", m.Provider).Debug("router: resolved model/provider")
			break
		}
	}

	// If nothing matched, use first mapping
	if ec.ResolvedModel == "" && len(mappings) > 0 {
		ec.ResolvedModel = mappings[0].Model
		ec.ResolvedProvider = mappings[0].Provider
	}

	ec.SetNodeData("requested_model", requestedModel)
	ec.SetNodeData("resolved_model", ec.ResolvedModel)
	ec.SetNodeData("resolved_provider", ec.ResolvedProvider)
	ec.SetNodeData("mapping_count", fmt.Sprintf("%d", len(mappings)))

	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"model":    ec.ResolvedModel,
		"provider": ec.ResolvedProvider,
	}}
}

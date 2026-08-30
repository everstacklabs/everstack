package engine

import "context"

// NodeResult is returned by a node executor after execution.
type NodeResult struct {
	NextHandle string                 // "out", "hit", "miss", "pass", "block", "true", "false"
	Error      error
	Output     map[string]interface{} // Typed output data recorded in the execution ledger
}

// NodeExecutor defines the interface every node type must implement.
type NodeExecutor interface {
	Execute(ctx context.Context, node *GraphNode, ec *ExecutionContext) NodeResult
	NodeType() string
}

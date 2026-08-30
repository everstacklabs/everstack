package engine

import (
	"encoding/json"
	"fmt"
)

// GraphNode represents an executable node in the workflow DAG.
type GraphNode struct {
	ID       string
	Type     string
	Label    string
	Config   map[string]interface{}
	OutEdges []*GraphEdge // Edges going out from this node
	InEdges  []*GraphEdge // Edges coming into this node
}

// GraphEdge represents a connection between two nodes.
type GraphEdge struct {
	ID           string
	Source       string
	Target       string
	SourceHandle string
	TargetHandle string
}

// Graph represents the complete executable workflow DAG.
type Graph struct {
	Nodes   map[string]*GraphNode
	Edges   []*GraphEdge
	StartID string // ID of the start node
}

// GetConfigString retrieves a string config value from a node.
func (n *GraphNode) GetConfigString(key string) string {
	if n.Config == nil {
		return ""
	}
	v, ok := n.Config[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// GetConfigBool retrieves a boolean config value from a node.
func (n *GraphNode) GetConfigBool(key string) bool {
	if n.Config == nil {
		return false
	}
	v, ok := n.Config[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		return false
	}
	return b
}

// GetConfigFloat retrieves a float64 config value from a node.
func (n *GraphNode) GetConfigFloat(key string) float64 {
	if n.Config == nil {
		return 0
	}
	v, ok := n.Config[key]
	if !ok {
		return 0
	}
	switch f := v.(type) {
	case float64:
		return f
	case float32:
		return float64(f)
	case int:
		return float64(f)
	case int64:
		return float64(f)
	}
	return 0
}

// GetConfigInt retrieves an int config value from a node.
func (n *GraphNode) GetConfigInt(key string) int {
	return int(n.GetConfigFloat(key))
}

// GetConfigStringSlice retrieves a string slice config value from a node.
func (n *GraphNode) GetConfigStringSlice(key string) []string {
	if n.Config == nil {
		return nil
	}
	v, ok := n.Config[key]
	if !ok {
		return nil
	}
	// JSON unmarshalling produces []interface{}
	if arr, ok := v.([]interface{}); ok {
		result := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// rawNode is used for JSON unmarshalling of workflow nodes.
type rawNode struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Label    string                 `json:"label"`
	Config   map[string]interface{} `json:"config"`
	Position map[string]interface{} `json:"position"` // Ignored for execution
}

// rawEdge is used for JSON unmarshalling of workflow edges.
type rawEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"source_handle"`
	TargetHandle string `json:"target_handle"`
}

// BuildGraph parses JSONB node and edge data into an executable DAG.
func BuildGraph(nodesJSON, edgesJSON []byte) (*Graph, error) {
	var rawNodes []rawNode
	if err := json.Unmarshal(nodesJSON, &rawNodes); err != nil {
		return nil, fmt.Errorf("failed to parse nodes: %w", err)
	}

	var rawEdges []rawEdge
	if err := json.Unmarshal(edgesJSON, &rawEdges); err != nil {
		return nil, fmt.Errorf("failed to parse edges: %w", err)
	}

	g := &Graph{
		Nodes: make(map[string]*GraphNode, len(rawNodes)),
	}

	// Build nodes
	for _, rn := range rawNodes {
		node := &GraphNode{
			ID:     rn.ID,
			Type:   rn.Type,
			Label:  rn.Label,
			Config: rn.Config,
		}
		g.Nodes[rn.ID] = node

		if rn.Type == "start" {
			if g.StartID != "" {
				return nil, fmt.Errorf("multiple start nodes found: %s and %s", g.StartID, rn.ID)
			}
			g.StartID = rn.ID
		}
	}

	if g.StartID == "" {
		return nil, fmt.Errorf("no start node found in workflow")
	}

	// Build edges and wire them to nodes
	for _, re := range rawEdges {
		edge := &GraphEdge{
			ID:           re.ID,
			Source:       re.Source,
			Target:       re.Target,
			SourceHandle: re.SourceHandle,
			TargetHandle: re.TargetHandle,
		}
		g.Edges = append(g.Edges, edge)

		if srcNode, ok := g.Nodes[re.Source]; ok {
			srcNode.OutEdges = append(srcNode.OutEdges, edge)
		}
		if tgtNode, ok := g.Nodes[re.Target]; ok {
			tgtNode.InEdges = append(tgtNode.InEdges, edge)
		}
	}

	// Validate: at least one response node exists
	hasResponse := false
	for _, n := range g.Nodes {
		if n.Type == "response" {
			hasResponse = true
			break
		}
	}
	if !hasResponse {
		return nil, fmt.Errorf("no response node found in workflow")
	}

	return g, nil
}

// ResolveNextNode finds the next node to execute based on the current node and the result handle.
func (g *Graph) ResolveNextNode(currentNodeID string, handle string) *GraphNode {
	current, ok := g.Nodes[currentNodeID]
	if !ok {
		return nil
	}

	for _, edge := range current.OutEdges {
		// Match edge source handle to the result handle
		if edge.SourceHandle == handle || (edge.SourceHandle == "" && handle == "out") {
			if next, ok := g.Nodes[edge.Target]; ok {
				return next
			}
		}
	}
	return nil
}

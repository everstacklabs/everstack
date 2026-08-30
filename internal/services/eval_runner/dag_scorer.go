package eval_runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const dagTraversalDepthLimit = 64

type dagDefinition struct {
	Root  string             `json:"root"`
	Nodes map[string]dagNode `json:"nodes"`
}

type dagNode struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	Prompt          string    `json:"prompt,omitempty"`
	Edges           []dagEdge `json:"edges,omitempty"`
	Children        []dagEdge `json:"children,omitempty"`
	Score           *float64  `json:"score,omitempty"`
	SubScorerPrompt string    `json:"sub_scorer_prompt,omitempty"`
}

type dagEdge struct {
	Label  string `json:"label"`
	Target string `json:"target"`
	NodeID string `json:"node_id"`
}

func hasDagDefinition(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return len(trimmed) > 2 && trimmed != "null"
}

func HasDagDefinition(data []byte) bool {
	return hasDagDefinition(data)
}

func ValidateDagDefinition(data []byte) error {
	_, err := parseDagDefinition(data)
	return err
}

func parseDagDefinition(data []byte) (*dagDefinition, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil, fmt.Errorf("dag_definition is empty")
	}

	var def dagDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("failed to parse dag_definition: %w", err)
	}
	for id, node := range def.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			node.ID = id
			def.Nodes[id] = node
		}
	}
	if err := validateDagDefinition(&def); err != nil {
		return nil, err
	}
	return &def, nil
}

func validateDagDefinition(def *dagDefinition) error {
	if def == nil {
		return fmt.Errorf("dag_definition is nil")
	}
	if strings.TrimSpace(def.Root) == "" {
		return fmt.Errorf("dag_definition must specify exactly one root")
	}
	if len(def.Nodes) == 0 {
		return fmt.Errorf("dag_definition must contain nodes")
	}
	if _, ok := def.Nodes[def.Root]; !ok {
		return fmt.Errorf("dag root %q does not exist", def.Root)
	}

	for id, node := range def.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("dag node %q is missing id", id)
		}
		if normalizeDagNodeType(node.Type) == "" {
			return fmt.Errorf("dag node %q has unknown type %q", id, node.Type)
		}

		edges := nodeEdges(node)
		if normalizeDagNodeType(node.Type) == "verdict" {
			if len(edges) > 0 {
				return fmt.Errorf("dag verdict node %q must be a leaf", id)
			}
			if node.Score == nil && strings.TrimSpace(node.SubScorerPrompt) == "" {
				return fmt.Errorf("dag verdict node %q must set score or sub_scorer_prompt", id)
			}
			if node.Score != nil && (*node.Score < 0 || *node.Score > 1) {
				return fmt.Errorf("dag verdict node %q score must be between 0 and 1", id)
			}
			continue
		}
		if strings.TrimSpace(node.Prompt) == "" {
			return fmt.Errorf("dag node %q must set prompt", id)
		}
		if len(edges) == 0 {
			return fmt.Errorf("dag node %q must have at least one edge", id)
		}
		for _, edge := range edges {
			target := edgeTarget(edge)
			if strings.TrimSpace(edge.Label) == "" {
				return fmt.Errorf("dag node %q has an edge without label", id)
			}
			if strings.TrimSpace(target) == "" {
				return fmt.Errorf("dag node %q edge %q has no target", id, edge.Label)
			}
			if _, ok := def.Nodes[target]; !ok {
				return fmt.Errorf("dag node %q edge %q targets missing node %q", id, edge.Label, target)
			}
		}
	}

	if err := validateDagCycles(def); err != nil {
		return err
	}
	if !hasReachableVerdict(def) {
		return fmt.Errorf("dag must have at least one reachable verdict")
	}
	return nil
}

func validateDagCycles(def *dagDefinition) error {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(def.Nodes))
	var visit func(string, []string) error
	visit = func(id string, path []string) error {
		switch state[id] {
		case visiting:
			return fmt.Errorf("dag contains a cycle at node %q along path %s", id, strings.Join(append(path, id), " -> "))
		case visited:
			return nil
		}
		state[id] = visiting
		node := def.Nodes[id]
		for _, edge := range nodeEdges(node) {
			target := edgeTarget(edge)
			if _, ok := def.Nodes[target]; !ok {
				continue
			}
			if err := visit(target, append(path, id)); err != nil {
				return err
			}
		}
		state[id] = visited
		return nil
	}
	for id := range def.Nodes {
		if err := visit(id, nil); err != nil {
			return err
		}
	}
	return nil
}

func hasReachableVerdict(def *dagDefinition) bool {
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		node := def.Nodes[id]
		if normalizeDagNodeType(node.Type) == "verdict" {
			return true
		}
		for _, edge := range nodeEdges(node) {
			if visit(edgeTarget(edge)) {
				return true
			}
		}
		return false
	}
	return visit(def.Root)
}

func (r *Runner) runDagScorer(ctx context.Context, cfg ScoreConfig, input, output, expected interface{}) (*ScoreResult, error) {
	def, err := parseDagDefinition(cfg.DagDefinition)
	if err != nil {
		return nil, err
	}

	intermediates := map[string]string{}
	path := make([]string, 0, 8)
	current := def.Root
	model := ""
	modelResolved := false
	resolveModel := func() (string, error) {
		if modelResolved {
			return model, nil
		}
		modelResolved = true
		first := firstEvalModel(cfg.EvalModel)
		resolved, err := r.resolveEvalModel(ctx, "", first)
		if err != nil {
			return "", err
		}
		model = resolved
		return model, nil
	}

	for depth := 0; depth < dagTraversalDepthLimit; depth++ {
		node, ok := def.Nodes[current]
		if !ok {
			return nil, fmt.Errorf("dag traversal reached missing node %q", current)
		}
		path = append(path, current)

		switch normalizeDagNodeType(node.Type) {
		case "verdict":
			score, err := r.runDagVerdict(ctx, cfg, node, input, output, expected, intermediates)
			if err != nil {
				return nil, err
			}
			return &ScoreResult{
				Name:   cfg.Name,
				Value:  score,
				Reason: strings.Join(path, " -> "),
			}, nil
		case "task", "binary_judgement", "non_binary_judgement":
			answer, err := r.runDagDecisionNode(ctx, cfg, node, input, output, expected, intermediates, resolveModel)
			if err != nil {
				return nil, err
			}
			intermediates[current] = answer
			intermediates["last"] = answer

			edge, fallback, ok := chooseDagEdge(nodeEdges(node), answer)
			if !ok {
				return nil, fmt.Errorf("dag node %q has no routable edges", current)
			}
			target := edgeTarget(edge)
			path[len(path)-1] = formatDagPathSegment(current, answer, edge, fallback)
			current = target
		default:
			return nil, fmt.Errorf("dag node %q has unknown type %q", current, node.Type)
		}
	}
	return nil, fmt.Errorf("dag traversal exceeded maximum depth %d", dagTraversalDepthLimit)
}

func (r *Runner) runDagDecisionNode(
	ctx context.Context,
	cfg ScoreConfig,
	node dagNode,
	input, output, expected interface{},
	intermediates map[string]string,
	resolveModel func() (string, error),
) (string, error) {
	model, err := resolveModel()
	if err != nil {
		return "", fmt.Errorf("failed to resolve dag eval model: %w", err)
	}
	labels := edgeLabels(nodeEdges(node))
	prompt := renderDagPrompt(node.Prompt, input, output, expected, intermediates)
	messages := buildDagDecisionMessages(node, prompt, labels)
	content, err := r.callJudge(ctx, model, messages, buildJudgeSampling(cfg.ModelParams), nil)
	if err != nil {
		return "", err
	}
	answer := extractDagDecision(content)
	if answer == "" {
		return "", fmt.Errorf("dag node %q returned an empty decision", node.ID)
	}
	return answer, nil
}

func (r *Runner) runDagVerdict(ctx context.Context, cfg ScoreConfig, node dagNode, input, output, expected interface{}, intermediates map[string]string) (float64, error) {
	if node.Score != nil {
		return *node.Score, nil
	}
	prompt := renderDagPrompt(node.SubScorerPrompt, input, output, expected, intermediates)
	zero, one := 0.0, 1.0
	subCfg := cfg
	subCfg.DataType = "NUMERIC"
	subCfg.EvalPrompt = prompt
	subCfg.Messages = nil
	subCfg.ChoiceScores = nil
	subCfg.MinValue = &zero
	subCfg.MaxValue = &one

	result, err := r.runSingleJudge(ctx, "", subCfg, firstEvalModel(cfg.EvalModel), input, output, expected, nil, "")
	if err != nil {
		return 0, err
	}
	score, ok := toFloat64(result.Value)
	if !ok {
		return 0, fmt.Errorf("dag sub_scorer_prompt returned non-numeric score %v", result.Value)
	}
	if score < 0 {
		return 0, nil
	}
	if score > 1 {
		return 1, nil
	}
	return score, nil
}

func buildDagDecisionMessages(node dagNode, prompt string, labels []string) []map[string]interface{} {
	system := "You are evaluating an Everstack deterministic DAG scorer node. Return only one route label."
	switch normalizeDagNodeType(node.Type) {
	case "binary_judgement":
		system = "You are evaluating an Everstack deterministic DAG scorer node. Answer only yes or no."
	case "non_binary_judgement":
		system = fmt.Sprintf("You are evaluating an Everstack deterministic DAG scorer node. Choose exactly one route label from: [%s]. Return only the label.", strings.Join(labels, ", "))
	case "task":
		if len(labels) > 0 {
			system = fmt.Sprintf("You are evaluating an Everstack deterministic DAG scorer node. Return only one route label from: [%s].", strings.Join(labels, ", "))
		}
	}
	return []map[string]interface{}{
		{"role": "system", "content": system},
		{"role": "user", "content": prompt},
	}
}

func renderDagPrompt(template string, input, output, expected interface{}, intermediates map[string]string) string {
	rendered := buildJudgePrompt(template, input, output, expected, nil, "")
	for key, value := range intermediates {
		rendered = strings.ReplaceAll(rendered, "{{"+key+"}}", value)
		rendered = strings.ReplaceAll(rendered, "{{intermediate."+key+"}}", value)
	}
	return rendered
}

func chooseDagEdge(edges []dagEdge, answer string) (dagEdge, string, bool) {
	if len(edges) == 0 {
		return dagEdge{}, "", false
	}
	target := normalizeDagDecision(answer)
	for _, edge := range edges {
		if normalizeDagDecision(edge.Label) == target {
			return edge, "", true
		}
	}
	for _, edge := range edges {
		if normalizeDagDecision(edge.Label) == "default" {
			return edge, "fallback:default", true
		}
	}
	return edges[0], "fallback:first", true
}

func extractDagDecision(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var asString string
	if err := json.Unmarshal([]byte(content), &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var asObject map[string]interface{}
	if err := json.Unmarshal([]byte(content), &asObject); err == nil {
		for _, key := range []string{"label", "answer", "choice", "verdict", "decision", "result"} {
			if v, ok := asObject[key]; ok {
				return strings.TrimSpace(fmt.Sprintf("%v", v))
			}
		}
	}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeDagDecision(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.Trim(s, "\"'`")
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".:")
	return strings.TrimSpace(s)
}

func formatDagPathSegment(nodeID, answer string, edge dagEdge, fallback string) string {
	label := edge.Label
	target := edgeTarget(edge)
	if fallback != "" {
		return fmt.Sprintf("%s[%q => %s -> %s, %s]", nodeID, answer, label, target, fallback)
	}
	return fmt.Sprintf("%s[%q => %s -> %s]", nodeID, answer, label, target)
}

func normalizeDagNodeType(t string) string {
	switch strings.TrimSpace(strings.ToLower(t)) {
	case "task", "binary_judgement", "non_binary_judgement", "verdict":
		return strings.TrimSpace(strings.ToLower(t))
	default:
		return ""
	}
}

func firstEvalModel(evalModel string) string {
	models := splitJuryModels(evalModel)
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

func nodeEdges(node dagNode) []dagEdge {
	edges := make([]dagEdge, 0, len(node.Edges)+len(node.Children))
	edges = append(edges, node.Edges...)
	edges = append(edges, node.Children...)
	return edges
}

func edgeTarget(edge dagEdge) string {
	if strings.TrimSpace(edge.Target) != "" {
		return edge.Target
	}
	return edge.NodeID
}

func edgeLabels(edges []dagEdge) []string {
	labels := make([]string, 0, len(edges))
	for _, edge := range edges {
		if label := strings.TrimSpace(edge.Label); label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

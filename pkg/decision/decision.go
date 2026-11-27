// Package decision implements the System One decision protocol shared by Jev,
// Kev and Laya. It is independent of chat completions and generated text.
package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

type Question struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions,omitempty"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}
type Request struct {
	Model     string              `json:"model"`
	State     json.RawMessage     `json:"state"`
	Questions map[string]Question `json:"questions"`
}
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}
type Provider interface {
	Decide(context.Context, Request) (Response, error)
}

// Validate uses the common subset supported by all three integrations. Limits
// bound option amplification before forwarding a request to a self-hosted model.
func (r Request) Validate() error {
	if r.Model == "" {
		return errors.New("model is required")
	}
	if len(r.State) == 0 || !json.Valid(r.State) {
		return errors.New("state must be JSON")
	}
	if len(r.Questions) < 1 || len(r.Questions) > 100 {
		return errors.New("questions must contain 1 to 100 entries")
	}
	for id, q := range r.Questions {
		if id == "" {
			return errors.New("question IDs must not be empty")
		}
		if len(q.Instructions) > 0 && !json.Valid(q.Instructions) {
			return fmt.Errorf("question %q has invalid instructions", id)
		}
		switch q.Type {
		case "noul":
			if len(q.Criteria) > 0 && string(q.Criteria) != "null" {
				var c map[string]json.RawMessage
				if json.Unmarshal(q.Criteria, &c) != nil || len(c) < 1 || len(c) > 2 {
					return fmt.Errorf("question %q: noul criteria accept true and false", id)
				}
				for key := range c {
					if key != "true" && key != "false" {
						return fmt.Errorf("question %q: invalid noul criterion", id)
					}
				}
			}
		case "choice":
			var c map[string]json.RawMessage
			if json.Unmarshal(q.Criteria, &c) != nil || len(c) < 1 || len(c) > 100 {
				return fmt.Errorf("question %q: choice criteria require 1 to 100 options", id)
			}
			for k := range c {
				if k == "" {
					return fmt.Errorf("question %q: empty choice", id)
				}
			}
		case "score":
			var c []json.RawMessage
			if json.Unmarshal(q.Criteria, &c) != nil || len(c) < 2 || len(c) > 10 {
				return fmt.Errorf("question %q: score criteria require 2 to 10 levels", id)
			}
		default:
			return fmt.Errorf("question %q: type must be noul, choice or score", id)
		}
	}
	return nil
}
func probability(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
func (r Response) Validate(req Request) error {
	if len(r.Answers) != len(req.Questions) || r.Usage.InputTokens < 0 || r.Usage.OutputTokens < 0 {
		return errors.New("invalid decision response")
	}
	for id, q := range req.Questions {
		a, ok := r.Answers[id]
		if !ok || a.Type != q.Type {
			return errors.New("missing or mismatched decision answer")
		}
		switch q.Type {
		case "noul":
			if a.Noul == nil || !probability(*a.Noul) {
				return errors.New("invalid noul probability")
			}
		case "choice", "score":
			if a.Confidence == nil || !probability(*a.Confidence) {
				return errors.New("invalid decision confidence")
			}
			var options map[string]json.RawMessage
			if q.Type == "choice" {
				_ = json.Unmarshal(q.Criteria, &options)
				if _, ok := options[a.Choice]; !ok {
					return errors.New("decision selected an unknown option")
				}
			} else {
				var levels []json.RawMessage
				_ = json.Unmarshal(q.Criteria, &levels)
				if a.Score == nil || math.IsNaN(*a.Score) || *a.Score < 0 || *a.Score > float64(len(levels)-1) {
					return errors.New("invalid decision score")
				}
				options = make(map[string]json.RawMessage, len(levels))
				for i := range levels {
					options[fmt.Sprint(i)] = nil
				}
			}
			if len(a.Probabilities) != len(options) {
				return errors.New("incomplete decision probabilities")
			}
			sum := 0.0
			for option, p := range a.Probabilities {
				if _, ok := options[option]; !ok || !probability(p) {
					return errors.New("invalid decision probabilities")
				}
				sum += p
			}
			if math.Abs(sum-1) > 0.01 {
				return errors.New("decision probabilities do not sum to one")
			}
		}
	}
	return nil
}

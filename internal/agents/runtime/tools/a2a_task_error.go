package tools

import "fmt"

// A2ATaskError retains the remote handle and observed state when message/send
// does not complete. It does not imply local persistence, polling or safe retry.
type A2ATaskError struct {
	TaskID      string
	ContextID   string
	State       string
	PartialText string
}

func (e *A2ATaskError) Error() string {
	return fmt.Sprintf("a2a_call: remote task state=%q task_id=%q context_id=%q; no completed answer; continuation is unsupported; do not automatically resend",
		e.State, e.TaskID, e.ContextID)
}

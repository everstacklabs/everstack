package runtime

import "strings"

// The ask_user tool result is consumed by two readers with opposite needs: the
// LLM, which wants a nudge to keep working after collecting input, and the
// session transcript, which should show only the user's actual words. We bake
// the nudge into the LLM-facing result (WrapUserAnswer) and expose
// DisplayUserAnswer so the persistence/render layers can recover the clean
// answer. Keeping both halves here guarantees the producer and the stripper
// never drift apart.
const (
	askUserAnswerPrefix       = "User answered: "
	askUserContinuationMarker = "\n\nContinue the task immediately using this information. Do not stop after collecting the answer, and do not ask the same question again unless the response is still genuinely insufficient."
)

// WrapUserAnswer formats a non-empty user answer for the LLM, appending the
// continuation nudge that keeps the agent moving after a HITL response.
func WrapUserAnswer(answer string) string {
	return askUserAnswerPrefix + answer + askUserContinuationMarker
}

// DisplayUserAnswer strips the LLM-steering wrapper from a persisted ask_user
// result, returning the user's actual words. Inputs without the wrapper
// (already-clean answers, timeout/empty sentinels) are returned trimmed and
// otherwise unchanged.
func DisplayUserAnswer(result string) string {
	text := result
	if i := strings.Index(text, askUserContinuationMarker); i != -1 {
		text = text[:i]
	}
	text = strings.TrimPrefix(text, askUserAnswerPrefix)
	return strings.TrimSpace(text)
}

package scores

// Reserved score name for the fix-attempt verdict labeled per agent turn or
// dataset run. Slices the existing outcome KPIs into win/fail/draw/no_change
// buckets so dashboards can answer "for the same problem, which model + prompt
// won, lost, or made no difference."
const FixAttemptVerdictName = "fix_attempt_verdict"

// Canonical verdict values. Anything else is rejected by ValidateFixAttemptVerdict.
//
// - VerdictWin:      the attempt achieved its goal (e.g. test passed, bug fixed).
// - VerdictFail:     the attempt produced a change that did not achieve the goal.
// - VerdictDraw:     the attempt produced a change but the outcome was unchanged
//                    (e.g. tests still pass / still fail at the same state).
// - VerdictNoChange: the agent emitted no edit — wasted iterations / loop / refusal.
const (
	VerdictWin      = "win"
	VerdictFail     = "fail"
	VerdictDraw     = "draw"
	VerdictNoChange = "no_change"
)

// FixAttemptVerdicts is the canonical set, in display order.
var FixAttemptVerdicts = []string{VerdictWin, VerdictFail, VerdictDraw, VerdictNoChange}

// ErrInvalidVerdictValue is returned when a score with name=fix_attempt_verdict
// has a value outside the canonical set.
var ErrInvalidVerdictValue = &ScoreError{Message: "invalid fix_attempt_verdict value (allowed: win, fail, draw, no_change)"}

// IsValidVerdict reports whether v is a canonical verdict value.
func IsValidVerdict(v string) bool {
	switch v {
	case VerdictWin, VerdictFail, VerdictDraw, VerdictNoChange:
		return true
	}
	return false
}

// FixAttemptVerdictScore constructs a categorical score for the reserved
// fix_attempt_verdict name. Returns ErrInvalidVerdictValue if verdict is not
// one of the canonical values.
func FixAttemptVerdictScore(traceID, verdict string, source ScoreSource) (*Score, error) {
	if !IsValidVerdict(verdict) {
		return nil, ErrInvalidVerdictValue
	}
	return CategoricalScore(traceID, FixAttemptVerdictName, verdict, source), nil
}

// ValidateFixAttemptVerdict returns ErrInvalidVerdictValue when the score
// carries the reserved name with a non-canonical StringValue. Other scores pass
// through. Call before persisting any externally-supplied score.
func ValidateFixAttemptVerdict(s *Score) error {
	if s == nil || s.Name != FixAttemptVerdictName {
		return nil
	}
	if s.DataType != ScoreDataTypeCategorical {
		return ErrInvalidVerdictValue
	}
	if s.StringValue == nil || !IsValidVerdict(*s.StringValue) {
		return ErrInvalidVerdictValue
	}
	return nil
}

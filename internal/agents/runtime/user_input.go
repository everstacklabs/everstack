package runtime

// UserInputRequest is sent from the ask_user tool handler to the SessionManager.
type UserInputOption struct {
	Label       string
	Value       string
	Description string
}

type UserInputRequest struct {
	InputID             string
	SessionID           string
	TenantID            string
	Question            string
	InputType           string
	Options             []UserInputOption
	AllowCustomResponse bool
	Placeholder         string
	MinSelections       int
	MaxSelections       int
	TimeoutSec          int
}

// UserInputResponse is sent from the SessionManager back to the ask_user tool handler.
type UserInputResponse struct {
	InputID string
	Text    string
}

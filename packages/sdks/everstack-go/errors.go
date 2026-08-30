package everstack

import (
	"encoding/json"
	"fmt"
)

// APIError is an error returned by the Everstack API.
type APIError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Param      string `json:"param,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("everstack: %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("everstack: %d: %s", e.StatusCode, e.Message)
}

// AuthenticationError indicates a 401 response.
type AuthenticationError struct{ *APIError }

// PermissionDeniedError indicates a 403 response.
type PermissionDeniedError struct{ *APIError }

// NotFoundError indicates a 404 response.
type NotFoundError struct{ *APIError }

// RateLimitError indicates a 429 response.
type RateLimitError struct{ *APIError }

// InternalServerError indicates a 500 response.
type InternalServerError struct{ *APIError }

// ServiceUnavailableError indicates a 503 response.
type ServiceUnavailableError struct{ *APIError }

// ConnectionError indicates a network-level failure.
type ConnectionError struct {
	Err error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("everstack: connection error: %v", e.Err)
}

func (e *ConnectionError) Unwrap() error {
	return e.Err
}

func parseAPIError(statusCode int, body []byte) error {
	apiErr := &APIError{StatusCode: statusCode, Message: "Unknown error"}

	var errBody struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Err     struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err == nil {
		if errBody.Message != "" {
			apiErr.Message = errBody.Message
		} else if errBody.Err.Message != "" {
			apiErr.Message = errBody.Err.Message
		}
		if errBody.Code != "" {
			apiErr.Code = errBody.Code
		} else if errBody.Err.Code != "" {
			apiErr.Code = errBody.Err.Code
		}
	}

	switch statusCode {
	case 401:
		return &AuthenticationError{apiErr}
	case 403:
		return &PermissionDeniedError{apiErr}
	case 404:
		return &NotFoundError{apiErr}
	case 429:
		return &RateLimitError{apiErr}
	case 500:
		return &InternalServerError{apiErr}
	case 503:
		return &ServiceUnavailableError{apiErr}
	default:
		return apiErr
	}
}

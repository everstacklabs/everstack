package mferrors

import "fmt"

var (
	_ PermissionDenied = (*PermissionDeniedError)(nil)
	_ Error            = (*PermissionDeniedError)(nil)
)

type PermissionDenied interface {
	Error
	IsPermissionDenied()
}

type PermissionDeniedError struct {
	*EverstackError
}

func ThrowPermissionDenied(parent error, id, message string) error {
	return &PermissionDeniedError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowPermissionDeniedf(parent error, id, format string, args ...interface{}) error {
	return ThrowPermissionDenied(parent, id, fmt.Sprintf(format, args...))
}

func (e *PermissionDeniedError) IsPermissionDenied() {}

func IsPermissionDenied(err error) bool {
	_, ok := err.(PermissionDenied)
	return ok
}

func (err *PermissionDeniedError) Is(target error) bool {
	t, ok := target.(*PermissionDeniedError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *PermissionDeniedError) Unwrap() error {
	return err.EverstackError
}

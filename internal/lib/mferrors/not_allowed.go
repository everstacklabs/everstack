package mferrors

import "fmt"

var (
	_ NotAllowed = (*NotAllowedError)(nil)
	_ Error      = (*NotAllowedError)(nil)
)

type NotAllowed interface {
	Error
	IsNotAllowed()
}

type NotAllowedError struct {
	*EverstackError
}

func ThrowNotAllowed(parent error, id, message string) error {
	return &NotAllowedError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowNotAllowedf(parent error, id, format string, args ...interface{}) error {
	return ThrowNotAllowed(parent, id, fmt.Sprintf(format, args...))
}

func (e *NotAllowedError) IsNotAllowed() {}

func IsNotAllowed(err error) bool {
	_, ok := err.(NotAllowed)
	return ok
}

func (err *NotAllowedError) Is(target error) bool {
	t, ok := target.(*NotAllowedError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *NotAllowedError) Unwrap() error {
	return err.EverstackError
}

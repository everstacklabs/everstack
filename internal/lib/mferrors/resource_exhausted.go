package mferrors

import "fmt"

var (
	_ ResourceExhausted = (*ResourceExhaustedError)(nil)
	_ Error             = (*ResourceExhaustedError)(nil)
)

type ResourceExhausted interface {
	Error
	IsResourceExhausted()
}

type ResourceExhaustedError struct {
	*EverstackError
}

func ThrowResourceExhausted(parent error, id, message string) error {
	return &ResourceExhaustedError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowResourceExhaustedf(parent error, id, format string, args ...interface{}) error {
	return ThrowResourceExhausted(parent, id, fmt.Sprintf(format, args...))
}

func (e *ResourceExhaustedError) IsResourceExhausted() {}

func IsResourceExhausted(err error) bool {
	_, ok := err.(ResourceExhausted)
	return ok
}

func (err *ResourceExhaustedError) Is(target error) bool {
	t, ok := target.(*ResourceExhaustedError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *ResourceExhaustedError) Unwrap() error {
	return err.EverstackError
}

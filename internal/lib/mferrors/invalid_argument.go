package mferrors

import "fmt"

var (
	_ InvalidArgument = (*InvalidArgumentError)(nil)
	_ Error           = (*InvalidArgumentError)(nil)
)

type InvalidArgument interface {
	Error
	IsInvalidArgument()
}

type InvalidArgumentError struct {
	*EverstackError
}

func ThrowInvalidArgument(parent error, id, message string) error {
	return &InvalidArgumentError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowInvalidArgumentf(parent error, id, format string, args ...interface{}) error {
	return ThrowInvalidArgument(parent, id, fmt.Sprintf(format, args...))
}

func (e *InvalidArgumentError) IsInvalidArgument() {}

func IsInvalidArgument(err error) bool {
	_, ok := err.(InvalidArgument)
	return ok
}

func (err *InvalidArgumentError) Is(target error) bool {
	t, ok := target.(*InvalidArgumentError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *InvalidArgumentError) Unwrap() error {
	return err.EverstackError
}

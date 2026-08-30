package mferrors

import "fmt"

var (
	_ Unknown = (*UnknownError)(nil)
	_ Error   = (*UnknownError)(nil)
)

type Unknown interface {
	Error
	IsUnknown()
}

type UnknownError struct {
	*EverstackError
}

func ThrowUnknown(parent error, id, message string) error {
	return &UnknownError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowUnknownf(parent error, id, format string, args ...interface{}) error {
	return ThrowUnknown(parent, id, fmt.Sprintf(format, args...))
}

func (e *UnknownError) IsUnknown() {}

func IsUnknown(err error) bool {
	_, ok := err.(Unknown)
	return ok
}

func (err *UnknownError) Is(target error) bool {
	t, ok := target.(*UnknownError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *UnknownError) Unwrap() error {
	return err.EverstackError
}

package mferrors

import (
	"fmt"
)

var (
	_ Internal = (*InternalError)(nil)
	_ Error    = (*InternalError)(nil)
)

type Internal interface {
	error
	IsInternal()
}

type InternalError struct {
	*EverstackError
}

func ThrowInternal(parent error, id, message string) error {
	return &InternalError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowInternalf(parent error, id, format string, a ...interface{}) error {
	return &InternalError{
		CreateEverstackError(parent, id, fmt.Sprintf(format, a...)),
	}
}

func (err *InternalError) IsInternal() {}

func (err *InternalError) Is(target error) bool {
	t, ok := target.(*InternalError)
	if !ok {
		return false
	}

	return err.EverstackError.Is(t.EverstackError)
}

func IsInternal(err error) bool {
	_, ok := err.(Internal)
	return ok
}

func (err *InternalError) Unwrap() error {
	return err.EverstackError
}

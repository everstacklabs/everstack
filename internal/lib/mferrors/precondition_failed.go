package mferrors

import "fmt"

var (
	_ PreconditionFailed = (*PreconditionFailedError)(nil)
	_ Error              = (*PreconditionFailedError)(nil)
)

type PreconditionFailed interface {
	Error
	IsPreconditionFailed()
}

type PreconditionFailedError struct {
	*EverstackError
}

func ThrowPreconditionFailed(parent error, id, message string) error {
	return &PreconditionFailedError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowPreconditionFailedf(parent error, id, format string, args ...interface{}) error {
	return ThrowPreconditionFailed(parent, id, fmt.Sprintf(format, args...))
}

func (e *PreconditionFailedError) IsPreconditionFailed() {}

func IsPreconditionFailed(err error) bool {
	_, ok := err.(PreconditionFailed)
	return ok
}

func (err *PreconditionFailedError) Is(target error) bool {
	t, ok := target.(*PreconditionFailedError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *PreconditionFailedError) Unwrap() error {
	return err.EverstackError
}

package mferrors

import "fmt"

var (
	_ NotFound = (*NotFoundError)(nil)
	_ Error    = (*NotFoundError)(nil)
)

type NotFound interface {
	Error
	IsNotFound()
}

type NotFoundError struct {
	*EverstackError
}

func ThrowNotFound(parent error, id, message string) error {
	return &NotFoundError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowNotFoundf(parent error, id, format string, args ...interface{}) error {
	return ThrowNotFound(parent, id, fmt.Sprintf(format, args...))
}

func (e *NotFoundError) IsNotFound() {}

func IsNotFound(err error) bool {
	_, ok := err.(NotFound)
	return ok
}

func (err *NotFoundError) Is(target error) bool {
	t, ok := target.(*NotFoundError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *NotFoundError) Unwrap() error {
	return err.EverstackError
}

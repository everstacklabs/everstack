package mferrors

import "fmt"

var (
	_ AlreadyExists = (*AlreadyExistsError)(nil)
	_ Error         = (*AlreadyExistsError)(nil)
)

type AlreadyExists interface {
	error
	IsAlreadyExists()
}

type AlreadyExistsError struct {
	*EverstackError
}

func ThrowAlreadyExists(parent error, id, message string) error {
	return &AlreadyExistsError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowAlreadyExistsf(parent error, id, format string, a ...interface{}) error {
	return &AlreadyExistsError{
		CreateEverstackError(parent, id, fmt.Sprintf(format, a...)),
	}
}

func (err *AlreadyExistsError) IsAlreadyExists() {}

func (err *AlreadyExistsError) Is(target error) bool {
	t, ok := target.(*AlreadyExistsError)
	if !ok {
		return false
	}

	return err.EverstackError.Is(t.EverstackError)
}

func IsErrorAlreadyExists(err error) bool {
	_, ok := err.(AlreadyExists)
	return ok
}

func (err *AlreadyExistsError) Unwrap() error {
	return err.EverstackError.Unwrap()
}

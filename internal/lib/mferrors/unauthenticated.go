package mferrors

import "fmt"

var (
	_ Unauthenticated = (*UnauthenticatedError)(nil)
	_ Error           = (*UnauthenticatedError)(nil)
)

type Unauthenticated interface {
	Error
	IsUnauthenticated()
}

type UnauthenticatedError struct {
	*EverstackError
}

func ThrowUnauthenticated(parent error, id, message string) error {
	return &UnauthenticatedError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowUnauthenticatedf(parent error, id, format string, args ...interface{}) error {
	return ThrowUnauthenticated(parent, id, fmt.Sprintf(format, args...))
}

func (e *UnauthenticatedError) IsUnauthenticated() {}

func IsUnauthenticated(err error) bool {
	_, ok := err.(Unauthenticated)
	return ok
}

func (err *UnauthenticatedError) Is(target error) bool {
	t, ok := target.(*UnauthenticatedError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *UnauthenticatedError) Unwrap() error {
	return err.EverstackError
}

package mferrors

import "fmt"

var (
	_ Unavailable = (*UnavailableError)(nil)
	_ Error       = (*UnavailableError)(nil)
)

type Unavailable interface {
	Error
	IsUnavailable()
}

type UnavailableError struct {
	*EverstackError
}

func ThrowUnavailable(parent error, id, message string) error {
	return &UnavailableError{
		CreateEverstackError(parent, id, message),
	}
}

func ThrowUnavailablef(parent error, id, format string, args ...interface{}) error {
	return ThrowUnavailable(parent, id, fmt.Sprintf(format, args...))
}

func (e *UnavailableError) IsUnavailable() {}

func IsUnavailable(err error) bool {
	_, ok := err.(Unavailable)
	return ok
}

func (err *UnavailableError) Is(target error) bool {
	t, ok := target.(*UnavailableError)
	if !ok {
		return false
	}
	return err.EverstackError.Is(t.EverstackError)
}

func (err *UnavailableError) Unwrap() error {
	return err.EverstackError
}

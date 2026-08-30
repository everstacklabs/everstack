package mferrors

import (
	"errors"
	"fmt"
	"reflect"
)

var _ Error = (*EverstackError)(nil)

type EverstackError struct {
	Parent  error
	Message string
	ID      string
}

func ThrowError(parent error, message, id string) error {
	return CreateEverstackError(parent, message, id)
}

func CreateEverstackError(parent error, message string, id string) *EverstackError {
	return &EverstackError{
		Parent:  parent,
		Message: message,
		ID:      id,
	}
}

func (e *EverstackError) Error() string {
	if e.Parent != nil {
		return fmt.Sprintf("ID=%s Message=%s Parent=%s", e.ID, e.Message, e.Parent)
	}

	return fmt.Sprintf("ID=%s Message=%s", e.ID, e.Message)
}

func (e *EverstackError) Unwrap() error {
	return e.Parent
}

func (e *EverstackError) GetID() string {
	return e.ID
}

func (e *EverstackError) GetMessage() string {
	return e.Message
}

func (e *EverstackError) GetParent() error {
	return e.Parent
}

func (e *EverstackError) SetMessage(message string) {
	e.Message = message
}

func (e *EverstackError) Is(target error) bool {
	t, ok := target.(*EverstackError)

	if !ok {
		return false
	}

	if t.ID != "" && t.ID != e.ID {
		return false
	}

	if t.Message != "" && t.Message != e.Message {
		return false
	}

	if t.Parent != nil && !errors.Is(e.Parent, t.Parent) {
		return false
	}

	return true
}

func (e *EverstackError) As(target interface{}) bool {
	_, ok := target.(*EverstackError)
	if !ok {
		return false
	}
	reflect.Indirect(reflect.ValueOf(target)).Set(reflect.ValueOf(e))
	return true
}

func IsEverstackError(err error) bool {
	everstackErr := new(EverstackError)
	return errors.As(err, &everstackErr)
}

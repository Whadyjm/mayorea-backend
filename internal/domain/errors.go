package domain

import (
	"errors"
	"fmt"
)

// Kind classifies a domain error so outer layers can translate it
// (HTTP status, CLI exit code...) without knowing business details.
type Kind int

const (
	KindInvalid  Kind = iota + 1 // malformed or missing input
	KindNotFound                 // referenced entity does not exist
	KindConflict                 // state changed under the caller's feet
	KindRule                     // a business rule forbids the operation

	KindUnauthenticated // no valid credentials
	KindForbidden       // authenticated, but not allowed
	KindThrottled       // too many attempts
)

// Error is the single error type produced by the domain and use cases.
// Messages are user-facing (Spanish); the frontend shows them verbatim.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(k Kind, format string, a ...any) error {
	return &Error{Kind: k, Message: fmt.Sprintf(format, a...)}
}

func Invalid(format string, a ...any) error  { return newError(KindInvalid, format, a...) }
func NotFound(format string, a ...any) error { return newError(KindNotFound, format, a...) }
func Conflict(format string, a ...any) error { return newError(KindConflict, format, a...) }
func Rule(format string, a ...any) error     { return newError(KindRule, format, a...) }

func Unauthenticated(format string, a ...any) error {
	return newError(KindUnauthenticated, format, a...)
}
func Forbidden(format string, a ...any) error { return newError(KindForbidden, format, a...) }
func Throttled(format string, a ...any) error { return newError(KindThrottled, format, a...) }

// KindOf returns the Kind of err, or 0 when err is not a domain error.
func KindOf(err error) Kind {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return 0
}

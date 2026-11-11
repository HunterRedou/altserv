package billing

import "errors"

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid billing input")
	ErrNotFound  = errors.New("billing run not found")
	ErrConflict  = errors.New("billing conflict")
)

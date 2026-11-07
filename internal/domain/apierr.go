package domain

import (
	"fmt"
	"net/http"
)

// API error codes are the stable contract for HTTP and MCP handlers.
const (
	CodeUnauthenticated = "unauthenticated"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeValidation      = "validation"
	CodeConflict        = "conflict"
	CodeAppendOnly      = "append_only"
	CodeRateLimited     = "rate_limited"
)

// APIError is the JSON body the API wave should return.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Field != "" {
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// HTTPStatus maps the code to the status the API serves.
func (e *APIError) HTTPStatus() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	switch e.Code {
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeValidation:
		return http.StatusBadRequest
	case CodeConflict, CodeAppendOnly:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// Unauthenticated constructs a 401 error.
func Unauthenticated(message string) *APIError {
	return &APIError{Code: CodeUnauthenticated, Message: message}
}

// Forbidden constructs a 403 error.
func Forbidden(message string) *APIError {
	return &APIError{Code: CodeForbidden, Message: message}
}

// NotFound constructs a 404 error.
func NotFound(entity string) *APIError {
	return &APIError{Code: CodeNotFound, Message: entity + " not found"}
}

// Invalid constructs a 400 error for one field.
func Invalid(field, message string) *APIError {
	return &APIError{Code: CodeValidation, Message: message, Field: field}
}

// Conflict constructs a 409 error.
func Conflict(message string) *APIError {
	return &APIError{Code: CodeConflict, Message: message}
}

// AppendOnly constructs a 409 error for a ledger or version that cannot change.
func AppendOnly(entity string) *APIError {
	return &APIError{Code: CodeAppendOnly, Message: entity + " cannot be changed"}
}

// RateLimited constructs a 429 error.
func RateLimited() *APIError {
	return &APIError{Code: CodeRateLimited, Message: "rate limit exceeded"}
}

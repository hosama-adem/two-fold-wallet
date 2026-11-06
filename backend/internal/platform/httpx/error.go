package httpx

import (
	"errors"
	"net/http"
	"time"
)

// Stable machine-readable codes used across the API.
const (
	CodeBadRequest       = "bad_request"
	CodeNotFound         = "not_found"
	CodeConflict         = "conflict"
	CodePayloadTooLarge  = "payload_too_large"
	CodeValidationFailed = "validation_failed"
	CodeInternal         = "internal_error"
	CodeRequestTimeout   = "request_timeout"
	CodeRateLimited      = "rate_limited"
)

// Error is a domain/transport error that maps cleanly to a Problem.
type Error struct {
	Code              string
	Title             string
	Detail            string
	Status            int
	RetryAfterSeconds int
	wrapped           error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	if e.wrapped != nil {
		return e.wrapped.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.wrapped }

// New builds an httpx.Error.
func New(status int, code, title, detail string) *Error {
	return &Error{Status: status, Code: code, Title: title, Detail: detail}
}

// Wrap attaches a cause while preserving status/code.
func Wrap(err error, status int, code, title, detail string) *Error {
	return &Error{Status: status, Code: code, Title: title, Detail: detail, wrapped: err}
}

func BadRequest(detail string) *Error {
	return New(http.StatusBadRequest, CodeBadRequest, "Bad Request", detail)
}

func NotFound(detail string) *Error {
	return New(http.StatusNotFound, CodeNotFound, "Not Found", detail)
}

func Conflict(detail string) *Error {
	return New(http.StatusConflict, CodeConflict, "Conflict", detail)
}

func PayloadTooLarge(detail string) *Error {
	return New(http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "Payload Too Large", detail)
}

func ValidationFailed(detail string) *Error {
	return New(http.StatusUnprocessableEntity, CodeValidationFailed, "Unprocessable Entity", detail)
}

func Internal(detail string) *Error {
	if detail == "" {
		detail = "An unexpected error occurred."
	}
	return New(http.StatusInternalServerError, CodeInternal, "Internal Server Error", detail)
}

// RateLimited returns a 429 problem with Retry-After seconds.
func RateLimited(retryAfter time.Duration) *Error {
	sec := int(retryAfter.Round(time.Second) / time.Second)
	if sec < 1 {
		sec = 1
	}
	return &Error{
		Status:            http.StatusTooManyRequests,
		Code:              CodeRateLimited,
		Title:             "Too Many Requests",
		Detail:            "Too many requests. Please try again later.",
		RetryAfterSeconds: sec,
	}
}

// AsError extracts an *Error from err, if present.
func AsError(err error) (*Error, bool) {
	var he *Error
	if errors.As(err, &he) {
		return he, true
	}
	return nil, false
}

// MapCommon turns a few sentinel-style failures into API problems.
// Unknown errors become a safe 500 (no internal detail leaked).
func MapCommon(err error) *Error {
	if err == nil {
		return nil
	}
	if he, ok := AsError(err); ok {
		return he
	}
	return Internal("An unexpected error occurred.")
}

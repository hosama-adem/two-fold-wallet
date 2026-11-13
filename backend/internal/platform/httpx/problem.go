package httpx

import (
	"fmt"
	"time"
)

const (
	problemTypeBase = "https://twofold.dev/errors/"
	contentType     = "application/problem+json"
)

// Problem is an RFC 9457 problem details document.
type Problem struct {
	Type           string     `json:"type"`
	Title          string     `json:"title"`
	Status         int        `json:"status"`
	Detail         string     `json:"detail,omitempty"`
	Code           string     `json:"code"`
	RequestID      string     `json:"request_id,omitempty"`
	TriesRemaining *int       `json:"tries_remaining,omitempty"`
	LockedUntil    *time.Time `json:"locked_until,omitempty"`
	Limit          *string    `json:"limit,omitempty"`
	RemainingMinor *int64     `json:"remaining_minor,omitempty"`
}

func problemType(code string) string {
	return problemTypeBase + code
}

func newProblem(status int, code, title, detail, requestID string) Problem {
	if title == "" {
		title = httpStatusTitle(status)
	}
	return Problem{
		Type:      problemType(code),
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: requestID,
	}
}

func httpStatusTitle(status int) string {
	switch status {
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 409:
		return "Conflict"
	case 413:
		return "Payload Too Large"
	case 422:
		return "Unprocessable Entity"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 503:
		return "Service Unavailable"
	default:
		return fmt.Sprintf("HTTP %d", status)
	}
}

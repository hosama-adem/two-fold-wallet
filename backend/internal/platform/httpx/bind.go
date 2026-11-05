package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// BindJSON decodes JSON into dst and rejects unknown fields.
func BindJSON(c *gin.Context, dst any) error {
	if c.Request.Body == nil {
		return BadRequest("Request body is required.")
	}

	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return mapJSONError(err)
	}

	// Reject trailing junk after the first JSON value.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return BadRequest("Request body must contain a single JSON object.")
	}
	return nil
}

func mapJSONError(err error) *Error {
	if err == nil {
		return nil
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return PayloadTooLarge("Request body must be at most 1 MB.")
	}
	// Older Go / wrapped forms
	if errors.Is(err, http.ErrBodyReadAfterClose) {
		return BadRequest("Could not read request body.")
	}
	msg := err.Error()
	if strings.Contains(msg, "http: request body too large") {
		return PayloadTooLarge("Request body must be at most 1 MB.")
	}
	if strings.Contains(msg, "unknown field") {
		return BadRequest("Request contains unknown fields.")
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return BadRequest("Malformed JSON body.")
	}
	var typ *json.UnmarshalTypeError
	if errors.As(err, &typ) {
		return ValidationFailed("JSON field has the wrong type.")
	}
	return BadRequest("Invalid JSON body.")
}

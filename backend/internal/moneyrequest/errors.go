package moneyrequest

import (
	"net/http"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func errNotFound() *httpx.Error {
	return httpx.NotFound("Money request not found.")
}

func errNotPending(detail string) *httpx.Error {
	return httpx.New(http.StatusConflict, "request_not_pending", "Request not pending", detail)
}

func errForbidden() *httpx.Error {
	return httpx.New(http.StatusForbidden, "forbidden", "Forbidden", "You cannot act on this money request.")
}

func errSelfRequest() *httpx.Error {
	return httpx.ValidationFailed("You cannot request money from yourself.")
}

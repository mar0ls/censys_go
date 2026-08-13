package censysx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/censys/censys-sdk-go/models/sdkerrors"
)

// StatusOf returns the HTTP status carried by err, or 0 if it carries none.
func StatusOf(err error) int {
	var model *sdkerrors.ErrorModel
	if errors.As(err, &model) && model.Status != nil {
		return int(*model.Status)
	}
	var sdkErr *sdkerrors.SDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.StatusCode
	}
	return 0
}

// IsAuth reports whether err is a credential problem rather than a transient one.
func IsAuth(err error) bool {
	var authErr *sdkerrors.AuthenticationError
	if errors.As(err, &authErr) {
		return true
	}
	switch StatusOf(err) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	}
	return false
}

// IsNotFound reports whether the requested asset is simply absent from Censys.
func IsNotFound(err error) bool {
	return StatusOf(err) == http.StatusNotFound
}

// problemDocument is the RFC 9457 shape the API returns on error.
type problemDocument struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// Explain renders err for a human, unwrapping the SDK's JSON-blob error strings
// into something that fits on one line.
func Explain(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out; some endpoints are slow, so try a longer --timeout or a narrower window"
	}

	var model *sdkerrors.ErrorModel
	if errors.As(err, &model) {
		var title, detail string
		if model.Title != nil {
			title = *model.Title
		}
		if model.Detail != nil {
			detail = *model.Detail
		}
		if line := joinProblem(title, detail); line != "" {
			return line
		}
	}

	var sdkErr *sdkerrors.SDKError
	if errors.As(err, &sdkErr) {
		// The SDK only decodes an ErrorModel for the status codes each operation
		// declares; anything else arrives as a generic SDKError with the problem
		// document left in Body. Without this the useful part is thrown away and
		// a 422 reads as a bare "API error occurred".
		var problem problemDocument
		if json.Unmarshal([]byte(sdkErr.Body), &problem) == nil {
			if line := joinProblem(problem.Title, problem.Detail); line != "" {
				return fmt.Sprintf("%s (HTTP %d)", line, sdkErr.StatusCode)
			}
		}
		return fmt.Sprintf("%s (HTTP %d)", sdkErr.Message, sdkErr.StatusCode)
	}

	return err.Error()
}

func joinProblem(title, detail string) string {
	switch {
	case title != "" && detail != "":
		return title + ": " + detail
	case detail != "":
		return detail
	default:
		return title
	}
}

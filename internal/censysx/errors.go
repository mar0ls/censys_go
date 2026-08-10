package censysx

import (
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

// Explain renders err for a human, unwrapping the SDK's JSON-blob error strings
// into something that fits on one line.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	var model *sdkerrors.ErrorModel
	if errors.As(err, &model) {
		title, detail := "", ""
		if model.Title != nil {
			title = *model.Title
		}
		if model.Detail != nil {
			detail = *model.Detail
		}
		switch {
		case title != "" && detail != "":
			return fmt.Sprintf("%s: %s", title, detail)
		case detail != "":
			return detail
		case title != "":
			return title
		}
	}
	var sdkErr *sdkerrors.SDKError
	if errors.As(err, &sdkErr) {
		return fmt.Sprintf("%s (HTTP %d)", sdkErr.Message, sdkErr.StatusCode)
	}
	return err.Error()
}

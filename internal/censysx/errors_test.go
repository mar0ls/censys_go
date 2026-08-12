package censysx

import (
	"errors"
	"fmt"
	"testing"

	"github.com/censys/censys-sdk-go/models/sdkerrors"
)

func TestStatusOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("boom"), 0},
		{"error model", &sdkerrors.ErrorModel{Status: ptr(int64(429))}, 429},
		{"sdk error", &sdkerrors.SDKError{StatusCode: 503}, 503},
		{"wrapped", fmt.Errorf("host 1.1.1.1: %w", &sdkerrors.SDKError{StatusCode: 404}), 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusOf(tc.err); got != tc.want {
				t.Errorf("StatusOf() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIsAuth(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"authentication error", &sdkerrors.AuthenticationError{}, true},
		{"401", &sdkerrors.SDKError{StatusCode: 401}, true},
		{"403", &sdkerrors.ErrorModel{Status: ptr(int64(403))}, true},
		{"429 is not auth", &sdkerrors.SDKError{StatusCode: 429}, false},
		{"plain error", errors.New("connection reset"), false},
		// The old substring matcher flagged this as an auth failure because the
		// body mentions "access token"; it is a 400.
		{"400 mentioning token", &sdkerrors.SDKError{StatusCode: 400, Body: "invalid access token field"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAuth(tc.err); got != tc.want {
				t.Errorf("IsAuth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&sdkerrors.SDKError{StatusCode: 404}) {
		t.Error("404 not reported as not-found")
	}
	if IsNotFound(&sdkerrors.SDKError{StatusCode: 500}) {
		t.Error("500 reported as not-found")
	}
}

func TestExplain(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain", errors.New("boom"), "boom"},
		{
			"title and detail",
			&sdkerrors.ErrorModel{Title: ptr("Bad Request"), Detail: ptr("unknown field 'foo'")},
			"Bad Request: unknown field 'foo'",
		},
		{"detail only", &sdkerrors.ErrorModel{Detail: ptr("rate limited")}, "rate limited"},
		{"sdk error", &sdkerrors.SDKError{Message: "unexpected response", StatusCode: 502}, "unexpected response (HTTP 502)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Explain(tc.err); got != tc.want {
				t.Errorf("Explain() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Package censysx wraps the Censys SDK with the retry policy, pagination and
// typed accessors this CLI needs, so command code never touches raw envelopes.
package censysx

import (
	"time"

	censys "github.com/censys/censys-sdk-go"
	"github.com/censys/censys-sdk-go/retry"
)

// Defaults applied when Options leaves a field at its zero value.
const (
	DefaultTimeout  = 60 * time.Second
	DefaultPageSize = 50
)

// retryBudgetRatio is the share of Timeout the retry loop may consume. Leaving
// headroom means a call that exhausts its retries reports the last HTTP error
// rather than a bare "context deadline exceeded".
const retryBudgetRatio = 0.9

// Options configures a Client.
type Options struct {
	OrgID string
	Token string

	// Timeout bounds one logical API call including every retry: the SDK applies
	// it to the context before entering its backoff loop.
	Timeout time.Duration

	// DisableRetry turns off the SDK backoff policy, so a call is attempted once.
	DisableRetry bool

	// PageSize is the default page size for paginated calls.
	PageSize int

	// BaseURL overrides the Censys API endpoint. Empty uses the SDK default.
	BaseURL string
}

// Client is a thin, typed facade over the Censys SDK.
type Client struct {
	sdk      *censys.SDK
	orgID    string
	pageSize int
}

// New builds a Client. Retries are delegated to the SDK, which already applies
// exponential backoff with jitter, honours Retry-After, retries only
// 429/500/502/503/504, and aborts on context cancellation.
func New(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.PageSize <= 0 {
		opts.PageSize = DefaultPageSize
	}

	sdkOpts := []censys.SDKOption{
		censys.WithSecurity(opts.Token),
		censys.WithTimeout(opts.Timeout),
	}
	// Only set the organization when there is one: WithOrganizationID stores a
	// pointer unconditionally, so passing "" would put an empty
	// organization_id on every request. Asset lookups work without one.
	if opts.OrgID != "" {
		sdkOpts = append(sdkOpts, censys.WithOrganizationID(opts.OrgID))
	}
	if opts.BaseURL != "" {
		sdkOpts = append(sdkOpts, censys.WithServerURL(opts.BaseURL))
	}
	if !opts.DisableRetry {
		sdkOpts = append(sdkOpts, censys.WithRetryConfig(backoff(opts.Timeout)))
	}

	return &Client{
		sdk:      censys.New(sdkOpts...),
		orgID:    opts.OrgID,
		pageSize: opts.PageSize,
	}
}

// OrgID returns the organization the client acts on behalf of.
func (c *Client) OrgID() string { return c.orgID }

// SDK exposes the underlying SDK for calls this package does not wrap yet.
func (c *Client) SDK() *censys.SDK { return c.sdk }

// backoff builds the SDK retry policy. retry.BackoffStrategy takes milliseconds.
func backoff(timeout time.Duration) retry.Config {
	return retry.Config{
		Strategy: "backoff",
		Backoff: &retry.BackoffStrategy{
			InitialInterval: 500,
			MaxInterval:     10_000,
			Exponent:        1.5,
			MaxElapsedTime:  int(float64(timeout.Milliseconds()) * retryBudgetRatio),
		},
		RetryConnectionErrors: true,
	}
}

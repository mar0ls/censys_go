package censysx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
	"github.com/censys/censys-sdk-go/models/operations"
	sdktypes "github.com/censys/censys-sdk-go/types"
)

// Balance is a credit balance from either the organization or the user wallet,
// whichever applies to the configured credentials.
type Balance struct {
	Credits int64      `json:"balance"`
	Scope   string     `json:"scope"`
	Renews  *time.Time `json:"renews_at,omitempty"`
	Expires *time.Time `json:"expires_at,omitempty"`
}

// Balance returns the credit balance, reading the user wallet when no
// organization is configured. The organization endpoint needs an organization
// ID, so without this a token-only account could not see its balance at all,
// even though /v3/accounts/users/credits answers for exactly that case.
func (c *Client) Balance(ctx context.Context) (*Balance, error) {
	if c.orgID == "" {
		resp, err := c.sdk.AccountManagement.GetUserCredits(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetching user credits: %w", err)
		}
		result := resp.GetResponseEnvelopeUserCredits().GetResult()
		if result == nil {
			return nil, errors.New("user credits: response carried no result")
		}
		return &Balance{Credits: result.GetBalance(), Scope: "user", Renews: result.GetResetsAt()}, nil
	}

	credits, err := c.Credits(ctx)
	if err != nil {
		return nil, err
	}
	balance := &Balance{Credits: credits.GetBalance(), Scope: "organization"}
	if exp := NextExpiry(credits); exp != nil {
		balance.Expires = exp.ExpiresAt
	}
	return balance, nil
}

// Credits returns the organization's current credit balance.
func (c *Client) Credits(ctx context.Context) (*components.OrganizationCredits, error) {
	resp, err := c.sdk.AccountManagement.GetOrganizationCredits(ctx,
		operations.V3AccountmanagementOrgCreditsRequest{OrganizationID: c.orgID})
	if err != nil {
		return nil, fmt.Errorf("fetching credits: %w", err)
	}

	result := resp.GetResponseEnvelopeOrganizationCredits().GetResult()
	if result == nil {
		return nil, errors.New("credits: response carried no result")
	}
	return result, nil
}

// CreditUsage returns the daily credit usage report covering the last `days` days.
func (c *Client) CreditUsage(ctx context.Context, days int) (*components.CreditUsageReport, error) {
	if days <= 0 {
		return nil, errors.New("usage window must be at least one day")
	}

	start := time.Now().AddDate(0, 0, -days).Format(time.DateOnly)
	startDate, err := sdktypes.NewDateFromString(start)
	if err != nil {
		return nil, fmt.Errorf("invalid start date %q: %w", start, err)
	}

	resp, err := c.sdk.AccountManagement.GetOrganizationCreditUsage(ctx,
		operations.V3AccountmanagementOrgCreditsUsageRequest{
			OrganizationID: c.orgID,
			StartDate:      startDate,
			Granularity:    operations.GranularityDaily,
		})
	if err != nil {
		return nil, fmt.Errorf("fetching credit usage: %w", err)
	}

	result := resp.GetResponseEnvelopeCreditUsageReport().GetResult()
	if result == nil {
		return nil, errors.New("credit usage: response carried no result")
	}
	return result, nil
}

// NextExpiry returns the soonest credit expiry, or nil if none is scheduled.
func NextExpiry(credits *components.OrganizationCredits) *components.CreditExpiration {
	if credits == nil {
		return nil
	}
	var soonest *components.CreditExpiration
	for i := range credits.CreditExpirations {
		exp := &credits.CreditExpirations[i]
		if exp.ExpiresAt == nil {
			continue
		}
		if soonest == nil || exp.ExpiresAt.Before(*soonest.ExpiresAt) {
			soonest = exp
		}
	}
	return soonest
}

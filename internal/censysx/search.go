package censysx

import (
	"context"
	"errors"
	"fmt"

	censys "github.com/censys/censys-sdk-go"
	"github.com/censys/censys-sdk-go/models/components"
	"github.com/censys/censys-sdk-go/models/operations"
)

// DefaultSearchFields is the field set requested from Search. Keeping it tight
// keeps responses small; every name here maps onto a field the response model
// actually carries.
var DefaultSearchFields = []string{
	"host.ip",
	"host.name",
	"host.location.country",
	"host.location.city",
	"host.autonomous_system.name",
	"host.autonomous_system.asn",
	"host.services.port",
	"host.services.protocol",
	"host.services.transport_protocol",
	"host.services.software",
	"host.services.tls.fingerprint_sha256",
	"host.services.jarm.fingerprint",
	"host.last_updated_at",
}

// SearchParams describes one search request.
type SearchParams struct {
	Query string

	// Fields limits the response payload. Empty means DefaultSearchFields;
	// use AllFields to opt out of field selection entirely.
	Fields []string

	// AllFields requests the complete record instead of a field subset.
	AllFields bool

	// PageSize overrides the client default.
	PageSize int
}

// SearchPage is one page of results.
type SearchPage struct {
	Hits          []components.SearchQueryHit
	TotalHits     float64
	NextPageToken string
}

// ErrEmptyQuery is returned when a search is attempted without a query string.
var ErrEmptyQuery = errors.New("query cannot be empty")

// Search fetches a single page of results, starting from pageToken (empty for
// the first page).
func (c *Client) Search(ctx context.Context, p SearchParams, pageToken string) (*SearchPage, error) {
	if p.Query == "" {
		return nil, ErrEmptyQuery
	}

	body := components.SearchQueryInputBody{
		Query:    p.Query,
		PageSize: censys.Pointer(int64(c.pageSizeOr(p.PageSize))),
	}
	if !p.AllFields {
		body.Fields = p.Fields
		if len(body.Fields) == 0 {
			body.Fields = DefaultSearchFields
		}
	}
	if pageToken != "" {
		body.PageToken = censys.Pointer(pageToken)
	}

	resp, err := c.sdk.GlobalData.Search(ctx, operations.V3GlobaldataSearchQueryRequest{
		SearchQueryInputBody: body,
	})
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	result := resp.GetResponseEnvelopeSearchQueryResponse().GetResult()
	if result == nil {
		return nil, errors.New("search: response carried no result")
	}
	return &SearchPage{
		Hits:          result.GetHits(),
		TotalHits:     result.GetTotalHits(),
		NextPageToken: result.GetNextPageToken(),
	}, nil
}

// SearchEach walks result pages, calling fn for each one. It stops after
// maxPages (0 means every page), when a page reports no continuation token, or
// as soon as fn returns an error, which is passed through to the caller.
//
// Each request gets the client's per-request timeout; ctx bounds the walk as a
// whole, so cancelling it stops the iteration between pages.
func (c *Client) SearchEach(ctx context.Context, p SearchParams, maxPages int, fn func(SearchPage) error) error {
	token := ""
	for page := 1; maxPages == 0 || page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := c.Search(ctx, p, token)
		if err != nil {
			return err
		}
		if err := fn(*result); err != nil {
			return err
		}
		if result.NextPageToken == "" {
			return nil
		}
		token = result.NextPageToken
	}
	return nil
}

// Aggregate buckets a query by field, for quick distribution analysis.
func (c *Client) Aggregate(ctx context.Context, query, field string, buckets int64) (*components.SearchAggregateResponse, error) {
	if query == "" {
		return nil, ErrEmptyQuery
	}
	if field == "" {
		return nil, errors.New("aggregation field cannot be empty")
	}

	resp, err := c.sdk.GlobalData.Aggregate(ctx, operations.V3GlobaldataSearchAggregateRequest{
		SearchAggregateInputBody: components.SearchAggregateInputBody{
			Query:           query,
			Field:           field,
			NumberOfBuckets: buckets,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate %s: %w", field, err)
	}

	result := resp.GetResponseEnvelopeSearchAggregateResponse().GetResult()
	if result == nil {
		return nil, errors.New("aggregate: response carried no result")
	}
	return result, nil
}

func (c *Client) pageSizeOr(override int) int {
	if override > 0 {
		return override
	}
	return c.pageSize
}

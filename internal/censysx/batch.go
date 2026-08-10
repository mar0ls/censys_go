package censysx

import (
	"context"
	"fmt"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
	"github.com/censys/censys-sdk-go/models/operations"
)

// MaxBatchSize is the largest host batch sent in one request.
const MaxBatchSize = 100

// HostsBatch fetches up to MaxBatchSize hosts in a single request. Prefer
// HostsEach, which chunks a longer list for you.
func (c *Client) HostsBatch(ctx context.Context, ids []string, at *time.Time) ([]components.HostAsset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > MaxBatchSize {
		return nil, fmt.Errorf("batch of %d exceeds the %d-host limit", len(ids), MaxBatchSize)
	}

	body := components.AssetHostListInputBody{HostIds: ids}
	if at != nil {
		utc := at.UTC()
		body.AtTime = &utc
	}

	resp, err := c.sdk.GlobalData.GetHosts(ctx, operations.V3GlobaldataAssetHostListPostRequest{
		AssetHostListInputBody: body,
	})
	if err != nil {
		return nil, fmt.Errorf("host batch: %w", err)
	}
	return resp.GetResponseEnvelopeListHostAsset().GetResult(), nil
}

// HostsEach fetches every host in ids, chunked into batches, calling fn for each
// host returned.
//
// The batch endpoint is one request per chunk rather than one per host, which
// is the difference between a handful of round trips and several hundred when
// sweeping a result set. Hosts Censys has no record of are simply absent from
// the response; missing reports how many were dropped that way.
func (c *Client) HostsEach(ctx context.Context, ids []string, at *time.Time, fn func(components.Host) error) (found, missing int, err error) {
	for start := 0; start < len(ids); start += MaxBatchSize {
		if err := ctx.Err(); err != nil {
			return found, missing, err
		}

		end := min(start+MaxBatchSize, len(ids))
		chunk := ids[start:end]

		assets, err := c.HostsBatch(ctx, chunk, at)
		if err != nil {
			return found, missing, err
		}

		for i := range assets {
			if err := fn(assets[i].Resource); err != nil {
				return found, missing, err
			}
		}
		found += len(assets)
		missing += len(chunk) - len(assets)
	}
	return found, missing, nil
}

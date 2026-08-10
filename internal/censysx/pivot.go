package censysx

import (
	"context"
	"errors"
	"fmt"
	"time"

	censys "github.com/censys/censys-sdk-go"
	"github.com/censys/censys-sdk-go/models/components"
	"github.com/censys/censys-sdk-go/models/operations"
)

// maxObservationPageSize is the cap the observations endpoint documents.
const maxObservationPageSize = 100

// CertObservationParams narrows a certificate-observation lookup.
type CertObservationParams struct {
	// Fingerprint is the certificate's SHA-256 digest.
	Fingerprint string

	// Start and End bound the observation window. Zero means unbounded.
	Start time.Time
	End   time.Time

	// Port and Protocol filter the observations. Zero and empty mean any.
	Port     int
	Protocol string

	PageSize int
}

// CertObservations reports every host seen serving a given certificate, as
// time-bounded ranges.
//
// This is the pivot that turns a single sample into a campaign: take the
// certificate off one panel and the endpoint returns the rest of the fleet,
// including hosts that have since gone dark and so no longer appear in a live
// search.
func (c *Client) CertObservations(ctx context.Context, p CertObservationParams, fn func(components.HostObservationRange) error) (int64, error) {
	fingerprint, err := NormalizeFingerprint(p.Fingerprint)
	if err != nil {
		return 0, err
	}

	pageSize := p.PageSize
	if pageSize <= 0 || pageSize > maxObservationPageSize {
		pageSize = maxObservationPageSize
	}

	req := operations.V3ThreathuntingGetHostObservationsWithCertificateRequest{
		CertificateID: fingerprint,
		PageSize:      censys.Pointer(pageSize),
	}
	if !p.Start.IsZero() {
		req.StartTime = censys.Pointer(p.Start.UTC().Format(time.RFC3339))
	}
	if !p.End.IsZero() {
		req.EndTime = censys.Pointer(p.End.UTC().Format(time.RFC3339))
	}
	if p.Port > 0 {
		req.Port = censys.Pointer(p.Port)
	}
	if p.Protocol != "" {
		req.Protocol = censys.Pointer(p.Protocol)
	}

	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		resp, err := c.sdk.ThreatHunting.GetHostObservationsWithCertificate(ctx, req)
		if err != nil {
			return total, fmt.Errorf("observations for %s: %w", fingerprint, err)
		}

		result := resp.GetResponseEnvelopeHostObservationResponse().GetResult()
		if result == nil {
			return total, errors.New("observations: response carried no result")
		}
		total = result.GetTotalResults()

		for _, r := range result.GetRanges() {
			if err := fn(r); err != nil {
				return total, err
			}
		}

		next := result.GetNextPageToken()
		if next == nil || *next == "" {
			return total, nil
		}
		req.PageToken = next
	}
}

// Timeline returns a host's service and certificate change history between two
// instants.
//
// The API names its window fields from the operator's point of view rather than
// chronologically: StartTime is the end of the window nearest to now and
// EndTime the one furthest away. This wrapper takes the range the ordinary way
// round and swaps it.
func (c *Client) Timeline(ctx context.Context, hostID string, from, to time.Time) (*components.HostTimeline, error) {
	if hostID == "" {
		return nil, errors.New("host ID cannot be empty")
	}
	if from.After(to) {
		return nil, fmt.Errorf("timeline window starts after it ends: %s to %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}

	resp, err := c.sdk.GlobalData.GetHostTimeline(ctx, operations.V3GlobaldataAssetHostTimelineRequest{
		HostID:    hostID,
		StartTime: to.UTC(),
		EndTime:   from.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("timeline for %s: %w", hostID, err)
	}

	result := resp.GetResponseEnvelopeHostTimeline().GetResult()
	if result == nil {
		return nil, fmt.Errorf("timeline for %s: response carried no result", hostID)
	}
	return result, nil
}

package censysx

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
	"github.com/censys/censys-sdk-go/models/operations"
)

// Host fetches one host. at selects a historical snapshot; nil means current.
func (c *Client) Host(ctx context.Context, id string, at *time.Time) (*components.Host, error) {
	if id == "" {
		return nil, errors.New("host ID cannot be empty")
	}

	resp, err := c.sdk.GlobalData.GetHost(ctx, operations.V3GlobaldataAssetHostRequest{
		HostID: id,
		AtTime: at,
	})
	if err != nil {
		return nil, fmt.Errorf("host %s: %w", id, err)
	}

	asset := resp.GetResponseEnvelopeHostAsset().GetResult()
	if asset == nil {
		return nil, fmt.Errorf("host %s: response carried no result", id)
	}
	return &asset.Resource, nil
}

// Certificate fetches one certificate by its SHA-256 fingerprint.
func (c *Client) Certificate(ctx context.Context, fingerprint string) (*components.Certificate, error) {
	fingerprint, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}

	resp, err := c.sdk.GlobalData.GetCertificate(ctx, operations.V3GlobaldataAssetCertificateRequest{
		CertificateID: fingerprint,
	})
	if err != nil {
		return nil, fmt.Errorf("certificate %s: %w", fingerprint, err)
	}

	asset := resp.GetResponseEnvelopeCertificateAsset().GetResult()
	if asset == nil {
		return nil, fmt.Errorf("certificate %s: response carried no result", fingerprint)
	}
	return &asset.Resource, nil
}

// NormalizeFingerprint lower-cases and validates a SHA-256 certificate
// fingerprint, rejecting anything that is not 64 hex characters.
func NormalizeFingerprint(fp string) (string, error) {
	fp = strings.ToLower(strings.TrimSpace(fp))
	if fp == "" {
		return "", errors.New("fingerprint cannot be empty")
	}
	if len(fp) != hex.EncodedLen(32) {
		return "", fmt.Errorf("SHA-256 fingerprint must be %d hex characters, got %d", hex.EncodedLen(32), len(fp))
	}
	if _, err := hex.DecodeString(fp); err != nil {
		return "", fmt.Errorf("fingerprint is not valid hex: %w", err)
	}
	return fp, nil
}

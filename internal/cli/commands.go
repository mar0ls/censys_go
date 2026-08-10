package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/config"
	"github.com/mar0ls/censys_go/internal/hunt"
	"github.com/mar0ls/censys_go/internal/ui"
)

// commands returns the subcommand table.
func commands() []command {
	var (
		query     string
		pages     int
		pageSize  int
		allFields bool
		field     string
		buckets   int
		inputFile string
		atTime    string
		days      int
	)

	return []command{
		{
			name:    "search",
			args:    "-q QUERY [--pages N]",
			summary: "run a CenQL query and stream matching hosts",
			register: func(fs *flag.FlagSet) {
				fs.StringVar(&query, "q", "", "CenQL query (required)")
				fs.IntVar(&pages, "pages", 1, "pages to fetch, 0 for every page")
				fs.IntVar(&pageSize, "page-size", 0, "results per page, 0 for the client default")
				fs.BoolVar(&allFields, "all-fields", false, "return complete records instead of the default field subset")
			},
			run: func(ctx context.Context, s *session, args []string) error {
				if query == "" && len(args) > 0 {
					query = strings.Join(args, " ")
				}
				if query == "" {
					return fmt.Errorf("%w: search needs a query (-q)", ErrUsage)
				}
				if pages < 0 {
					return fmt.Errorf("%w: --pages cannot be negative", ErrUsage)
				}
				return runSearch(ctx, s, censysx.SearchParams{
					Query:     query,
					PageSize:  pageSize,
					AllFields: allFields,
				}, pages)
			},
		},
		{
			name:    "host",
			args:    "[IP|CIDR ...] [-f FILE]",
			summary: "fetch hosts by address; reads stdin when given neither arguments nor a file",
			register: func(fs *flag.FlagSet) {
				fs.StringVar(&inputFile, "f", "", "read targets from a file, one per line")
				fs.StringVar(&atTime, "at", "", "snapshot time as RFC3339, for a historical view")
			},
			run: func(ctx context.Context, s *session, args []string) error {
				targets, err := collectTargets(s, args, inputFile)
				if err != nil {
					return err
				}
				at, err := parseAtTime(atTime)
				if err != nil {
					return err
				}
				return runHosts(ctx, s, targets, at)
			},
		},
		{
			name:    "cert",
			args:    "SHA256",
			summary: "look up a certificate by its SHA-256 fingerprint",
			run: func(ctx context.Context, s *session, args []string) error {
				if len(args) != 1 {
					return fmt.Errorf("%w: cert takes exactly one fingerprint", ErrUsage)
				}
				cert, err := s.client.Certificate(ctx, args[0])
				if err != nil {
					return err
				}
				stream := s.stream()
				if err := stream.Value(cert); err != nil {
					_ = stream.Close()
					return err
				}
				return stream.Close()
			},
		},
		{
			name:    "aggregate",
			args:    "-q QUERY [--field F] [--buckets N]",
			summary: "bucket a query by field to see how a population is distributed",
			register: func(fs *flag.FlagSet) {
				fs.StringVar(&query, "q", "", "CenQL query (required)")
				fs.StringVar(&field, "field", "services.port", "field to aggregate on")
				fs.IntVar(&buckets, "buckets", 20, "number of buckets to return")
			},
			run: func(ctx context.Context, s *session, _ []string) error {
				if query == "" {
					return fmt.Errorf("%w: aggregate needs a query (-q)", ErrUsage)
				}
				if buckets <= 0 {
					return fmt.Errorf("%w: --buckets must be positive", ErrUsage)
				}

				result, err := s.client.Aggregate(ctx, query, field, int64(buckets))
				if err != nil {
					return err
				}

				stream := s.stream()
				for _, b := range result.GetBuckets() {
					if err := stream.Value(map[string]any{"key": b.Key, "count": b.Count}); err != nil {
						_ = stream.Close()
						return err
					}
				}
				if err := stream.Close(); err != nil {
					return err
				}
				s.okf("%d buckets over %d matches", len(result.GetBuckets()), result.GetTotalCount())
				return nil
			},
		},
		{
			name:    "credits",
			args:    "[--days N]",
			summary: "report the credit balance and recent usage",
			register: func(fs *flag.FlagSet) {
				fs.IntVar(&days, "days", 30, "usage window in days")
			},
			run: func(ctx context.Context, s *session, _ []string) error {
				credits, err := s.client.Credits(ctx)
				if err != nil {
					return err
				}
				payload := map[string]any{"balance": credits.GetBalance()}
				if exp := censysx.NextExpiry(credits); exp != nil {
					payload["expires_at"] = exp.ExpiresAt.Format(time.RFC3339)
					payload["expiring_balance"] = exp.Balance
				}

				if usage, err := s.client.CreditUsage(ctx, days); err != nil {
					s.warnf("usage report unavailable: %s", censysx.Explain(err))
				} else {
					payload["window_days"] = days
					payload["consumed"] = usage.TotalConsumed
					payload["transactions"] = usage.TransactionCount
					payload["consumed_api"] = usage.CreditsConsumedBySource.API
					payload["consumed_ui"] = usage.CreditsConsumedBySource.UI
				}

				stream := s.stream()
				if err := stream.Value(payload); err != nil {
					_ = stream.Close()
					return err
				}
				return stream.Close()
			},
		},
		{
			name:    "ui",
			args:    "",
			summary: "start the interactive menu",
			run: func(ctx context.Context, s *session, _ []string) error {
				return ui.New(ui.Options{
					Client: s.client,
					Format: s.format,
					In:     s.env.In,
					Out:    s.out,
					Msg:    s.env.Msg,
					NewClient: func(c config.Credentials) *censysx.Client {
						return censysx.New(censysx.Options{OrgID: c.OrgID, Token: c.Token})
					},
				}).Run(ctx)
			},
		},
	}
}

// runSearch streams every page of a query through the session's renderer.
func runSearch(ctx context.Context, s *session, params censysx.SearchParams, pages int) error {
	stream := s.stream()
	count := 0

	err := s.client.SearchEach(ctx, params, pages, func(page censysx.SearchPage) error {
		for _, rec := range censysx.HostRecordsFromHits(page.Hits) {
			if err := stream.Host(rec); err != nil {
				return err
			}
			count++
		}
		s.infof("%d hosts written (%.0f match the query)", count, page.TotalHits)
		return nil
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	// An interrupt mid-walk is not a failure: whatever was written is valid.
	if errors.Is(err, context.Canceled) {
		s.warnf("interrupted after %d hosts", count)
		return nil
	}
	if err != nil {
		return err
	}

	if count == 0 {
		s.warnf("no results")
		return nil
	}
	s.okf("%d hosts written", count)
	return nil
}

// runHosts fetches each target in turn, reporting failures without abandoning
// the rest of the list.
func runHosts(ctx context.Context, s *session, targets []string, at *time.Time) error {
	s.infof("%d hosts to fetch, 1 credit each", len(targets))

	stream := s.stream()
	fetched, failed := 0, 0

	for _, target := range targets {
		if ctx.Err() != nil {
			s.warnf("interrupted after %d hosts", fetched)
			break
		}

		host, err := s.client.Host(ctx, target, at)
		if err != nil {
			failed++
			if censysx.IsNotFound(err) {
				s.warnf("%s: not present in Censys", target)
			} else {
				s.warnf("%s: %s", target, censysx.Explain(err))
			}
			// Bad credentials will not fix themselves on the next target.
			if censysx.IsAuth(err) {
				_ = stream.Close()
				return err
			}
			continue
		}

		if err := stream.Host(censysx.NewHostRecord(host)); err != nil {
			_ = stream.Close()
			return err
		}
		fetched++
	}

	if err := stream.Close(); err != nil {
		return err
	}
	s.okf("%d hosts written, %d failed", fetched, failed)
	return nil
}

// collectTargets gathers targets from arguments, a file, or stdin, in that order.
func collectTargets(s *session, args []string, file string) ([]string, error) {
	var (
		targets []string
		errs    []error
	)
	switch {
	case len(args) > 0:
		targets, errs = hunt.ParseTargets(args)
	case file != "":
		targets, errs = hunt.ReadTargetsFile(file)
	default:
		targets, errs = hunt.ReadTargets(s.env.In)
	}

	for _, err := range errs {
		s.warnf("%v", err)
	}
	if len(targets) == 0 {
		return nil, errors.New("no usable targets")
	}
	return targets, nil
}

// parseAtTime accepts an RFC3339 timestamp or a bare date.
func parseAtTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, value); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%w: --at wants an RFC3339 timestamp or YYYY-MM-DD, got %q", ErrUsage, value)
}

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/censys/censys-sdk-go/models/components"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/config"
	"github.com/mar0ls/censys_go/internal/hunt"
	"github.com/mar0ls/censys_go/internal/render"
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
		since     string
		until     string
		port      int
		protocol  string
		noBatch   bool
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
				fs.BoolVar(&noBatch, "no-batch", false, "fetch one host per request instead of in batches")
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
				if noBatch {
					return runHostsOneByOne(ctx, s, targets, at)
				}
				return runHosts(ctx, s, targets, at)
			},
		},
		{
			name:    "cert-hosts",
			args:    "SHA256 [--since D] [--until D] [--port N]",
			summary: "list every host observed serving a certificate, including ones now dark",
			register: func(fs *flag.FlagSet) {
				fs.StringVar(&since, "since", "", "only ranges ending at or after this time")
				fs.StringVar(&until, "until", "", "only ranges starting at or before this time")
				fs.IntVar(&port, "port", 0, "restrict to one port")
				fs.StringVar(&protocol, "protocol", "", "restrict to one transport protocol")
				fs.IntVar(&pages, "pages", 0, "pages of 100 to fetch, 0 for every page; each costs 5 credits")
			},
			run: func(ctx context.Context, s *session, args []string) error {
				if len(args) != 1 {
					return fmt.Errorf("%w: cert-hosts takes exactly one fingerprint", ErrUsage)
				}
				start, err := parseAtTime(since)
				if err != nil {
					return err
				}
				end, err := parseAtTime(until)
				if err != nil {
					return err
				}
				if pages < 0 {
					return fmt.Errorf("%w: --pages cannot be negative", ErrUsage)
				}
				return runCertHosts(ctx, s, censysx.CertObservationParams{
					Fingerprint: args[0],
					Start:       valueOrZero(start),
					End:         valueOrZero(end),
					Port:        port,
					Protocol:    protocol,
					MaxPages:    pages,
				})
			},
		},
		{
			name:    "timeline",
			args:    "IP [--since D] [--until D]",
			summary: "show a host's service and certificate changes over a window",
			register: func(fs *flag.FlagSet) {
				fs.StringVar(&since, "since", "", "window start, RFC3339 or YYYY-MM-DD (default 30 days ago)")
				fs.StringVar(&until, "until", "", "window end, RFC3339 or YYYY-MM-DD (default now)")
			},
			run: func(ctx context.Context, s *session, args []string) error {
				if len(args) != 1 {
					return fmt.Errorf("%w: timeline takes exactly one host", ErrUsage)
				}
				from, err := parseAtTime(since)
				if err != nil {
					return err
				}
				to, err := parseAtTime(until)
				if err != nil {
					return err
				}
				now := time.Now()
				if to == nil {
					to = &now
				}
				if from == nil {
					start := to.AddDate(0, 0, -30)
					from = &start
				}

				timeline, err := s.client.Timeline(ctx, args[0], *from, *to)
				if err != nil {
					return err
				}

				stream := s.stream()
				for _, event := range timeline.Events {
					if err := stream.Value(event); err != nil {
						_ = stream.Close()
						return err
					}
				}
				if err := stream.Close(); err != nil {
					return err
				}
				s.okf("%d events between %s and %s", len(timeline.Events),
					from.Format(time.RFC3339), to.Format(time.RFC3339))
				return nil
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
				fs.StringVar(&field, "field", "host.services.port", "field to aggregate on")
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
					if err := stream.Record(render.Bucket(field, b)); err != nil {
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
	// Whatever was written before the interrupt is valid and already flushed,
	// but the run did not complete, so the status still has to say so.
	if Interrupted(ctx, err) {
		s.warnf("interrupted after %d hosts; results so far are written", count)
		return ctx.Err()
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

// runHosts fetches targets through the batch endpoint, which costs one request
// per hundred hosts instead of one per host.
func runHosts(ctx context.Context, s *session, targets []string, at *time.Time) error {
	batches := (len(targets) + censysx.MaxBatchSize - 1) / censysx.MaxBatchSize
	s.infof("%d hosts to fetch in %d request(s), 1 credit each", len(targets), batches)

	stream := s.stream()
	found, missing, err := s.client.HostsEach(ctx, targets, at, func(host components.Host) error {
		return stream.Host(censysx.NewHostRecord(&host))
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	if Interrupted(ctx, err) {
		s.warnf("interrupted after %d hosts; results so far are written", found)
		return ctx.Err()
	}
	if err != nil {
		return err
	}

	s.okf("%d hosts written, %d not present in Censys", found, missing)
	return nil
}

// runHostsOneByOne fetches each target separately. It costs the same in credits
// but many more round trips; its value is that a failure names the host that
// caused it, which the batch endpoint cannot do.
func runHostsOneByOne(ctx context.Context, s *session, targets []string, at *time.Time) error {
	s.infof("%d hosts to fetch, one request each, 1 credit each", len(targets))

	stream := s.stream()
	fetched, failed := 0, 0
	interrupted := false

	for _, target := range targets {
		if ctx.Err() != nil {
			interrupted = true
			break
		}

		host, err := s.client.Host(ctx, target, at)
		if err != nil {
			// A request cut short by the interrupt is not a failure of that host.
			if Interrupted(ctx, err) {
				s.warnf("interrupted after %d hosts; results so far are written", fetched)
				interrupted = true
				break
			}
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
	if interrupted {
		return ctx.Err()
	}
	return nil
}

// runCertHosts streams the observation ranges for a certificate.
func runCertHosts(ctx context.Context, s *session, p censysx.CertObservationParams) error {
	if p.MaxPages > 0 {
		s.infof("up to %d page(s) of 100, %d credits each",
			p.MaxPages, censysx.ObservationCreditsPerPage)
	} else {
		s.infof("every page of 100, %d credits each; cap it with --pages",
			censysx.ObservationCreditsPerPage)
	}

	stream := s.stream()
	seen := map[string]struct{}{}

	total, pages, err := s.client.CertObservations(ctx, p, func(r components.HostObservationRange) error {
		seen[r.IP] = struct{}{}
		return stream.Record(render.Observation(r))
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	if Interrupted(ctx, err) {
		s.warnf("interrupted after %d hosts; results so far are written", len(seen))
		return ctx.Err()
	}
	if err != nil {
		return err
	}

	s.okf("%d observation ranges across %d unique hosts (%d reported); %d page(s), about %d credits",
		stream.Count(), len(seen), total, pages, pages*censysx.ObservationCreditsPerPage)
	if p.MaxPages > 0 && pages == p.MaxPages && int64(stream.Count()) < total {
		s.warnf("stopped at the --pages limit; %d ranges were not fetched", total-int64(stream.Count()))
	}
	return nil
}

// valueOrZero dereferences an optional time, yielding the zero value for nil.
func valueOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
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

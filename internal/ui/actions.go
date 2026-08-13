package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/censys/censys-sdk-go/models/components"
	"github.com/manifoldco/promptui"
	"github.com/schollz/progressbar/v3"

	"github.com/mar0ls/censys_go/internal/censysx"
	"github.com/mar0ls/censys_go/internal/config"
	"github.com/mar0ls/censys_go/internal/hunt"
	"github.com/mar0ls/censys_go/internal/render"
)

// usageWindowDays is the reporting window for the credit usage summary.
const usageWindowDays = 30

// CenQL fields used to pivot from one host's artifacts to everything else
// presenting the same artifact. Platform queries require the dataset prefix:
// a bare "services.port" matches nothing.
const (
	certFingerprintField = "host.services.cert.fingerprint_sha256"
	jarmFingerprintField = "host.services.jarm.fingerprint"
)

// exampleQueries are shown before the query prompt as a reminder of CenQL syntax.
var exampleQueries = []string{
	`host.services.port=443`,
	`host.services.port=22 and host.location.country="Poland"`,
	`host.services.software.product:"Apache"`,
	`host.services.cert.fingerprint_sha256="<sha256>"`,
	`host.services.jarm.fingerprint="07d14d16d21d21d07c42d41d00041d24"`,
}

func (u *UI) credits(ctx context.Context) error {
	balance, err := u.client.Balance(ctx)
	if err != nil {
		return err
	}

	u.printf("\n=== Credits (%s) ===\n", balance.Scope)
	u.printf("  Balance : %d\n", balance.Credits)
	if balance.Renews != nil {
		u.printf("  Renews  : %s\n", balance.Renews.Format(time.DateOnly))
	}
	if balance.Expires != nil {
		u.printf("  Expires : %s\n", balance.Expires.Format(time.DateOnly))
	}

	usage, err := u.client.CreditUsage(ctx, usageWindowDays)
	if err != nil {
		u.warnf("usage report unavailable: %s", censysx.Explain(err))
		return nil
	}

	u.printf("\n=== Usage (last %d days) ===\n", usageWindowDays)
	u.printf("  Consumed     : %d credits over %d transactions\n", usage.TotalConsumed, usage.TransactionCount)
	u.printf("  API / UI     : %d / %d\n", usage.CreditsConsumedBySource.API, usage.CreditsConsumedBySource.UI)
	for _, period := range usage.Periods {
		if period.CreditsConsumed == 0 {
			continue
		}
		u.printf("    %s  %d credits (%d tx)\n",
			period.StartDate.Format(time.DateOnly), period.CreditsConsumed, period.TransactionCount)
	}
	return nil
}

func (u *UI) search(ctx context.Context) error {
	u.infof("Example queries:")
	for _, q := range exampleQueries {
		u.printf("    %s\n", q)
	}

	query, err := u.askRequired("Censys query: ")
	if err != nil {
		return err
	}
	maxPages, err := u.askInt("Pages to fetch (0 = all)", 1)
	if err != nil {
		// 0 is a legitimate answer here even though askInt rejects it elsewhere.
		if !strings.Contains(err.Error(), "must be positive") {
			return err
		}
		maxPages = 0
	}

	stream := u.stream()
	var records []censysx.HostRecord

	err = u.client.SearchEach(ctx, censysx.SearchParams{Query: query}, maxPages, func(page censysx.SearchPage) error {
		hosts := censysx.HostRecordsFromHits(page.Hits)
		records = append(records, hosts...)
		for _, rec := range hosts {
			if err := stream.Host(rec); err != nil {
				return err
			}
		}
		u.infof("%d hits so far (%.0f match the query)", len(records), page.TotalHits)
		return nil
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	if len(records) == 0 {
		u.warnf("no results")
		return nil
	}
	u.okf("%d hosts written", len(records))

	return u.offerPivots(ctx, records)
}

// offerPivots turns a result set into the next query. Extracting IPs, certificate
// hashes and JARM fingerprints by hand is the tedious part of chasing
// infrastructure, so the tool offers the follow-up directly.
func (u *UI) offerPivots(ctx context.Context, records []censysx.HostRecord) error {
	var ips, certs, jarms []string
	seenCert, seenJARM := map[string]bool{}, map[string]bool{}
	for _, rec := range records {
		if rec.IP != "" {
			ips = append(ips, rec.IP)
		}
		for _, h := range rec.CertHashes() {
			if !seenCert[h] {
				seenCert[h] = true
				certs = append(certs, h)
			}
		}
		for _, j := range rec.JARMHashes() {
			if !seenJARM[j] {
				seenJARM[j] = true
				jarms = append(jarms, j)
			}
		}
	}

	u.infof("Pivot material: %d unique IPs, %d certificates, %d JARM fingerprints", len(ips), len(certs), len(jarms))

	options := []string{"Nothing, return to menu"}
	if len(ips) > 0 {
		options = append(options, fmt.Sprintf("Bulk view %d hosts (%d credits)", len(ips), len(ips)))
	}
	if len(certs) > 0 {
		options = append(options, "Search other hosts sharing a certificate")
	}
	if len(jarms) > 0 {
		options = append(options, "Search other hosts sharing a JARM fingerprint")
	}
	if len(options) == 1 {
		return nil
	}

	prompt := promptui.Select{Label: "Pivot", Items: options, Stdout: nopCloser{u.msg}, Size: len(options)}
	idx, _, err := prompt.Run()
	if err != nil || idx == 0 {
		return nil
	}

	switch choice := options[idx]; {
	case strings.HasPrefix(choice, "Bulk view"):
		return u.bulkFetch(ctx, ips)
	case strings.Contains(choice, "certificate"):
		return u.pivotQuery(ctx, certFingerprintField, certs)
	case strings.Contains(choice, "JARM"):
		return u.pivotQuery(ctx, jarmFingerprintField, jarms)
	}
	return nil
}

// pivotQuery lets the operator pick one fingerprint and runs the matching query.
func (u *UI) pivotQuery(ctx context.Context, field string, values []string) error {
	prompt := promptui.Select{Label: field, Items: values, Stdout: nopCloser{u.msg}, Size: 10}
	idx, _, err := prompt.Run()
	if err != nil {
		return nil
	}

	// "=" rather than ":": a fingerprint has to match exactly, and ":" is a
	// case-insensitive tokenized match that would pull in unrelated hosts.
	query := fmt.Sprintf("%s=%q", field, values[idx])
	u.infof("Running: %s", query)

	stream := u.stream()
	count := 0
	err = u.client.SearchEach(ctx, censysx.SearchParams{Query: query}, 1, func(page censysx.SearchPage) error {
		for _, rec := range censysx.HostRecordsFromHits(page.Hits) {
			count++
			if err := stream.Host(rec); err != nil {
				return err
			}
		}
		return nil
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	u.okf("%d hosts written", count)
	return nil
}

func (u *UI) host(ctx context.Context) error {
	id, err := u.askRequired("IP address: ")
	if err != nil {
		return err
	}

	host, err := u.client.Host(ctx, id, nil)
	if err != nil {
		return err
	}

	stream := u.stream()
	if err := stream.Host(censysx.NewHostRecord(host)); err != nil {
		return err
	}
	return stream.Close()
}

func (u *UI) bulk(ctx context.Context) error {
	source := promptui.Select{
		Label:  "Input",
		Items:  []string{"Type addresses (one per line, blank line ends)", "Read a text file"},
		Stdout: nopCloser{u.msg},
	}
	idx, _, err := source.Run()
	if err != nil {
		return nil
	}

	var (
		targets []string
		errs    []error
	)
	if idx == 0 {
		u.infof("Enter IP addresses or CIDR prefixes, one per line; blank line to finish:")
		var entries []string
		for {
			line, readErr := u.ask("")
			if readErr != nil || line == "" {
				break
			}
			entries = append(entries, line)
		}
		targets, errs = hunt.ParseTargets(entries)
	} else {
		path, askErr := u.askDefault("Path to file", "ips.txt")
		if askErr != nil {
			return askErr
		}
		targets, errs = hunt.ReadTargetsFile(path)
	}

	for _, e := range errs {
		u.warnf("%v", e)
	}
	if len(targets) == 0 {
		return errors.New("no usable targets")
	}

	return u.bulkFetch(ctx, targets)
}

// bulkFetch retrieves hosts through the batch endpoint, showing a progress bar
// on the message stream. The operator confirms first because every host costs a
// credit whether or not Censys has a record for it.
func (u *UI) bulkFetch(ctx context.Context, targets []string) error {
	batches := (len(targets) + censysx.MaxBatchSize - 1) / censysx.MaxBatchSize
	u.infof("%d hosts to fetch in %d request(s), 1 credit each", len(targets), batches)

	proceed, err := u.confirm("Continue?", false)
	if err != nil || !proceed {
		u.infof("cancelled")
		return err
	}

	bar := progressbar.NewOptions(len(targets),
		progressbar.OptionSetWriter(u.msg),
		progressbar.OptionSetDescription("Fetching"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionThrottle(100*time.Millisecond),
	)

	stream := u.stream()
	passive := 0
	found, missing, err := u.client.HostsEach(ctx, targets, nil, func(host components.Host) error {
		_ = bar.Add(1)
		rec := censysx.NewHostRecord(&host)
		if !rec.Scanned() {
			passive++
		}
		return stream.Host(rec)
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	u.printf("\n")

	if err != nil && ctx.Err() != nil {
		u.warnf("interrupted after %d hosts; what was fetched is already written", found)
		return ctx.Err()
	}
	if err != nil {
		return err
	}

	u.okf("%d hosts written, %d not present in Censys", found, missing)
	if passive > 0 {
		u.warnf("%d of %d have no scanned services", passive, found)
	}
	return nil
}

// certHosts pivots from a certificate to every host observed serving it.
func (u *UI) certHosts(ctx context.Context) error {
	fingerprint, err := u.askRequired("Certificate SHA-256: ")
	if err != nil {
		return err
	}
	window, err := u.askInt("Look back how many days", 90)
	if err != nil {
		return err
	}
	maxPages, err := u.askInt(
		fmt.Sprintf("Pages of 100 to fetch (%d credits each)", censysx.ObservationCreditsPerPage), 3)
	if err != nil {
		return err
	}

	stream := u.stream()
	seen := map[string]struct{}{}

	total, pages, err := u.client.CertObservations(ctx, censysx.CertObservationParams{
		Fingerprint: fingerprint,
		Start:       time.Now().AddDate(0, 0, -window),
		MaxPages:    maxPages,
	}, func(r components.HostObservationRange) error {
		seen[r.IP] = struct{}{}
		return stream.Record(render.Observation(r))
	})
	if closeErr := stream.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	u.okf("%d ranges across %d unique hosts (%d reported); %d page(s), about %d credits",
		stream.Count(), len(seen), total, pages, pages*censysx.ObservationCreditsPerPage)
	if len(seen) > 0 {
		fetch, err := u.confirm(fmt.Sprintf("Fetch full records for those %d hosts?", len(seen)), false)
		if err != nil || !fetch {
			return err
		}
		ips := make([]string, 0, len(seen))
		for ip := range seen {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		return u.bulkFetch(ctx, ips)
	}
	return nil
}

// timeline shows how a host's exposed services changed over a window.
func (u *UI) timeline(ctx context.Context) error {
	host, err := u.askRequired("IP address: ")
	if err != nil {
		return err
	}
	window, err := u.askInt("Look back how many days", 30)
	if err != nil {
		return err
	}

	to := time.Now()
	result, err := u.client.Timeline(ctx, host, to.AddDate(0, 0, -window), to)
	if err != nil {
		return err
	}

	stream := u.stream()
	for _, event := range result.Events {
		if err := stream.Value(event); err != nil {
			_ = stream.Close()
			return err
		}
	}
	if err := stream.Close(); err != nil {
		return err
	}

	u.okf("%d events in the last %d days (scanned to %s)",
		len(result.Events), window, result.ScannedTo.Format(time.RFC3339))
	return nil
}

func (u *UI) aggregate(ctx context.Context) error {
	query, err := u.askRequired("Censys query: ")
	if err != nil {
		return err
	}
	field, err := u.askDefault("Field to aggregate", "host.services.port")
	if err != nil {
		return err
	}
	buckets, err := u.askInt("Number of buckets", 20)
	if err != nil {
		return err
	}

	result, err := u.client.Aggregate(ctx, query, field, int64(buckets))
	if err != nil {
		return err
	}

	stream := u.stream()
	for _, bucket := range result.GetBuckets() {
		if err := stream.Record(render.Bucket(field, bucket)); err != nil {
			_ = stream.Close()
			return err
		}
	}
	if err := stream.Close(); err != nil {
		return err
	}

	u.okf("%d buckets over %d matches", len(result.GetBuckets()), result.GetTotalCount())
	if result.GetOtherCount() > 0 {
		u.infof("%d matches fell outside the returned buckets", result.GetOtherCount())
	}
	return nil
}

func (u *UI) certificate(ctx context.Context) error {
	fingerprint, err := u.askRequired("SHA-256 fingerprint (64 hex characters): ")
	if err != nil {
		return err
	}

	cert, err := u.client.Certificate(ctx, fingerprint)
	if err != nil {
		return err
	}

	stream := u.stream()
	if err := stream.Record(render.Certificate(cert)); err != nil {
		_ = stream.Close()
		return err
	}
	return stream.Close()
}

func (u *UI) chooseFormat(context.Context) error {
	items := make([]string, len(render.Formats))
	for i, f := range render.Formats {
		items[i] = string(f)
	}

	prompt := promptui.Select{Label: "Output format", Items: items, Stdout: nopCloser{u.msg}}
	idx, _, err := prompt.Run()
	if err != nil {
		return nil
	}
	u.format = render.Formats[idx]
	u.okf("output format is now %s", u.format)
	return nil
}

func (u *UI) configure(context.Context) error {
	orgPrompt := promptui.Prompt{Label: "Organization ID", Stdout: nopCloser{u.msg}}
	orgID, err := orgPrompt.Run()
	if err != nil {
		return nil
	}
	tokenPrompt := promptui.Prompt{Label: "Bearer Token", Mask: '*', Stdout: nopCloser{u.msg}}
	token, err := tokenPrompt.Run()
	if err != nil {
		return nil
	}

	creds := config.Credentials{OrgID: strings.TrimSpace(orgID), Token: strings.TrimSpace(token)}
	if err := creds.Validate(); err != nil {
		return err
	}

	persist, err := u.confirm(fmt.Sprintf("Save to %s?", credentialPath()), true)
	if err != nil {
		return err
	}
	if persist {
		if err := config.Save(creds); err != nil {
			return err
		}
		u.okf("credentials saved")
	}

	u.creds = creds
	if u.newClient != nil {
		u.client = u.newClient(creds)
	}
	return nil
}

// credentialPath renders the config path for prompts, degrading to the bare
// filename if the home directory cannot be resolved.
func credentialPath() string {
	path, err := config.Path()
	if err != nil {
		return config.FileName
	}
	return path
}

// Censys-Go CLI: interactive command-line client for the Censys API.
// PoC how can you use censys-go-sdk from https://github.com/censys/censys-sdk-go (license: MIT)
// Provides interactive search, host and certificate lookup, bulk operations
// and JSON export of results.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/schollz/progressbar/v3"

	censys "github.com/censys/censys-sdk-go"
	"github.com/censys/censys-sdk-go/models/components"
	"github.com/censys/censys-sdk-go/models/operations"
	"github.com/censys/censys-sdk-go/models/sdkerrors"
	sdktypes "github.com/censys/censys-sdk-go/types"
)

// Config holds configuration required to connect to the Censys API.
type Config struct {
	OrgID string `json:"org_id"`
	Token string `json:"token"`
}

// AppConfig contains runtime configuration for the CLI.
type AppConfig struct {
	PageSize       int           // Page size for search requests
	MaxRetries     int           // Maximum number of retry attempts for API calls
	RequestTimeout time.Duration // Per-request timeout for API calls
	RetryDelay     time.Duration // Base delay between retries (multiplied by attempt number)
}

const (
	configDir = ".censys"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// Log-level style prefixes for consistent console output.
const (
	prefixOK   = "[OK]"
	prefixWarn = "[Warning]"
	prefixErr  = "[Error]"
	prefixInfo = "[**]"
)

var defaultAppConfig = AppConfig{
	PageSize:       50,
	MaxRetries:     3,
	RequestTimeout: 5 * time.Minute,
	RetryDelay:     2 * time.Second,
}

// defaultSearchFields is the default list of fields requested from Search.
// Keeping this list tight reduces response payload and speeds things up.
var defaultSearchFields = []string{
	"host.ip",
	"host.name",
	"host.location.country",
	"host.location.city",
	"host.autonomous_system.name",
	"host.autonomous_system.asn",
	"host.services.port",
	"host.services.service_name",
	"host.services.transport_protocol",
	"host.services.software.product",
	"host.services.software.version",
	"host.services.tls.certificates.leaf_data.subject.common_name",
	"host.last_updated_at",
}

func getHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home, nil
	}
	// Fallback for environments where UserHomeDir fails (e.g. some CI/CD setups)
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return home, nil
	}
	return "", errors.New("cannot determine the user's home directory")
}

// ensureResultsDir creates a "results" directory in the current working directory
// if it doesn't already exist. Files are stored with 0700 permissions.
func ensureResultsDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("unable to get current working directory: %w", err)
	}
	resultsPath := filepath.Join(dir, "results")
	if err := os.MkdirAll(resultsPath, 0700); err != nil {
		return "", fmt.Errorf("unable to create results directory: %w", err)
	}
	return resultsPath, nil
}

// saveJSON serializes data to indented JSON and writes it to the results directory.
func saveJSON(filename string, data interface{}) error {
	resultsDir, err := ensureResultsDir()
	if err != nil {
		return err
	}
	path := filepath.Join(resultsDir, filename)
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to serialize data to JSON: %w", err)
	}
	if err := os.WriteFile(path, jsonData, 0600); err != nil {
		return fmt.Errorf("unable to write file: %w", err)
	}
	fmt.Printf("%s Saved %s\n", prefixOK, path)
	return nil
}

// askToSave presents a summary of results and prompts the user to save or
// print them. Intentionally kept simple — no fancy TUI here.
func askToSave(data interface{}, prefix string) {
	printResultSummary(data, prefix)

	fmt.Printf("Save results to JSON? [Y/n]: ")
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading response: %v\n", prefixWarn, err)
		return
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "y" || answer == "yes" || answer == "" {
		timestamp := time.Now().Format("20060102_150405")
		filename := fmt.Sprintf("%s_%s.json", prefix, timestamp)
		if err := saveJSON(filename, data); err != nil {
			fmt.Printf("%s Error saving file: %v\n", prefixErr, err)
		}
		return
	}

	// User declined to save — offer alternative actions.
	menu := promptui.Select{
		Label: "What to do with the results?",
		Items: []string{
			"1. Print JSON to console",
			"2. Return to menu (cancel)",
		},
	}
	_, choice, err := menu.Run()
	if err != nil || choice[0] == '2' {
		fmt.Printf("%s Cancelled — returning to menu.\n", prefixInfo)
		return
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Printf("%s Error formatting JSON: %v\n", prefixErr, err)
		return
	}
	fmt.Println(string(jsonData))
}

// printResultSummary displays a brief summary of results before asking to save.
// For bulk view results (map[IP]data) it also lists the IP addresses.
func printResultSummary(data interface{}, prefix string) {
	fmt.Printf("\n%s === Results summary (%s) ===\n", prefixInfo, prefix)

	switch v := data.(type) {
	case []interface{}:
		fmt.Printf("%s Number of records: %d\n", prefixInfo, len(v))
	case map[string]interface{}:
		fmt.Printf("%s Number of records: %d\n", prefixInfo, len(v))
		if strings.Contains(prefix, "bulk") || strings.Contains(prefix, "view") {
			fmt.Printf("%s IP addresses:\n", prefixInfo)
			for ip := range v {
				fmt.Printf("    - %s\n", ip)
			}
		}
	default:
		fmt.Printf("%s Retrieved 1 record\n", prefixInfo)
	}
	fmt.Println()
}

func loadConfig() (Config, error) {
	homeDir, err := getHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("error getting home directory: %w", err)
	}
	configPath := filepath.Join(homeDir, configDir, "config.json")
	data, err := os.ReadFile(configPath) // #nosec G304 -- path is derived from the user's own home directory
	if err != nil {
		return Config{}, fmt.Errorf("unable to read configuration file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", configPath, err)
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	homeDir, err := getHomeDir()
	if err != nil {
		return fmt.Errorf("error getting home directory: %w", err)
	}
	configDirPath := filepath.Join(homeDir, configDir)
	if err := os.MkdirAll(configDirPath, 0700); err != nil {
		return fmt.Errorf("unable to create configuration directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to serialize configuration: %w", err)
	}
	configPath := filepath.Join(configDirPath, "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("unable to write configuration: %w", err)
	}
	return nil
}

func validateConfig(cfg Config) error {
	if cfg.OrgID == "" {
		return errors.New("organization ID cannot be empty")
	}
	if cfg.Token == "" {
		return errors.New("bearer token cannot be empty")
	}
	return nil
}

// ensureConfig tries to load an existing config. If that fails (first run,
// corrupted file, etc.) it falls back to prompting the user interactively.
func ensureConfig() Config {
	cfg, err := loadConfig()
	if err == nil {
		if validateErr := validateConfig(cfg); validateErr == nil {
			return cfg
		}
	}
	fmt.Printf("%s No configuration found – setting up now\n", prefixWarn)

	orgPrompt := promptui.Prompt{Label: "Organization ID"}
	orgID, err := orgPrompt.Run()
	if err != nil {
		log.Fatalf("%s Error obtaining Organization ID: %v", prefixErr, err)
	}

	tokenPrompt := promptui.Prompt{Label: "Bearer Token", Mask: '*'}
	token, err := tokenPrompt.Run()
	if err != nil {
		log.Fatalf("%s Error obtaining Bearer Token: %v", prefixErr, err)
	}

	cfg = Config{OrgID: strings.TrimSpace(orgID), Token: strings.TrimSpace(token)}
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("%s Invalid configuration: %v", prefixErr, err)
	}
	if err := saveConfig(cfg); err != nil {
		log.Fatalf("%s Unable to save configuration: %v", prefixErr, err)
	}
	fmt.Printf("%s Configuration saved!\n", prefixOK)
	return cfg
}

// interactiveConfig re-prompts for credentials and overwrites the saved config.
// Called from the main menu when the user wants to switch organizations/tokens.
func interactiveConfig() Config {
	orgPrompt := promptui.Prompt{Label: "Organization ID"}
	orgID, err := orgPrompt.Run()
	if err != nil {
		fmt.Printf("%s Error obtaining Organization ID: %v\n", prefixErr, err)
		return Config{}
	}

	tokenPrompt := promptui.Prompt{Label: "Bearer Token", Mask: '*'}
	token, err := tokenPrompt.Run()
	if err != nil {
		fmt.Printf("%s Error obtaining Bearer Token: %v\n", prefixErr, err)
		return Config{}
	}

	cfg := Config{OrgID: strings.TrimSpace(orgID), Token: strings.TrimSpace(token)}
	if err := validateConfig(cfg); err != nil {
		fmt.Printf("%s Invalid configuration: %v\n", prefixErr, err)
		return Config{}
	}
	if err := saveConfig(cfg); err != nil {
		fmt.Printf("%s Unable to save configuration: %v\n", prefixErr, err)
		return Config{}
	}
	fmt.Printf("%s Configuration saved!\n", prefixOK)
	return cfg
}

func createClient(cfg Config) *censys.SDK {
	return censys.New(
		censys.WithOrganizationID(cfg.OrgID),
		censys.WithSecurity(cfg.Token),
	)
}

// retryAPICall wraps an API call with retry logic using linear backoff. The wait
// between attempts is interruptible, so a cancelled ctx aborts immediately instead
// of sleeping through the remaining attempts.
func retryAPICall(ctx context.Context, maxRetries int, retryDelay time.Duration, operation string, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if !isRetryable(err) {
			return err
		}
		lastErr = err
		if attempt == maxRetries {
			break
		}
		wait := retryDelay * time.Duration(attempt)
		fmt.Printf("%s Attempt %d/%d failed for %s: %v. Retrying in %v...\n",
			prefixWarn, attempt, maxRetries, operation, err, wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s: %w", operation, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("operation %s failed after %d attempts: %w", operation, maxRetries, lastErr)
}

// isRetryable reports whether retrying err has any chance of succeeding.
// Client-side faults (bad query, unknown host, bad credentials) are permanent;
// retrying them only wastes time and, for metered endpoints, credits.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var authErr *sdkerrors.AuthenticationError
	if errors.As(err, &authErr) {
		return false
	}
	var model *sdkerrors.ErrorModel
	if errors.As(err, &model) && model.Status != nil {
		return retryableStatus(int(*model.Status))
	}
	var sdkErr *sdkerrors.SDKError
	if errors.As(err, &sdkErr) {
		return retryableStatus(sdkErr.StatusCode)
	}
	// Transport-level failure with no HTTP status attached — worth another go.
	return true
}

// retryableStatus treats 429 and 5xx as transient; every other 4xx is permanent.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// showCredits fetches and prints credit balance and the last 30 days of usage.
func showCredits(client *censys.SDK, appCfg AppConfig, orgID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), appCfg.RequestTimeout)
	defer cancel()

	fmt.Println("\n=== Credits and Usage ===")

	var creditsResp *operations.V3AccountmanagementOrgCreditsResponse
	err := retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay, "fetching credits", func() error {
		resp, err := client.AccountManagement.GetOrganizationCredits(ctx,
			operations.V3AccountmanagementOrgCreditsRequest{OrganizationID: orgID})
		if err != nil {
			return err
		}
		creditsResp = resp
		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to fetch credits information: %w", err)
	}

	if creditsResp != nil {
		raw, _ := json.Marshal(creditsResp)
		var parsed map[string]interface{}
		if err := json.Unmarshal(raw, &parsed); err == nil {
			balance, expiry := extractCreditInfo(parsed)
			fmt.Printf("  Credit balance : %v\n", balance)
			fmt.Printf("  Expiry         : %v\n", expiry)
		}
	}

	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	d, derr := sdktypes.NewDateFromString(startDate)
	if derr != nil {
		return fmt.Errorf("invalid start date: %w", derr)
	}

	var usageResp *operations.V3AccountmanagementOrgCreditsUsageResponse
	err = retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay, "fetching usage", func() error {
		resp, err := client.AccountManagement.GetOrganizationCreditUsage(ctx,
			operations.V3AccountmanagementOrgCreditsUsageRequest{
				OrganizationID: orgID,
				StartDate:      d,
				Granularity:    operations.GranularityDaily,
			})
		if err != nil {
			return err
		}
		usageResp = resp
		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to fetch usage information: %w", err)
	}

	if usageResp != nil {
		raw, _ := json.Marshal(usageResp)
		var parsed map[string]interface{}
		if err := json.Unmarshal(raw, &parsed); err == nil {
			printUsageSummary(parsed)
		}
	}

	return nil
}

// extractCreditInfo extracts the balance and expiration date from the credit response.
// The response envelope key varies between SDK versions so we try a few known paths.
func extractCreditInfo(parsed map[string]interface{}) (balance interface{}, expiry interface{}) {
	paths := [][]string{
		{"ResponseEnvelopeOrganizationCredits", "result"},
		{"result"},
	}
	for _, path := range paths {
		cur := interface{}(parsed)
		for _, key := range path {
			if m, ok := cur.(map[string]interface{}); ok {
				cur = m[key]
			}
		}
		if result, ok := cur.(map[string]interface{}); ok {
			balance = result["balance"]
			if expirations, ok := result["credit_expirations"].([]interface{}); ok && len(expirations) > 0 {
				if exp, ok := expirations[0].(map[string]interface{}); ok {
					expiry = exp["expires_at"]
				}
			}
			return
		}
	}
	return "?", "?"
}

// printUsageSummary displays a readable summary of credit usage.
// Skips periods with zero consumption to keep the output clean.
func printUsageSummary(parsed map[string]interface{}) {
	paths := [][]string{
		{"ResponseEnvelopeCreditUsageReport", "result"},
		{"result"},
	}
	for _, path := range paths {
		cur := interface{}(parsed)
		for _, key := range path {
			if m, ok := cur.(map[string]interface{}); ok {
				cur = m[key]
			}
		}
		if result, ok := cur.(map[string]interface{}); ok {
			fmt.Println("\n  Usage (last 30 days):")
			fmt.Printf("  Total consumed  : %v credits\n", result["total_consumed"])
			fmt.Printf("  Transaction count: %v\n", result["transaction_count"])
			if bySource, ok := result["credits_consumed_by_source"].(map[string]interface{}); ok {
				fmt.Printf("  API             : %v credits\n", bySource["api"])
				fmt.Printf("  UI              : %v credits\n", bySource["ui"])
			}
			if periods, ok := result["periods"].([]interface{}); ok && len(periods) > 0 {
				fmt.Println("\n  Daily usage:")
				for _, p := range periods {
					if period, ok := p.(map[string]interface{}); ok {
						consumed := period["credits_consumed"]
						date := period["start_date"]
						txCount := period["transaction_count"]
						if consumed != nil && consumed != float64(0) {
							fmt.Printf("    %v  ->  %v credits (%v tx)\n", date, consumed, txCount)
						}
					}
				}
			}
			return
		}
	}
}

// validateIP checks that the provided string is a valid IPv4 or IPv6 address.
func validateIP(ip string) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return errors.New("IP address cannot be empty")
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	return nil
}

// readLinesFromStdin reads IP addresses line by line from stdin until an empty
// line is entered. Invalid IPs are skipped with a warning.
func readLinesFromStdin() ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("\n%s Enter IP addresses (one per line), finish with an empty line:\n", prefixInfo)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		if err := validateIP(line); err != nil {
			fmt.Printf("%s Skipping invalid IP: %v\n", prefixWarn, err)
			continue
		}
		lines = append(lines, line)
		fmt.Printf("%s Added: %s (total: %d)\n", prefixOK, line, len(lines))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading from stdin: %w", err)
	}
	return lines, nil
}

// getIPsFromUser lets the user choose between pasting IPs manually or loading
// them from a plain-text file (one IP per line, # for comments).
func getIPsFromUser() ([]string, error) {
	fmt.Printf("\n%s You can paste IPs manually or load from a text file\n", prefixInfo)

	mode := promptui.Select{
		Label: "Choose input method",
		Items: []string{"1. Paste manually (one per line)", "2. Load from .txt file"},
	}
	_, choice, err := mode.Run()
	if err != nil {
		return nil, fmt.Errorf("error selecting mode: %w", err)
	}

	var ips []string
	if choice[0] == '1' {
		ips, err = readLinesFromStdin()
		if err != nil {
			return nil, err
		}
	} else {
		fileReader := bufio.NewReader(os.Stdin)
		fmt.Print("Path to .txt file [ips.txt]: ")
		path, err := fileReader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("error reading path: %w", err)
		}
		path = strings.TrimSpace(path)
		if path == "" {
			path = "ips.txt"
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("invalid file path: %w", err)
		}
		// #nosec G703 -- absPath validated via filepath.Abs; directory check below
		st, err := os.Stat(absPath)
		if err != nil {
			return nil, fmt.Errorf("unable to stat file %s: %w", absPath, err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("path is a directory, expected a file: %s", absPath)
		}
		// #nosec G304 G703 -- path validated above (Abs + Stat + directory check)
		file, err := os.Open(absPath)
		if err != nil {
			return nil, fmt.Errorf("unable to open file %s: %w", absPath, err)
		}
		defer func() {
			if cerr := file.Close(); cerr != nil {
				fmt.Printf("%s unable to close file: %v\n", prefixWarn, cerr)
			}
		}()
		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := strings.TrimSpace(scanner.Text())
			// Skip empty lines and comments
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if err := validateIP(line); err != nil {
				fmt.Printf("%s Line %d: skipping invalid IP: %v\n", prefixErr, lineNum, err)
				continue
			}
			ips = append(ips, line)
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("error reading file: %w", err)
		}
		fmt.Printf("%s Loaded %d IP addresses from file\n", prefixOK, len(ips))
	}

	if len(ips) == 0 {
		return nil, errors.New("no valid IP addresses provided")
	}
	return ips, nil
}

func parsePositiveInt(s string, fieldName string) (int64, error) {
	s = strings.TrimSpace(s)
	value, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid value for %s: %w", fieldName, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive number", fieldName)
	}
	return value, nil
}

// dedupIPs removes duplicates while preserving input order. Every duplicate that
// reaches Bulk View is a wasted credit, so this runs on every path into it.
func dedupIPs(ips []string) []string {
	seen := make(map[string]struct{}, len(ips))
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	return out
}

// extractIPsFromHits extracts unique IP addresses from Search results.
// Supports current API structure: hit.host_v1.resource.ip
func extractIPsFromHits(hits []interface{}) []string {
	var ips []string
	seen := make(map[string]bool)

	for _, h := range hits {
		raw, err := json.Marshal(h)
		if err != nil {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}

		ip := extractIPFromHit(m)
		if ip != "" && validateIP(ip) == nil && !seen[ip] {
			ips = append(ips, ip)
			seen[ip] = true
		}
	}
	return ips
}

// extractIPFromHit extracts an IP from a single search hit.
// Supports several variants of the Censys API response structure —
// the shape has changed across API versions so we try each known path.
func extractIPFromHit(m map[string]interface{}) string {
	// Variant 1 (current): hit.host_v1.resource.ip
	if hostV1, ok := m["host_v1"].(map[string]interface{}); ok {
		if resource, ok := hostV1["resource"].(map[string]interface{}); ok {
			if ip, ok := resource["ip"].(string); ok && ip != "" {
				return ip
			}
		}
	}
	// Variant 2: hit.host.ip
	if host, ok := m["host"].(map[string]interface{}); ok {
		if ip, ok := host["ip"].(string); ok && ip != "" {
			return ip
		}
	}
	// Variant 3: hit.ip (flat structure)
	if ip, ok := m["ip"].(string); ok && ip != "" {
		return ip
	}
	return ""
}

// bulkViewIPs fetches full data for a list of IPs via sequential GetHost calls.
// Each call costs 1 credit, so the user is warned and must confirm before proceeding.
func bulkViewIPs(client *censys.SDK, appCfg AppConfig, orgID string, ips []string) {
	ips = dedupIPs(ips)

	fmt.Printf("\n%s Estimated cost: %d credits (1 credit per host)\n", prefixInfo, len(ips))
	fmt.Printf("%s Continue with Bulk View? [y/N]: ", prefixInfo)

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println("Bulk View cancelled.")
		return
	}

	if err := showCredits(client, appCfg, orgID); err != nil {
		fmt.Printf("%s %v\n", prefixWarn, err)
	}

	hostMap := make(map[string]interface{})

	fmt.Printf("Fetching %d hosts...\n", len(ips))

	bar := progressbar.NewOptions(len(ips),
		progressbar.OptionSetDescription("Progress"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowIts(),
		progressbar.OptionSetItsString("IP"),
	)

	for _, currentIP := range ips {
		var hostResp *operations.V3GlobaldataAssetHostResponse

		// The timeout budget is per host, not for the whole run — otherwise a long
		// list would start failing the moment the first deadline elapsed.
		ctx, cancel := context.WithTimeout(context.Background(), appCfg.RequestTimeout)
		err := retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay,
			fmt.Sprintf("host %s", currentIP), func() error {
				resp, err := client.GlobalData.GetHost(ctx,
					operations.V3GlobaldataAssetHostRequest{HostID: currentIP})
				if err != nil {
					return err
				}
				hostResp = resp
				return nil
			})
		cancel()

		if err != nil {
			fmt.Printf("\n%s Error for %s: %v\n", prefixErr, currentIP, err)
			if berr := bar.Add(1); berr != nil {
				fmt.Printf("%s progress bar update error: %v\n", prefixWarn, berr)
			}
			continue
		}

		if hostResp != nil {
			raw, err := json.Marshal(hostResp)
			if err == nil {
				var parsed map[string]interface{}
				if err := json.Unmarshal(raw, &parsed); err == nil {
					// Strip the envelope wrapper — callers only care about the result payload.
					if result := extractResultFromParsed(parsed); result != nil {
						hostMap[currentIP] = result
					} else {
						hostMap[currentIP] = parsed
					}
				} else {
					hostMap[currentIP] = hostResp
				}
			} else {
				hostMap[currentIP] = hostResp
			}
		}

		if berr := bar.Add(1); berr != nil {
			fmt.Printf("%s progress bar update error: %v\n", prefixWarn, berr)
		}
	}

	fmt.Println()
	if len(hostMap) > 0 {
		fmt.Printf("%s Fetched full data for %d hosts.\n", prefixOK, len(hostMap))
		askToSave(hostMap, "censys_bulkview")
	} else {
		fmt.Printf("%s Failed to fetch any data\n", prefixErr)
	}
}

// extractResultFromParsed extracts the result field from the Censys API response (skips Headers etc.)
// The SDK wraps responses in a named envelope struct; we just want the inner result.
func extractResultFromParsed(parsed map[string]interface{}) interface{} {
	searchKeys := []string{
		"ResponseEnvelopeHostAsset",
		"ResponseEnvelopeListHostAsset",
	}
	for _, key := range searchKeys {
		if envelope, ok := parsed[key].(map[string]interface{}); ok {
			if result, ok := envelope["result"]; ok {
				return result
			}
		}
	}
	if result, ok := parsed["result"]; ok {
		return result
	}
	return nil
}

// handleSearch handles host search (search option) with page pagination.
// Also allows redirecting to Bulk View after collecting results.
func handleSearch(client *censys.SDK, appCfg AppConfig, orgID string) {
	fmt.Printf("\n%s Example queries:\n", prefixInfo)
	fmt.Println("   - services.port:443")
	fmt.Println("   - services.port:22 and location.country:\"PL\"")
	fmt.Println("   - services.software.product:\"Apache\"")
	fmt.Println("   - autonomous_system.name:\"Amazon\"")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Censys query: ")
	query, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading query: %v\n", prefixErr, err)
		return
	}
	query = strings.TrimSpace(query)
	if query == "" {
		fmt.Printf("%s Query cannot be empty\n", prefixErr)
		return
	}

	fmt.Printf("%s Fetch all pages? [y/N]: ", prefixInfo)
	paginationAnswer, _ := reader.ReadString('\n')
	fetchAllPages := strings.ToLower(strings.TrimSpace(paginationAnswer)) == "y"

	cursor := ""
	var allHits []interface{}

	fmt.Println(" [...] Searching...")

	for {
		var searchResp *operations.V3GlobaldataSearchQueryResponse

		// One timeout budget per page, so fetching all pages of a large result
		// set cannot trip a single deadline shared by the whole loop.
		ctx, cancel := context.WithTimeout(context.Background(), appCfg.RequestTimeout)
		err := retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay, "search", func() error {
			var pageToken *string
			if cursor != "" {
				pageToken = censys.Pointer(cursor)
			}
			req := operations.V3GlobaldataSearchQueryRequest{
				SearchQueryInputBody: components.SearchQueryInputBody{
					Query:     query,
					Fields:    defaultSearchFields,
					PageSize:  censys.Pointer(int64(appCfg.PageSize)),
					PageToken: pageToken,
				},
			}
			resp, err := client.GlobalData.Search(ctx, req)
			if err != nil {
				return err
			}
			searchResp = resp
			return nil
		})
		cancel()

		if err != nil {
			fmt.Printf("%s Error during search: %v\n", prefixErr, err)
			break
		}

		resp := searchResp.GetResponseEnvelopeSearchQueryResponse()
		if resp == nil || resp.GetResult() == nil {
			fmt.Printf("%s No results in response\n", prefixErr)
			break
		}

		result := resp.GetResult()
		hits := result.GetHits()
		fmt.Printf("  Retrieved %d results (total %d of %.0f matching)\n",
			len(hits), len(allHits)+len(hits), result.GetTotalHits())
		for _, h := range hits {
			allHits = append(allHits, h)
		}

		cursor = result.GetNextPageToken()
		if !fetchAllPages || cursor == "" {
			break
		}
	}

	if len(allHits) == 0 {
		fmt.Printf("%s No results.\n", prefixWarn)
		return
	}

	fmt.Printf("\n%s Retrieved a total of %d results.\n", prefixOK, len(allHits))

	// Show a preview of the first hit so the user knows what they're saving.
	preview, _ := json.MarshalIndent(allHits[0], "", "  ")
	fmt.Printf("\n%s Preview of first result:\n%s\n", prefixInfo, string(preview))

	askToSave(allHits, "censys_search")

	// Optionally pipe the search results straight into a Bulk View.
	ips := extractIPsFromHits(allHits)
	if len(ips) == 0 {
		fmt.Printf("%s No IP addresses found in results.\n", prefixWarn)
		return
	}

	fmt.Printf("%s Extracted %d unique IP addresses.\n", prefixOK, len(ips))
	fmt.Printf("%s Run Bulk View for extracted IPs? [y/N]: ", prefixInfo)
	bulkReader := bufio.NewReader(os.Stdin)
	bulkAnswer, _ := bulkReader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(bulkAnswer)) == "y" {
		bulkViewIPs(client, appCfg, orgID, ips)
	}
}

// handleSingleView handles a single host query by IP address.
func handleSingleView(client *censys.SDK, appCfg AppConfig) {
	fmt.Printf("\n%s View Host - details for a single host\n", prefixInfo)

	ipReader := bufio.NewReader(os.Stdin)
	fmt.Print("IP or hostname: ")
	ip, err := ipReader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading IP: %v\n", prefixErr, err)
		return
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		fmt.Printf("%s IP cannot be empty\n", prefixErr)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), appCfg.RequestTimeout)
	defer cancel()

	var hostResp *operations.V3GlobaldataAssetHostResponse
	err = retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay, "fetching host", func() error {
		resp, err := client.GlobalData.GetHost(ctx,
			operations.V3GlobaldataAssetHostRequest{HostID: ip})
		if err != nil {
			return err
		}
		hostResp = resp
		return nil
	})
	if err != nil {
		fmt.Printf("%s Unable to fetch host information: %v\n", prefixErr, err)
		return
	}

	jsonData, err := json.MarshalIndent(hostResp, "", "  ")
	if err != nil {
		fmt.Printf("%s Error formatting JSON: %v\n", prefixErr, err)
		return
	}
	fmt.Println(string(jsonData))
	askToSave(hostResp, "censys_view")
}

// handleBulkView is called during BulkView handling.
// It collects IPs from the user and delegates to bulkViewIPs.
func handleBulkView(client *censys.SDK, appCfg AppConfig, orgID string) {
	fmt.Printf("\n%s Bulk View - fetching full information for multiple hosts\n", prefixInfo)
	fmt.Printf("%s Each host = 1 credit. Make sure you have sufficient credits!\n", prefixInfo)

	ips, err := getIPsFromUser()
	if err != nil {
		fmt.Printf("%s Error getting IP list: %v\n", prefixErr, err)
		return
	}

	bulkViewIPs(client, appCfg, orgID, ips)
}

// handleAggregate handles Aggregate queries — useful for quick field distribution analysis.
func handleAggregate(client *censys.SDK, appCfg AppConfig) {
	fmt.Printf("\n%s Aggregate - aggregate data by a field\n", prefixInfo)
	fmt.Printf("%s Example — query: services.port:443 | field: services.port or location.country\n", prefixInfo)
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Censys query: ")
	query, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading query: %v\n", prefixErr, err)
		return
	}
	query = strings.TrimSpace(query)
	if query == "" {
		fmt.Printf("%s Query cannot be empty\n", prefixErr)
		return
	}

	fmt.Print("Field to aggregate [services.port]: ")
	field, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading field: %v\n", prefixErr, err)
		return
	}
	field = strings.TrimSpace(field)
	if field == "" {
		field = "services.port"
	}

	fmt.Print("Number of buckets [20]: ")
	bucketsStr, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading number of buckets: %v\n", prefixErr, err)
		return
	}
	bucketsStr = strings.TrimSpace(bucketsStr)
	if bucketsStr == "" {
		bucketsStr = "20"
	}
	numBuckets, err := parsePositiveInt(bucketsStr, "number of buckets")
	if err != nil {
		fmt.Printf("%s %v\n", prefixErr, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), appCfg.RequestTimeout)
	defer cancel()

	var aggregateResp *operations.V3GlobaldataSearchAggregateResponse
	err = retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay, "aggregate", func() error {
		req := operations.V3GlobaldataSearchAggregateRequest{
			SearchAggregateInputBody: components.SearchAggregateInputBody{
				Query:           query,
				Field:           field,
				NumberOfBuckets: numBuckets,
			},
		}
		resp, err := client.GlobalData.Aggregate(ctx, req)
		if err != nil {
			return err
		}
		aggregateResp = resp
		return nil
	})
	if err != nil {
		fmt.Printf("%s Error during aggregation: %v\n", prefixErr, err)
		return
	}

	jsonData, err := json.MarshalIndent(aggregateResp, "", "  ")
	if err != nil {
		fmt.Printf("%s Error formatting JSON: %v\n", prefixErr, err)
		return
	}
	fmt.Println(string(jsonData))
	askToSave(aggregateResp, "censys_aggregate")
}

// handleCertificate handles certificate lookup by SHA-256 fingerprint.
func handleCertificate(client *censys.SDK, appCfg AppConfig) {
	fmt.Printf("\n%s Certificate Lookup - TLS certificate search\n", prefixInfo)
	fmt.Printf("%s Enter SHA-256 fingerprint (64 hex characters)\n", prefixInfo)

	fpReader := bufio.NewReader(os.Stdin)
	fmt.Print("SHA-256 fingerprint: ")
	fingerprint, err := fpReader.ReadString('\n')
	if err != nil {
		fmt.Printf("%s Error reading fingerprint: %v\n", prefixErr, err)
		return
	}
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if fingerprint == "" {
		fmt.Printf("%s Fingerprint cannot be empty\n", prefixErr)
		return
	}
	if len(fingerprint) != 64 {
		fmt.Printf("%s SHA-256 fingerprint must be exactly 64 characters\n", prefixErr)
		return
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		fmt.Printf("%s Fingerprint contains invalid hex characters: %v\n", prefixErr, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), appCfg.RequestTimeout)
	defer cancel()

	var certResp *operations.V3GlobaldataAssetCertificateResponse
	err = retryAPICall(ctx, appCfg.MaxRetries, appCfg.RetryDelay, "fetching certificate", func() error {
		resp, err := client.GlobalData.GetCertificate(ctx,
			operations.V3GlobaldataAssetCertificateRequest{CertificateID: fingerprint})
		if err != nil {
			return err
		}
		certResp = resp
		return nil
	})
	if err != nil {
		fmt.Printf("%s Unable to fetch certificate: %v\n", prefixErr, err)
		return
	}

	jsonData, err := json.MarshalIndent(certResp, "", "  ")
	if err != nil {
		fmt.Printf("%s Error formatting JSON: %v\n", prefixErr, err)
		return
	}
	fmt.Println(string(jsonData))
	askToSave(certResp, "censys_cert")
}

func main() {
	fmt.Printf("=== Censys-Go CLI %s ===\n", version)

	// Environment variables take precedence over saved config — useful for CI or Docker.
	envOrg := strings.TrimSpace(os.Getenv("CENSYS_ORG"))
	envToken := strings.TrimSpace(os.Getenv("CENSYS_TOKEN"))
	var cfg Config
	if envOrg != "" && envToken != "" {
		cfg = Config{OrgID: envOrg, Token: envToken}
		if err := saveConfig(cfg); err != nil {
			fmt.Printf("%s Unable to save configuration from env: %v\n", prefixWarn, err)
		} else {
			fmt.Printf("%s Saved configuration from environment variables\n", prefixOK)
		}
	}

	if cfg.OrgID == "" || cfg.Token == "" {
		cfg = ensureConfig()
	}

	client := createClient(cfg)
	appCfg := defaultAppConfig

	for {
		menu := promptui.Select{
			Label: "Select action",
			Items: []string{
				"0. Show credits and usage",
				"1. Configure",
				"2. Search hosts (with pagination)",
				"3. View host (single IP)",
				"4. Bulk Full View (multiple IPs)",
				"5. Aggregate",
				"6. Certificate lookup",
				"7. Exit",
			},
		}

		_, choice, err := menu.Run()
		if err != nil {
			fmt.Printf("%s Menu error: %v\n", prefixErr, err)
			continue
		}

		switch choice {
		case "0. Show credits and usage":
			if err := showCredits(client, appCfg, cfg.OrgID); err != nil {
				fmt.Printf("%s %v\n", prefixErr, err)
			}
		case "1. Configure":
			newCfg := interactiveConfig()
			if newCfg.OrgID != "" {
				cfg = newCfg
				client = createClient(cfg)
			} else {
				fmt.Printf("%s Configuration not changed\n", prefixWarn)
			}
		case "2. Search hosts (with pagination)":
			handleSearch(client, appCfg, cfg.OrgID)
		case "3. View host (single IP)":
			handleSingleView(client, appCfg)
		case "4. Bulk Full View (multiple IPs)":
			handleBulkView(client, appCfg, cfg.OrgID)
		case "5. Aggregate":
			handleAggregate(client, appCfg)
		case "6. Certificate lookup":
			handleCertificate(client, appCfg)
		case "7. Exit":
			fmt.Println("Goodbye!")
			return
		default:
			fmt.Printf("%s Invalid choice\n", prefixErr)
		}

		fmt.Println("\nPress Enter to return to the menu...")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			fmt.Printf("%s Error reading stdin: %v\n", prefixWarn, err)
		}
	}
}

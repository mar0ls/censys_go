# Censys-Go — CLI Documentation

<div id="header" align="center">
    <img src="https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExcnlxcXUxaHhsa2J0N3ZranM2a3RxaXUyaWRpZW96bHoxY2poaXJ3bCZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/q15lIdQWBYs7K/giphy.gif" width="200"/>
</div>

## Table of contents

1. [Package overview](#package-overview)
2. [Configuration](#configuration)
3. [File handling & results](#file-handling-results)
4. [Utilities](#utilities)
5. [Command handlers](#command-handlers)
6. [Other](#other)

---

## Package overview

Censys-Go CLI: interactive command-line client for the Censys API.
PoC how can you use censys-go-sdk from https://github.com/censys/censys-sdk-go (license: MIT)
Provides interactive search, host and certificate lookup, bulk operations
and JSON export of results.

---

## Configuration

| Function / Type | Description |
|-----------------|-------------|
| `Config` | Config holds configuration required to connect to the Censys API. |
| `AppConfig` | AppConfig contains runtime configuration for the CLI. |
| `loadConfig()` | _No description provided._ |
| `saveConfig()` | _No description provided._ |
| `validateConfig()` | _No description provided._ |
| `ensureConfig()` | ensureConfig tries to load an existing config. If that fails (first run, |
| `interactiveConfig()` | interactiveConfig re-prompts for credentials and overwrites the saved config. |
| `createClient()` | _No description provided._ |

### `Config`

Config holds configuration required to connect to the Censys API.

### `AppConfig`

AppConfig contains runtime configuration for the CLI.

### `loadConfig()`

_No comment provided._

### `saveConfig()`

_No comment provided._

### `validateConfig()`

_No comment provided._

### `ensureConfig()`

ensureConfig tries to load an existing config. If that fails (first run,
corrupted file, etc.) it falls back to prompting the user interactively.

### `interactiveConfig()`

interactiveConfig re-prompts for credentials and overwrites the saved config.
Called from the main menu when the user wants to switch organizations/tokens.

### `createClient()`

_No comment provided._

---

## File handling & results

| Function / Type | Description |
|-----------------|-------------|
| `getHomeDir()` | _No description provided._ |
| `ensureResultsDir()` | ensureResultsDir creates a "results" directory in the current working directory |
| `saveJSON()` | saveJSON serializes data to indented JSON and writes it to the results directory. |
| `askToSave()` | askToSave presents a summary of results and prompts the user to save or |

### `getHomeDir()`

_No comment provided._

### `ensureResultsDir()`

ensureResultsDir creates a "results" directory in the current working directory
if it doesn't already exist. Files are stored with 0700 permissions.

### `saveJSON()`

saveJSON serializes data to indented JSON and writes it to the results directory.

### `askToSave()`

askToSave presents a summary of results and prompts the user to save or
print them. Intentionally kept simple — no fancy TUI here.

---

## Utilities

| Function / Type | Description |
|-----------------|-------------|
| `retryAPICall()` | retryAPICall wraps an API call with retry logic using linear backoff. |
| `validateIP()` | validateIP checks that the provided string is a valid IPv4 or IPv6 address. |
| `readLinesFromStdin()` | readLinesFromStdin reads IP addresses line by line from stdin until an empty |
| `getIPsFromUser()` | getIPsFromUser lets the user choose between pasting IPs manually or loading |
| `parsePositiveInt()` | _No description provided._ |
| `showCredits()` | showCredits fetches and prints credit balance and the last 30 days of usage. |

### `retryAPICall()`

retryAPICall wraps an API call with retry logic using linear backoff.
Auth errors are not retried — no point hammering the API with bad credentials.

### `validateIP()`

validateIP checks that the provided string is a valid IPv4 or IPv6 address.

### `readLinesFromStdin()`

readLinesFromStdin reads IP addresses line by line from stdin until an empty
line is entered. Invalid IPs are skipped with a warning.

### `getIPsFromUser()`

getIPsFromUser lets the user choose between pasting IPs manually or loading
them from a plain-text file (one IP per line, # for comments).

### `parsePositiveInt()`

_No comment provided._

### `showCredits()`

showCredits fetches and prints credit balance and the last 30 days of usage.

---

## Command handlers

| Function / Type | Description |
|-----------------|-------------|
| `handleSearch()` | handleSearch handles host search (search option) with page pagination. |
| `handleSingleView()` | handleSingleView handles a single host query by IP address. |
| `handleBulkView()` | handleBulkView is called during BulkView handling. |
| `handleAggregate()` | handleAggregate handles Aggregate queries — useful for quick field distribution analysis. |
| `handleCertificate()` | handleCertificate handles certificate lookup by SHA-256 fingerprint. |

### `handleSearch()`

handleSearch handles host search (search option) with page pagination.
Also allows redirecting to Bulk View after collecting results.

### `handleSingleView()`

handleSingleView handles a single host query by IP address.

### `handleBulkView()`

handleBulkView is called during BulkView handling.
It collects IPs from the user and delegates to bulkViewIPs.

### `handleAggregate()`

handleAggregate handles Aggregate queries — useful for quick field distribution analysis.

### `handleCertificate()`

handleCertificate handles certificate lookup by SHA-256 fingerprint.

---

## Other

| Function / Type | Description |
|-----------------|-------------|
| `isPathWithin()` | isPathWithin checks that child does not escape outside parent via path traversal. |
| `printResultSummary()` | printResultSummary displays a brief summary of results before asking to save. |
| `isAuthError()` | isAuthError checks whether the error looks like a 401/auth failure. |
| `extractCreditInfo()` | extractCreditInfo extracts the balance and expiration date from the credit response. |
| `printUsageSummary()` | printUsageSummary displays a readable summary of credit usage. |
| `extractIPsFromHits()` | extractIPsFromHits extracts unique IP addresses from Search results. |
| `extractIPFromHit()` | extractIPFromHit extracts an IP from a single search hit. |
| `bulkViewIPs()` | bulkViewIPs fetches full data for a list of IPs via sequential GetHost calls. |
| `extractResultFromParsed()` | extractResultFromParsed extracts the result field from the Censys API response (skips Headers etc.) |
| `main()` | _No description provided._ |

### `isPathWithin()`

isPathWithin checks that child does not escape outside parent via path traversal.
Simple but effective guard against "../../../etc/passwd" style attacks.

### `printResultSummary()`

printResultSummary displays a brief summary of results before asking to save.
For bulk view results (map[IP]data) it also lists the IP addresses.

### `isAuthError()`

isAuthError checks whether the error looks like a 401/auth failure.
The SDK doesn't expose typed errors, so we resort to string matching.

### `extractCreditInfo()`

extractCreditInfo extracts the balance and expiration date from the credit response.
The response envelope key varies between SDK versions so we try a few known paths.

### `printUsageSummary()`

printUsageSummary displays a readable summary of credit usage.
Skips periods with zero consumption to keep the output clean.

### `extractIPsFromHits()`

extractIPsFromHits extracts unique IP addresses from Search results.
Supports current API structure: hit.host_v1.resource.ip

### `extractIPFromHit()`

extractIPFromHit extracts an IP from a single search hit.
Supports several variants of the Censys API response structure —
the shape has changed across API versions so we try each known path.

### `bulkViewIPs()`

bulkViewIPs fetches full data for a list of IPs via sequential GetHost calls.
Each call costs 1 credit, so the user is warned and must confirm before proceeding.

### `extractResultFromParsed()`

extractResultFromParsed extracts the result field from the Censys API response (skips Headers etc.)
The SDK wraps responses in a named envelope struct; we just want the inner result.

### `main()`

_No comment provided._

---


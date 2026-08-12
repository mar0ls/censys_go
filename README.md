# Censys-Go CLI

[![Go version](https://img.shields.io/badge/go-1.25%2B-00ADD8?style=flat&logo=go)](https://golang.org/doc/install)
[![Docs](https://img.shields.io/badge/docs-generated-blueviolet?style=flat&logo=markdown)](docs/DOCUMENTATION.md)

<div id="header" align="center">
    <img src="https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExcnlxcXUxaHhsa2J0N3ZranM2a3RxaXUyaWRpZW96bHoxY2poaXJ3bCZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/q15lIdQWBYs7K/giphy.gif" width="200"/>
</div>

A command-line client for the Censys Platform API, built on the official
[censys-sdk-go](https://github.com/censys/censys-sdk-go) (MIT). It runs either as
subcommands you can put in a pipeline or as an interactive menu.

## Overview

The tool is shaped around chasing infrastructure rather than looking up one host
at a time:

- **Search** with pagination, streaming results as they arrive
- **Pivots** from a certificate to every host observed serving it — including
  hosts that have since gone dark and no longer show up in a live search
- **Timelines** showing when a host's services and certificates changed
- **Batch host lookups**, one request per hundred addresses
- **Historical snapshots** through `--at`, for working an old report
- **Aggregations** to see how a population is distributed across a field

Results go to stdout and diagnostics to stderr, so redirecting stdout gives you
a file containing only data.

## Requirements

- Go 1.25 or newer
- A Censys account with an API token

## Installation

```bash
git clone https://github.com/mar0ls/censys_go.git
cd censys_go
make build          # produces ./censys_go
```

Or run it straight from source with `go run .`.

## Configuration

Credentials are resolved from the first source that supplies both values:

1. `--org` and `--token` flags
2. `CENSYS_ORG` and `CENSYS_TOKEN` environment variables
3. `$HOME/.censys/config.json` (mode `0600`)

```bash
export CENSYS_ORG="your_org_id"
export CENSYS_TOKEN="your_token"
```

Credentials taken from the environment stay in the process — they are never
written to disk. To store them, run `censys_go` with no arguments and use
**Configure credentials** in the menu.

## Usage

```
censys_go [flags] <command> [flags] [arguments]
```

Running it with no command starts the interactive menu.

| Command | Purpose |
|---|---|
| `search -q QUERY` | Run a CenQL query and stream matching hosts |
| `host [IP\|CIDR ...]` | Fetch hosts by address, from a file, or from stdin |
| `cert SHA256` | Look up a certificate by fingerprint |
| `cert-hosts SHA256` | Every host observed serving a certificate |
| `timeline IP` | A host's service and certificate changes over a window |
| `aggregate -q QUERY` | Bucket a query by field |
| `credits` | Credit balance and recent usage |
| `ui` | The interactive menu |

### Global flags

| Flag | Default | Meaning |
|---|---|---|
| `--format` | `ndjson` | `ndjson`, `json`, `table`, or `csv` |
| `--output` | `-` | Write results to a file instead of stdout |
| `--quiet` | off | Suppress the status stream on stderr |
| `--timeout` | `60s` | Budget for one API call, retries included |
| `--no-retry` | off | Attempt each call once |
| `--api-url` | — | Point the client at a proxy or a capture |

Exit status is `0` on success, `2` for a malformed command line, `130` when
interrupted, and `1` otherwise. An interrupt keeps whatever was already written.

### Examples

Find a Cobalt Strike population and keep every page:

```bash
censys_go search -q 'host.services.port=9001 and host.services.software.product:"Team Server"' \
  --pages 0 > c2.ndjson
```

Pivot from one panel's certificate to the rest of the fleet, then pull full
records for what you find:

```bash
censys_go cert-hosts 3fa1b2c4... --since 2026-01-01 --pages 3 --format csv > observed.csv
cut -d, -f1 observed.csv | tail -n +2 | sort -u | censys_go host > fleet.ndjson
```

Work a result set with `jq`:

```bash
censys_go search -q 'host.services.jarm.fingerprint="07d14d16d21d21d"' --pages 0 \
  | jq -r 'select(.asn == 64500) | .ip'
```

Check when a host started serving on 9001:

```bash
censys_go timeline 198.51.100.7 --since 2026-01-01 --format json
```

Read targets from a file, expanding CIDR prefixes on the way in:

```bash
censys_go host -f suspects.txt --format table
```

### Query syntax

Platform queries are CenQL and every field carries its dataset prefix — a bare
`services.port` matches nothing, it has to be `host.services.port`. Two
comparison operators are available and they differ: `:` is a case-insensitive
tokenized match, `=` is exact. Use `=` for fingerprints and ports, `:` when you
want a substring-ish match on a product or organisation name.

Queries copied from the legacy Censys Search syntax will not run as-is; Censys
publishes a [query converter](https://docs.censys.com/docs/query-converter) for
migrating them.

### Credit cost

Per the [Censys credit documentation](https://docs.censys.com/docs/platform-credits-enterprise):

| Action | Cost |
|---|---|
| `search` | 1 credit, plus 1 for each additional page of 100 |
| `host` | 1 credit per asset retrieved |
| `cert` | 1 credit |
| `cert-hosts` | **5 credits per page** of 100 |

`cert-hosts` is the expensive one, so it reports the cost up front and how many
pages it actually fetched; cap it with `--pages`. Target lists are deduplicated
before anything is sent, and CIDR prefixes wider than 4096 addresses are refused
rather than silently expanded. `credits` reports the balance before you commit
to a large sweep.

## Output formats

`ndjson` (the default) writes one JSON document per line: it streams, it
survives truncation, and `jq` and DuckDB read it directly. `json` writes a
single array, `table` aligned columns for reading, and `csv` a header row plus
one row per host with the fields worth eyeballing — address, ASN, country,
ports, software, certificate hash, JARM, and DNS names.

## Development

```bash
make check      # gofmt, go vet, golangci-lint, go test -race
make test
make lint
```

Package documentation is generated from the Go doc comments:

```bash
python3 scripts/generate_docs.py    # writes docs/DOCUMENTATION.md
```

### Layout

| Path | Contents |
|---|---|
| `main.go` | Entry point, signal handling, exit codes |
| `internal/cli` | Flag parsing, subcommand dispatch |
| `internal/censysx` | SDK facade: client, typed calls, error classification |
| `internal/render` | Streaming ndjson/json/table/csv writers |
| `internal/config` | Credential resolution and storage |
| `internal/hunt` | Target parsing: addresses, CIDR expansion, dedup |
| `internal/ui` | The interactive menu |

### Cross-compiling

```bash
make linux linux-arm64 macos macos-arm64 windows
make all-platforms
```

`scripts/build.sh` and `scripts/build.ps1` do the same without `make`.

## Contributing

Issues and pull requests welcome. Please include tests, and run `make check`
before opening one.

## Acknowledgements

Built on the official [Censys Go SDK](https://github.com/censys/censys-sdk-go).

## License

MIT

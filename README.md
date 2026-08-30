# go-web-crawler

A polite, concurrent web crawler in **Go with zero external dependencies** (standard
library only). BFS by default, per-host rate limiting, optional robots.txt respect,
and three output formats (JSON, CSV, plain text).

## Quick Start

```bash
# Build
go build -o crawler ./cmd/crawler

# Crawl a site (max 50 pages by default, unlimited depth)
./crawler -seed https://example.com

# 5 pages, follow links 1 level deep, write JSON to a file
./crawler -seed https://go.dev -max-pages 5 -depth 1 -format json -out pages.json
```

## Flags

### Required
- `-seed URL` — single starting URL
- `-seeds PATH` — file with one URL per line (`#` for comments)

### Crawl control
- `-max-pages N` — hard cap on total pages (default 50)
- `-depth N` — max BFS depth: **-1**=unlimited, **0**=seeds only, **N**=follow N levels (default -1)
- `-concurrency N` — number of worker goroutines (default 4)
- `-delay-ms N` — per-host delay between requests (default 200)
- `-timeout N` — HTTP timeout in seconds (default 15)
- `-user-agent STRING` — override User-Agent
- `-respect-robots` — honor robots.txt rules
- `-allowed-domain HOST` — restrict links to a single domain (e.g. `example.com`)

### Output
- `-out PATH` — output file, `-` for stdout (default `-`)
- `-format FMT` — `json` | `csv` | `text` (default `text`)
- `-quiet` — suppress per-skip log lines

## Examples

```bash
# Polite crawl of go.dev
./crawler -seed https://go.dev -max-pages 100 -delay-ms 500 -respect-robots -format json -out go.json

# Multi-seed from a file
./crawler -seeds seeds.txt -max-pages 200 -depth 2 -format csv -out crawl.csv

# Single-page extract
./crawler -seed https://example.com -max-pages 1 -depth 0
```

## Architecture

```
crawler (orchestrator, BFS)
  ├── fetcher (HTTP client + robots + per-host delay)
  ├── parser (HTML title + link extraction via regex)
  └── output (json / csv / text)

cmd/crawler/main.go    — CLI (flag parsing, signal handling, output wiring)
internal/crawler/      — orchestrator (BFS engine, producer + workers)
internal/fetcher/      — HTTP fetcher, robots.txt, polite delay
internal/parser/       — HTML extraction
internal/output/       — writers for json/csv/text
```

### Concurrency model

- One **producer** goroutine owns the URL "seen" set and feeds the frontier.
- N **worker** goroutines pull URLs from the frontier, fetch+parse, and post
  results back to the producer.
- The producer enqueues newly discovered links subject to the cap and depth.
- `runCtx` is cancelled when the cap is hit or `Ctrl+C` is pressed, which aborts
  in-flight HTTP requests immediately.

## Output Formats

### JSON
```json
{
  "count": 2,
  "pages": [
    { "url": "...", "status_code": 200, "title": "...", "links": [...], ... }
  ]
}
```

### CSV
```
url,status,title,host,duration_ms,fetched_at,link_count
```

### Text
```
[1] https://example.com
    title:   Example Domain
    status:  200
    host:    example.com
    fetched: 2026-08-30 04:00:00 (42ms)
    links:   1
      - https://iana.org/domains/example
```

## Known limitations

- **Single-process in-memory dedup set**: this crawler is meant for moderate
  crawls (low thousands of pages). For large crawls you would want a
  persistent dedup store.
- **HTML parsing is regex-based**: fast and dependency-free, but not as
  correct as a real HTML parser. It handles the common cases (anchor + link
  tags, relative URL resolution, ignores `javascript:` and similar).
- **No JavaScript rendering**: only server-rendered HTML is followed.

## License

MIT

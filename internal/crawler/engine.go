// Package crawler is the BFS engine. The first implementation tried
// concurrent workers with shared state and ran into a deadlock that
// was hard to debug. The current version is **sequential** (one URL
// at a time) — fast enough for personal use and easy to reason about.
// Concurrency can be re-added later by parallelising fetcher.Get
// calls only (the seen-set is local to the run, no shared state).
package crawler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/qifani35/go-web-crawler/internal/fetcher"
	"github.com/qifani35/go-web-crawler/internal/parser"
)

// Page is the result of fetching and parsing a single URL.
type Page struct {
	URL        string    `json:"url"`
	StatusCode int       `json:"status_code"`
	Title      string    `json:"title"`
	Links      []string  `json:"links"`
	FetchedAt  time.Time `json:"fetched_at"`
	DurationMs int64     `json:"duration_ms"`
	Host       string    `json:"host"`
}

// Options configures a crawl run. Constructed from CLI flags in main.go.
type Options struct {
	Seeds          []string
	MaxPages       int
	MaxDepth       int
	AllowedDomains []string
	Concurrency    int // currently unused; sequential for now
	DelayMs        int
	TimeoutSeconds int
	UserAgent      string
	RespectRobots  bool
	OutputPath     string
	OutputFormat   string
}

// Crawler is a BFS engine. Currently sequential.
type Crawler struct {
	opts    Options
	fetcher *fetcher.Fetcher
	results []Page
}

// New creates a Crawler.
func New(opts Options) *Crawler {
	fopts := fetcher.Options{
		UserAgent:     opts.UserAgent,
		DelayMs:       opts.DelayMs,
		TimeoutSeconds: opts.TimeoutSeconds,
		RespectRobots: opts.RespectRobots,
	}
	return &Crawler{
		opts:    opts,
		fetcher: fetcher.New(fopts),
	}
}

// urlEntry is one item in the BFS frontier.
type urlEntry struct {
	url   string
	depth int
}

// Run starts the crawl and blocks until it finishes, the cap is
// reached, or the context is cancelled. Returns the collected pages.
func (c *Crawler) Run(ctx context.Context) ([]Page, error) {
	if len(c.opts.Seeds) == 0 {
		return nil, errors.New("at least one seed URL is required")
	}
	if c.opts.MaxPages <= 0 {
		c.opts.MaxPages = 100
	}

	allowed := make(map[string]struct{}, len(c.opts.AllowedDomains))
	for _, d := range c.opts.AllowedDomains {
		allowed[d] = struct{}{}
	}
	domainAllowed := func(rawURL string) bool {
		if len(allowed) == 0 {
			return true
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			return false
		}
		_, ok := allowed[u.Host]
		return ok
	}

	seen := make(map[string]bool)
	frontier := make([]urlEntry, 0, 32)

	// Seed the frontier with normalised, allowed seeds.
	for _, s := range c.opts.Seeds {
		norm, err := fetcher.NormalizeURL(s)
		if err != nil {
			log.Printf("skip seed (invalid): %s", s)
			continue
		}
		if seen[norm] || !domainAllowed(norm) {
			continue
		}
		seen[norm] = true
		frontier = append(frontier, urlEntry{url: norm, depth: 0})
	}

	// BFS loop. Sequential: pop one entry, fetch, enqueue its children.
	for len(frontier) > 0 && len(c.results) < c.opts.MaxPages {
		if ctx.Err() != nil {
			break
		}
		entry := frontier[0]
		frontier = frontier[1:]

		page := c.fetchAndParse(ctx, entry)
		if page == nil {
			continue
		}
		c.results = append(c.results, *page)

		// Enqueue outgoing links if we have depth budget.
		// MaxDepth semantics:
		//   <0  -> follow links forever (no limit)
		//   0   -> seeds only (no recursion)
		//   >=1 -> follow links this many levels deep
		if c.opts.MaxDepth < 0 || entry.depth < c.opts.MaxDepth {
			for _, link := range page.Links {
				norm, err := fetcher.NormalizeURL(link)
				if err != nil {
					continue
				}
				if seen[norm] || !domainAllowed(norm) {
					continue
				}
				seen[norm] = true
				frontier = append(frontier, urlEntry{url: norm, depth: entry.depth + 1})
			}
		}
	}

	return c.results, nil
}

// fetchAndParse does the actual GET + parse for one URL.
func (c *Crawler) fetchAndParse(ctx context.Context, entry urlEntry) *Page {
	start := time.Now()
	body, status, err := c.fetcher.Get(ctx, entry.url)
	dur := time.Since(start)
	if err != nil {
		log.Printf("skip %s: %v", entry.url, err)
		return nil
	}
	if status >= 400 {
		log.Printf("skip %s: HTTP %d", entry.url, status)
		return nil
	}
	isHTML := looksLikeHTML(body)
	var links []string
	var title string
	if isHTML {
		title = parser.ExtractTitle(body)
		links = parser.ExtractLinks(body, entry.url)
	}
	u, _ := url.Parse(entry.url)
	host := ""
	if u != nil {
		host = u.Host
	}
	return &Page{
		URL:        entry.url,
		StatusCode: status,
		Title:      title,
		Links:      links,
		FetchedAt:  start,
		DurationMs: dur.Milliseconds(),
		Host:       host,
	}
}

// looksLikeHTML is a cheap pre-check.
func looksLikeHTML(body string) bool {
	s := body
	if len(s) > 512 {
		s = s[:512]
	}
	s = strings.ToLower(s)
	return strings.Contains(s, "<html") ||
		strings.Contains(s, "<!doctype html") ||
		strings.Contains(s, "<head") ||
		strings.Contains(s, "<body")
}

// Summary formats a short human-readable summary of the run.
func (c *Crawler) Summary() string {
	var totalLinks int
	for _, p := range c.results {
		totalLinks += len(p.Links)
	}
	return fmt.Sprintf("Crawled %d pages, %d total links discovered", len(c.results), totalLinks)
}

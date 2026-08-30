// Package fetcher handles HTTP fetching with per-host rate limiting,
// optional robots.txt respect, and configurable timeout / user agent.
package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Fetcher is a polite HTTP client tuned for crawling.
type Fetcher struct {
	client     *http.Client
	userAgent  string
	delayMs    int
	respectRobots bool
	robots     *robotsCache
	hostMu     sync.Mutex
	hostLocks  map[string]*time.Time
}

// Options configure the Fetcher.
type Options struct {
	UserAgent     string
	DelayMs       int
	TimeoutSeconds int
	RespectRobots bool
}

// New returns a Fetcher with sensible defaults applied.
func New(opts Options) *Fetcher {
	if opts.UserAgent == "" {
		opts.UserAgent = "go-web-crawler/0.1 (+https://github.com/qifani35/go-web-crawler)"
	}
	if opts.TimeoutSeconds <= 0 {
		opts.TimeoutSeconds = 15
	}
	return &Fetcher{
		client: &http.Client{
			Timeout: time.Duration(opts.TimeoutSeconds) * time.Second,
		},
		userAgent:     opts.UserAgent,
		delayMs:       opts.DelayMs,
		respectRobots: opts.RespectRobots,
		robots:        &robotsCache{rules: make(map[string]*robotRules)},
		hostLocks:     make(map[string]*time.Time),
	}
}

// Get fetches a single URL and returns the body. It enforces per-host
// politeness by sleeping before the request if the last request to the
// same host was less than DelayMs ago. robots.txt is checked when
// RespectRobots is true.
func (f *Fetcher) Get(ctx context.Context, rawURL string) (string, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", 0, errors.New("only http/https schemes supported")
	}
	host := u.Host

	if f.respectRobots {
		allowed, err := f.robots.isAllowed(ctx, f.client, f.userAgent, host, u.Path)
		if err != nil {
			return "", 0, fmt.Errorf("robots check: %w", err)
		}
		if !allowed {
			return "", 0, ErrDisallowedByRobots
		}
	}

	// Per-host polite delay (mutex ensures only one caller per host
	// is in this critical section at a time).
	f.hostMu.Lock()
	last, hasLast := f.hostLocks[host]
	if hasLast && f.delayMs > 0 {
		elapsed := time.Since(*last)
		wait := time.Duration(f.delayMs)*time.Millisecond - elapsed
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	now := time.Now()
	f.hostLocks[host] = &now
	f.hostMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5 MB cap
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("read body: %w", err)
	}
	return string(body), resp.StatusCode, nil
}

// ErrDisallowedByRobots is returned when robots.txt forbids the URL.
var ErrDisallowedByRobots = errors.New("disallowed by robots.txt")

// NormalizeURL returns an absolute, canonicalized URL suitable for dedup.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("url missing scheme or host")
	}
	// Drop fragment.
	u.Fragment = ""
	// Lower-case host.
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

// SameDomain reports whether both URLs share a registrable host.
func SameDomain(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Host, ub.Host)
}

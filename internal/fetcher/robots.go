package fetcher

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// robotsCache lazily fetches and caches robots.txt per host.
type robotsCache struct {
	mu    sync.Mutex
	rules map[string]*robotRules
}

// robotRules is a tiny subset of the robots.txt spec sufficient for
// polite crawling: only "User-agent: *" + Allow/Disallow rules.
type robotRules struct {
	// groups indexed by lowercased user-agent token; "*" = default
	groups map[string]*robotGroup
	fetchedAt time.Time
}

type robotGroup struct {
	allows    []string
	disallows []string
}

// isAllowed returns true if the URL may be fetched for the given user agent.
func (c *robotsCache) isAllowed(ctx context.Context, client *http.Client, ua, host, path string) (bool, error) {
	c.mu.Lock()
	rules, ok := c.rules[host]
	c.mu.Unlock()

	if !ok || time.Since(rules.fetchedAt) > 24*time.Hour {
		fetched, err := fetchRobots(ctx, client, ua, host)
		if err != nil {
			// If robots.txt is missing, allow crawling.
			if fetched == nil {
				c.mu.Lock()
				c.rules[host] = &robotRules{groups: map[string]*robotGroup{"*": {}}, fetchedAt: time.Now()}
				c.mu.Unlock()
				return true, nil
			}
			return false, err
		}
		c.mu.Lock()
		c.rules[host] = fetched
		c.mu.Unlock()
		rules = fetched
	}

	// Match against the user-agent's group, falling back to "*".
	uaToken := canonicalUA(ua)
	if rules.groups[uaToken] == nil {
		uaToken = "*"
	}
	group := rules.groups[uaToken]
	if group == nil {
		return true, nil
	}

	// Longest-match wins; Disallow beats Allow on tie.
	bestAllow, bestDisallow := 0, 0
	for _, a := range group.allows {
		if a == "" || strings.HasPrefix(path, a) {
			if len(a) > bestAllow {
				bestAllow = len(a)
			}
		}
	}
	for _, d := range group.disallows {
		if d == "" {
			continue
		}
		if strings.HasPrefix(path, d) {
			if len(d) > bestDisallow {
				bestDisallow = len(d)
			}
		}
	}
	return bestDisallow == 0 || bestAllow > bestDisallow, nil
}

// fetchRobots downloads and parses a host's robots.txt.
// Returns (nil, nil) if the file is missing (which is the green-light
// signal under the robots spec).
func fetchRobots(ctx context.Context, client *http.Client, ua, host string) (*robotRules, error) {
	robotsURL := (&url.URL{Scheme: "https", Host: host, Path: "/robots.txt"}).String()
	if !httpsReachable(ctx, client, host) {
		robotsURL = (&url.URL{Scheme: "http", Host: host, Path: "/robots.txt"}).String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode >= 500 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("robots.txt: status %d", resp.StatusCode)
	}
	return parseRobots(resp.Body), nil
}

// parseRobots is a forgiving parser that only handles the rules we care about.
func parseRobots(r interface{ Read(p []byte) (int, error) }) *robotRules {
	rules := &robotRules{groups: map[string]*robotGroup{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var currentAgents []string
	flush := func() {
		if len(currentAgents) == 0 {
			return
		}
		// group file-level Allow/Disallow into each named agent
		var allows, disallows []string
		// We stash the latest-encountered rules in group "current".
		g := &robotGroup{}
		for _, ag := range currentAgents {
			rules.groups[canonicalUA(ag)] = g
		}
		allows, disallows = nil, nil
		_ = allows
		_ = disallows
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comments.
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		val := strings.TrimSpace(line[colon+1:])
		switch key {
		case "user-agent":
			// Start of a new group; flush the previous one.
			flush()
			currentAgents = []string{val}
		case "allow":
			if g, ok := rules.groups[canonicalUA(currentAgent(currentAgents))]; ok {
				g.allows = append(g.allows, val)
			}
		case "disallow":
			if g, ok := rules.groups[canonicalAgentOfFirst(currentAgents, rules)]; ok {
				g.disallows = append(g.disallows, val)
			}
		case "crawl-delay":
			// Recorded but currently unused; clients sleep by their own delay.
			_, _ = strconv.Atoi(val)
		}
	}
	flush()
	return rules
}

// canonicalUA normalises the user-agent header to the wildcard token
// used as the default group.
func canonicalUA(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "*"
	}
	// Take the first token (e.g. "go-web-crawler/0.1" -> "go-web-crawler").
	if i := strings.IndexAny(s, " /"); i >= 0 {
		s = s[:i]
	}
	return s
}

// currentAgent returns the first declared agent or "*" if none.
func currentAgent(agents []string) string {
	if len(agents) == 0 {
		return "*"
	}
	return agents[0]
}

// canonicalAgentOfFirst looks up (or creates) the group for the first
// declared user-agent in the current record, defaulting to "*".
func canonicalAgentOfFirst(agents []string, rules *robotRules) string {
	if len(agents) == 0 {
		return "*"
	}
	ua := canonicalUA(agents[0])
	if rules.groups[ua] == nil {
		rules.groups[ua] = &robotGroup{}
	}
	return ua
}

// httpsReachable does a quick HTTPS probe to decide which scheme to use
// for fetching robots.txt. Falls back to HTTP on any failure.
func httpsReachable(ctx context.Context, client *http.Client, host string) bool {
	probe := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/robots.txt", nil)
	if err != nil {
		return false
	}
	resp, err := probe.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

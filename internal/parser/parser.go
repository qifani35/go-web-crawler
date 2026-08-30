// Package parser extracts useful information from raw HTML: page title
// and all <a href> links (resolved to absolute URLs).
package parser

import (
	"net/url"
	"regexp"
	"strings"
)

// titleRe captures the contents of the first <title>...</title> tag.
var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// hrefRe captures href attribute values from <a> tags.
// Conservative: ignores tags inside <script>/<style>/<noscript> (best-effort).
var hrefRe = regexp.MustCompile(`(?i)<a\s[^>]*?href\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)

// linkRe captures <link href="..."> too (helps when sites skip <a>).
var linkRe = regexp.MustCompile(`(?i)<link\s[^>]*?href\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)

// ExtractTitle returns the page title (empty string if none).
func ExtractTitle(html string) string {
	m := titleRe.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(stripTags(m[1]))
}

// ExtractLinks returns absolute URLs for every <a href> and <link href>
// found in the HTML, resolved against the page URL. Invalid / empty
// hrefs and non-http(s) schemes are dropped.
func ExtractLinks(html, base string) []string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string

	scan := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(html, -1) {
			href := pickHref(m)
			if href == "" {
				continue
			}
			abs, ok := resolveURL(baseURL, href)
			if !ok {
				continue
			}
			if _, dup := seen[abs]; dup {
				continue
			}
			seen[abs] = struct{}{}
			out = append(out, abs)
		}
	}
	scan(hrefRe)
	scan(linkRe)
	return out
}

func pickHref(m []string) string {
	// m[1] is the full quoted-or-bare value; groups 2/3/4 are the inner.
	if len(m) < 5 {
		return ""
	}
	if m[2] != "" {
		return m[2]
	}
	if m[3] != "" {
		return m[3]
	}
	return m[4]
}

func resolveURL(base *url.URL, href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}
	// Drop in-page anchors only ("#section") — keep "#"+"path" as-is.
	if strings.HasPrefix(href, "#") {
		return "", false
	}
	// Drop obviously non-navigable schemes.
	low := strings.ToLower(href)
	for _, bad := range []string{"javascript:", "mailto:", "tel:", "data:", "sms:"} {
		if strings.HasPrefix(low, bad) {
			return "", false
		}
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return "", false
	}
	// Drop fragment.
	abs.Fragment = ""
	return abs.String(), true
}

func stripTags(s string) string {
	// Strip nested tags from a title (rare, but some pages do it).
	r := regexp.MustCompile(`<[^>]*>`)
	return r.ReplaceAllString(s, "")
}

// Package output writes crawl results to disk in one of three formats.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/qifani35/go-web-crawler/internal/crawler"
)

// Write serialises pages to w in the requested format.
// Supported formats: "json" (line-delimited), "csv", "text".
func Write(w io.Writer, format string, pages []crawler.Page) error {
	switch strings.ToLower(format) {
	case "json":
		return writeJSON(w, pages)
	case "csv":
		return writeCSV(w, pages)
	case "text", "":
		return writeText(w, pages)
	default:
		return fmt.Errorf("unknown output format %q (use json|csv|text)", format)
	}
}

// WriteToFile writes to a file path. If path is "-", writes to stdout.
func WriteToFile(path, format string, pages []crawler.Page) error {
	if path == "-" {
		return Write(os.Stdout, format, pages)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return Write(f, format, pages)
}

func writeJSON(w io.Writer, pages []crawler.Page) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Wrap in an object so consumers can also see the total count.
	return enc.Encode(map[string]any{
		"count":  len(pages),
		"pages":  pages,
	})
}

func writeCSV(w io.Writer, pages []crawler.Page) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"url", "status", "title", "host", "duration_ms", "fetched_at", "link_count"}); err != nil {
		return err
	}
	for _, p := range pages {
		if err := cw.Write([]string{
			p.URL,
			fmt.Sprintf("%d", p.StatusCode),
			p.Title,
			p.Host,
			fmt.Sprintf("%d", p.DurationMs),
			p.FetchedAt.Format("2006-01-02T15:04:05Z07:00"),
			fmt.Sprintf("%d", len(p.Links)),
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeText(w io.Writer, pages []crawler.Page) error {
	for i, p := range pages {
		if _, err := fmt.Fprintf(w, "[%d] %s\n", i+1, p.URL); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "    title:   %s\n", p.Title); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "    status:  %d\n    host:    %s\n    fetched: %s (%dms)\n",
			p.StatusCode, p.Host, p.FetchedAt.Format("2006-01-02 15:04:05"), p.DurationMs); err != nil {
			return err
		}
		if len(p.Links) > 0 {
			if _, err := fmt.Fprintf(w, "    links:   %d\n", len(p.Links)); err != nil {
				return err
			}
			// Show up to 5 sample links for compactness.
			n := len(p.Links)
			if n > 5 {
				n = 5
			}
			for _, l := range p.Links[:n] {
				if _, err := fmt.Fprintf(w, "      - %s\n", l); err != nil {
					return err
				}
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

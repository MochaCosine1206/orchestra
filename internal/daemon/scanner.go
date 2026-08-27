package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// SignalSource defines a feed to scan for discoveries.
type SignalSource struct {
	Name      string `json:"name"`
	Type      string `json:"type"`      // "rss", "api", "web"
	URL       string `json:"url"`
	Frequency string `json:"frequency"` // "daily", "weekly", "monthly"
	Category  string `json:"category"`  // "ai-devtools", "academic", "startup", etc.
}

// Discovery represents a single item found by scanning a signal source.
type Discovery struct {
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	URL          string    `json:"url"`
	Source       string    `json:"source"`        // signal source name
	Category     string    `json:"category"`
	DiscoveredAt time.Time `json:"discovered_at"`
	ContentHash  string    `json:"content_hash"`  // SHA256(source + title) for dedup
}

// DiscoveryScanner reads signal source feeds and creates work_items for new discoveries.
type DiscoveryScanner struct {
	DB           *db.DB
	Logger       *slog.Logger
	LastScanFile string // path to store last scan timestamps
	client       *http.Client
}

// NewDiscoveryScanner creates a scanner with a 10-second HTTP timeout.
func NewDiscoveryScanner(database *db.DB, logger *slog.Logger, lastScanFile string) *DiscoveryScanner {
	return &DiscoveryScanner{
		DB:           database,
		Logger:       logger,
		LastScanFile: lastScanFile,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ContentHash computes SHA256(source + title) for deduplication.
func ContentHash(source, title string) string {
	h := sha256.Sum256([]byte(source + title))
	return fmt.Sprintf("%x", h)
}

// ScanAll scans all sources, returns new discoveries.
func (s *DiscoveryScanner) ScanAll(ctx context.Context, sources []SignalSource) ([]Discovery, error) {
	times, err := LoadLastScanTimes(s.LastScanFile)
	if err != nil {
		s.Logger.Warn("could not load last scan times, using zero time", "error", err)
		times = make(map[string]time.Time)
	}

	var all []Discovery
	for _, src := range sources {
		since := times[src.Name]
		var discoveries []Discovery
		var scanErr error

		switch src.Type {
		case "rss":
			discoveries, scanErr = s.ScanRSS(ctx, src.URL, since)
		case "api":
			if strings.Contains(src.URL, "hacker-news") || src.Name == "hackernews" {
				discoveries, scanErr = s.ScanHackerNews(ctx, since)
			} else {
				s.Logger.Warn("unknown API source, skipping", "name", src.Name)
				continue
			}
		case "web":
			if strings.Contains(src.URL, "github.com/trending") {
				discoveries, scanErr = s.ScanGitHubTrending(ctx)
			} else {
				s.Logger.Warn("unknown web source, skipping", "name", src.Name)
				continue
			}
		default:
			s.Logger.Warn("unknown source type, skipping", "type", src.Type, "name", src.Name)
			continue
		}

		if scanErr != nil {
			s.Logger.Error("scan failed", "source", src.Name, "error", scanErr)
			continue
		}

		// Tag discoveries with source metadata.
		for i := range discoveries {
			discoveries[i].Source = src.Name
			discoveries[i].Category = src.Category
			if discoveries[i].ContentHash == "" {
				discoveries[i].ContentHash = ContentHash(src.Name, discoveries[i].Title)
			}
		}

		all = append(all, discoveries...)
		times[src.Name] = time.Now()
	}

	if err := SaveLastScanTimes(s.LastScanFile, times); err != nil {
		s.Logger.Error("could not save last scan times", "error", err)
	}

	return all, nil
}

// rssXML models an RSS 2.0 or Atom feed for XML parsing.
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

// atomFeed models an Atom feed.
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string    `xml:"title"`
	Link    atomLink  `xml:"link"`
	Summary string    `xml:"summary"`
	Updated string    `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
}

// ScanRSS fetches an RSS/Atom feed, returns items newer than since.
func (s *DiscoveryScanner) ScanRSS(ctx context.Context, url string, since time.Time) ([]Discovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating RSS request for %s: %w", url, err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching RSS feed %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RSS feed %s returned status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading RSS body from %s: %w", url, err)
	}

	var discoveries []Discovery

	// Try RSS 2.0 first.
	var rss rssFeed
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		for _, item := range rss.Channel.Items {
			pubTime := parseRSSDate(item.PubDate)
			if !since.IsZero() && !pubTime.IsZero() && !pubTime.After(since) {
				continue
			}
			discoveries = append(discoveries, Discovery{
				Title:        item.Title,
				Description:  truncate(item.Description, 500),
				URL:          item.Link,
				DiscoveredAt: pubTime,
			})
		}
		return discoveries, nil
	}

	// Try Atom.
	var atom atomFeed
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		for _, entry := range atom.Entries {
			pubTime := parseAtomDate(entry.Updated)
			if !since.IsZero() && !pubTime.IsZero() && !pubTime.After(since) {
				continue
			}
			discoveries = append(discoveries, Discovery{
				Title:        entry.Title,
				Description:  truncate(entry.Summary, 500),
				URL:          entry.Link.Href,
				DiscoveredAt: pubTime,
			})
		}
		return discoveries, nil
	}

	return nil, fmt.Errorf("could not parse feed from %s as RSS or Atom", url)
}

// hnTopStories is the response from HN Firebase top stories endpoint.
type hnItem struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
	Time  int64  `json:"time"`
	Type  string `json:"type"`
}

const (
	hnTopStoriesURL = "https://hacker-news.firebaseio.com/v0/topstories.json"
	hnItemURL       = "https://hacker-news.firebaseio.com/v0/item/%d.json"
	hnMaxStories    = 30
)

// ScanHackerNews fetches HN top stories via Firebase API, returns items newer than since.
func (s *DiscoveryScanner) ScanHackerNews(ctx context.Context, since time.Time) ([]Discovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hnTopStoriesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HN top stories request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching HN top stories: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HN top stories returned status %d", resp.StatusCode)
	}

	var ids []int
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, fmt.Errorf("decoding HN top story IDs: %w", err)
	}

	// Limit to top N stories.
	if len(ids) > hnMaxStories {
		ids = ids[:hnMaxStories]
	}

	var discoveries []Discovery
	for _, id := range ids {
		item, err := s.fetchHNItem(ctx, id)
		if err != nil {
			s.Logger.Warn("failed to fetch HN item", "id", id, "error", err)
			continue
		}
		if item.Type != "story" || item.Title == "" {
			continue
		}

		itemTime := time.Unix(item.Time, 0)
		if !since.IsZero() && !itemTime.After(since) {
			continue
		}

		desc := item.Text
		if desc == "" && item.URL != "" {
			desc = item.URL
		}

		discoveries = append(discoveries, Discovery{
			Title:        item.Title,
			Description:  truncate(desc, 500),
			URL:          item.URL,
			DiscoveredAt: itemTime,
		})
	}

	return discoveries, nil
}

func (s *DiscoveryScanner) fetchHNItem(ctx context.Context, id int) (*hnItem, error) {
	url := fmt.Sprintf(hnItemURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HN item request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching HN item %d: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HN item %d returned status %d", id, resp.StatusCode)
	}

	var item hnItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("decoding HN item %d: %w", id, err)
	}
	return &item, nil
}

// ScanGitHubTrending fetches trending repos from GitHub.
// NOTE: GitHub has no official trending API. This is a placeholder that logs
// the limitation and returns an empty result. A proper implementation would
// need HTML scraping or a third-party API.
func (s *DiscoveryScanner) ScanGitHubTrending(ctx context.Context) ([]Discovery, error) {
	s.Logger.Info("GitHub trending scan: no official API available, skipping (needs HTML scraper)")
	return nil, nil
}

// FilterNew deduplicates discoveries against existing work_items using content hash.
func (s *DiscoveryScanner) FilterNew(ctx context.Context, discoveries []Discovery) ([]Discovery, error) {
	if len(discoveries) == 0 {
		return nil, nil
	}

	// Build a set of existing content hashes.
	rows, err := s.DB.QueryContext(ctx, `SELECT source_id FROM work_items WHERE source = 'discovery'`)
	if err != nil {
		return nil, fmt.Errorf("querying existing discovery hashes: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("scanning discovery hash: %w", err)
		}
		existing[hash] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating discovery hashes: %w", err)
	}

	var filtered []Discovery
	for _, d := range discoveries {
		if !existing[d.ContentHash] {
			filtered = append(filtered, d)
		}
	}
	return filtered, nil
}

// IngestDiscoveries creates work_items for each new discovery.
// Returns the count of items inserted.
func (s *DiscoveryScanner) IngestDiscoveries(ctx context.Context, discoveries []Discovery) (int, error) {
	if len(discoveries) == 0 {
		return 0, nil
	}

	count := 0
	err := s.DB.WithTx(ctx, func(tx *sql.Tx) error {
		for _, d := range discoveries {
			id := db.GenID("disc")
			desc := d.Description
			if desc == "" {
				desc = d.URL
			}

			_, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO work_items (
					id, source, source_id, title, description, status, created_at
				) VALUES (?, 'discovery', ?, ?, ?, 'pending', ?)`,
				id, d.ContentHash, d.Title, desc, d.DiscoveredAt.UTC().Format(time.RFC3339),
			)
			if err != nil {
				return fmt.Errorf("inserting discovery %q: %w", d.Title, err)
			}
			count++
			s.Logger.Info("ingested discovery", "title", d.Title, "source", d.Source, "hash", d.ContentHash[:12])
		}
		return nil
	})
	if err != nil {
		return count, err
	}

	return count, nil
}

// LoadLastScanTimes reads a JSON file mapping source name to last scan time.
func LoadLastScanTimes(path string) (map[string]time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]time.Time), nil
		}
		return nil, fmt.Errorf("reading last scan times from %s: %w", path, err)
	}

	var times map[string]time.Time
	if err := json.Unmarshal(data, &times); err != nil {
		return nil, fmt.Errorf("parsing last scan times from %s: %w", path, err)
	}
	return times, nil
}

// SaveLastScanTimes writes a JSON file mapping source name to last scan time.
func SaveLastScanTimes(path string, times map[string]time.Time) error {
	data, err := json.MarshalIndent(times, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling last scan times: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing last scan times to %s: %w", path, err)
	}
	return nil
}

// parseRSSDate attempts to parse common RSS date formats.
func parseRSSDate(s string) time.Time {
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC3339,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseAtomDate attempts to parse Atom date formats.
func parseAtomDate(s string) time.Time {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// NOTE: truncate() is defined in decisions.go and reused here.

package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/config"
	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Collector struct {
	repository *Repository
	client     *http.Client
	accountID  string
	sources    []config.SourceConfig
}

func NewCollector(
	repository *Repository,
	client *http.Client,
	accountID string,
	sources []config.SourceConfig,
) *Collector {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Collector{
		repository: repository, client: client,
		accountID: accountID, sources: sources,
	}
}

func (c *Collector) Collect(ctx context.Context) (int, error) {
	total := 0
	for _, sourceConfig := range c.sources {
		if !sourceConfig.Enabled {
			continue
		}
		items, err := c.fetch(ctx, sourceConfig)
		if err != nil {
			return total, err
		}
		sourceID, err := c.repository.EnsureSource(
			ctx, c.accountID, sourceConfig.Name, sourceConfig.FeedURL,
		)
		if err != nil {
			return total, err
		}
		for _, item := range items {
			created, err := c.repository.SaveItem(ctx, sourceID, item)
			if err != nil {
				return total, err
			}
			if created {
				total++
			}
		}
	}
	return total, nil
}

func (c *Collector) fetch(
	ctx context.Context,
	sourceConfig config.SourceConfig,
) ([]Item, error) {
	parsedURL, err := url.Parse(sourceConfig.FeedURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return nil, fmt.Errorf("source %s must use an absolute HTTPS feed URL", sourceConfig.Name)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceConfig.FeedURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "X-AI-Operations/1.0 feed-reader")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch source %s: %w", sourceConfig.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s returned HTTP %d", sourceConfig.Name, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", sourceConfig.Name, err)
	}
	return parseFeed(body)
}

type feedDocument struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Author      string `xml:"author"`
	PubDate     string `xml:"pubDate"`
}

type atomEntry struct {
	Title   string `xml:"title"`
	Summary string `xml:"summary"`
	Content string `xml:"content"`
	Author  struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
}

func parseFeed(body []byte) ([]Item, error) {
	var document feedDocument
	if err := xml.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("parse feed XML: %w", err)
	}
	var items []Item
	for _, entry := range document.Channel.Items {
		summary := sanitizeText(entry.Description)
		items = append(items, newItem(entry.Title, entry.Link, entry.Author, summary, entry.PubDate))
	}
	for _, entry := range document.Entries {
		link := ""
		for _, candidate := range entry.Links {
			if candidate.Rel == "" || candidate.Rel == "alternate" {
				link = candidate.Href
				break
			}
		}
		summary := entry.Summary
		if summary == "" {
			summary = entry.Content
		}
		published := entry.Published
		if published == "" {
			published = entry.Updated
		}
		items = append(items, newItem(entry.Title, link, entry.Author.Name, sanitizeText(summary), published))
	}
	return items, nil
}

func newItem(title, link, author, summary, published string) Item {
	sum := sha256.Sum256([]byte(title + "\x00" + summary))
	return Item{
		ID: id.New("source_item"), URL: strings.TrimSpace(link),
		Author: strings.TrimSpace(author), Title: sanitizeText(title),
		Summary: summary, ContentHash: hex.EncodeToString(sum[:]),
		PublishedAt: parseFeedTime(published), FetchedAt: time.Now().UTC(),
	}
}

func sanitizeText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 4000 {
		value = value[:4000]
	}
	return value
}

func parseFeedTime(value string) *time.Time {
	formats := []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822}
	for _, format := range formats {
		parsed, err := time.Parse(format, strings.TrimSpace(value))
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

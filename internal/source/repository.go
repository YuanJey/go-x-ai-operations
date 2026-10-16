package source

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Item struct {
	ID          string     `json:"id"`
	SourceID    string     `json:"source_id"`
	URL         string     `json:"url"`
	Author      string     `json:"author,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	ContentHash string     `json:"content_hash"`
	FetchedAt   time.Time  `json:"fetched_at"`
	LicenseType string     `json:"license_type,omitempty"`
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) EnsureSource(
	ctx context.Context,
	accountID, name, baseURL string,
) (string, error) {
	var sourceID string
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM sources
		WHERE account_id = ? AND name = ? AND base_url = ?
		LIMIT 1`,
		accountID, name, baseURL,
	).Scan(&sourceID)
	if err == nil {
		return sourceID, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("find source: %w", err)
	}
	sourceID = id.New("source")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO sources(
			id, account_id, name, base_url, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, 1, ?, ?)`,
		sourceID, accountID, name, baseURL, now, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert source: %w", err)
	}
	return sourceID, nil
}

func (r *Repository) SaveItem(
	ctx context.Context,
	sourceID string,
	item Item,
) (bool, error) {
	if item.ID == "" {
		item.ID = id.New("source_item")
	}
	if item.FetchedAt.IsZero() {
		item.FetchedAt = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO source_items(
			id, source_id, url, author, published_at, title, summary,
			content_hash, fetched_at, license_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, url, content_hash) DO NOTHING`,
		item.ID, sourceID, item.URL, nullableString(item.Author),
		nullableTime(item.PublishedAt), item.Title, item.Summary,
		item.ContentHash, formatTime(item.FetchedAt), nullableString(item.LicenseType),
	)
	if err != nil {
		return false, fmt.Errorf("save source item: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

func (r *Repository) Recent(
	ctx context.Context,
	accountID string,
	limit int,
) ([]Item, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT si.id, si.source_id, si.url, si.author, si.published_at,
			si.title, si.summary, si.content_hash, si.fetched_at, si.license_type
		FROM source_items si
		JOIN sources s ON s.id = si.source_id
		WHERE s.account_id = ? AND s.enabled = 1
		ORDER BY COALESCE(si.published_at, si.fetched_at) DESC
		LIMIT ?`,
		accountID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list source items: %w", err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var item Item
		var author, publishedAt, licenseType sql.NullString
		var fetchedAt string
		if err := rows.Scan(
			&item.ID, &item.SourceID, &item.URL, &author, &publishedAt,
			&item.Title, &item.Summary, &item.ContentHash, &fetchedAt, &licenseType,
		); err != nil {
			return nil, err
		}
		if author.Valid {
			item.Author = author.String
		}
		if publishedAt.Valid {
			value, err := time.Parse(time.RFC3339Nano, publishedAt.String)
			if err != nil {
				return nil, err
			}
			item.PublishedAt = &value
		}
		item.FetchedAt, err = time.Parse(time.RFC3339Nano, fetchedAt)
		if err != nil {
			return nil, err
		}
		if licenseType.Valid {
			item.LicenseType = licenseType.String
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

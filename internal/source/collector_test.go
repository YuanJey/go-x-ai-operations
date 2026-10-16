package source

import (
	"strings"
	"testing"
)

func TestParseRSSAndAtom(t *testing.T) {
	rss := `<?xml version="1.0"?>
	<rss><channel><item>
		<title>Release 1</title><link>https://example.com/1</link>
		<description>  Reliable   jobs  </description>
		<author>Team</author><pubDate>Fri, 16 Oct 2026 10:00:00 +0000</pubDate>
	</item></channel></rss>`
	items, err := parseFeed([]byte(rss))
	if err != nil {
		t.Fatalf("parse RSS: %v", err)
	}
	if len(items) != 1 || items[0].Summary != "Reliable jobs" || items[0].PublishedAt == nil {
		t.Fatalf("unexpected RSS items: %+v", items)
	}

	atom := `<?xml version="1.0"?>
	<feed xmlns="http://www.w3.org/2005/Atom"><entry>
		<title>Release 2</title><link rel="alternate" href="https://example.com/2"/>
		<summary>Outbox consistency</summary><author><name>Team</name></author>
		<updated>2026-10-16T11:00:00Z</updated>
	</entry></feed>`
	items, err = parseFeed([]byte(atom))
	if err != nil {
		t.Fatalf("parse Atom: %v", err)
	}
	if len(items) != 1 || items[0].URL != "https://example.com/2" {
		t.Fatalf("unexpected Atom items: %+v", items)
	}
}

func TestSanitizeTextBoundsUntrustedInput(t *testing.T) {
	value := sanitizeText(strings.Repeat("a", 5000))
	if len(value) != 4000 {
		t.Fatalf("sanitized length = %d, want 4000", len(value))
	}
}

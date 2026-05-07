package tour

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BlogFeedURL is the canonical RSS feed for entire.io.
const BlogFeedURL = "https://entire.io/feed.xml"

// blogFetchTimeout caps how long --latest waits on the feed before
// bailing. The user already pays for the agent call after this; keeping
// the fetch tight avoids stacking two long waits.
const blogFetchTimeout = 8 * time.Second

// blogFetchMaxBytes caps how much of the feed body we'll read. RSS feeds
// in practice are well under a MiB; the cap exists so a malformed,
// hijacked, or unbounded response can't pull arbitrary memory into the
// CLI process.
const blogFetchMaxBytes = 5 << 20 // 5 MiB

// BlogPost is the subset of a feed <item> the agent prompt cares about.
type BlogPost struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	PubDate     string `json:"pub_date,omitempty"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content,omitempty"`
}

// rssEnvelope models the bits of RSS 2.0 we read. We deliberately keep
// the schema permissive — extra/unknown elements are ignored, and we
// match content:encoded on its local name so the standard xmlns prefix
// shape is enough.
type rssEnvelope struct {
	XMLName xml.Name    `xml:"rss"`
	Channel rssChannel  `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
	// content:encoded — encoding/xml resolves namespaces when we declare
	// the full namespace URL on the field tag. The W3C content namespace
	// is the canonical one used by virtually every RSS publisher.
	Encoded string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
}

// errNoBlogPosts is returned when the feed parses successfully but
// contains no items.
var errNoBlogPosts = errors.New("entire blog feed contains no posts")

// FetchLatestBlogPost GETs the configured feed URL and returns the first
// (most recent) <item>. The HTTP client uses a hard timeout, so this
// won't hang `entire tour --latest` when the feed is slow or
// unreachable; the body is also size-capped so a malformed feed can't
// pull unbounded memory.
var FetchLatestBlogPost = defaultFetchLatestBlogPost

func defaultFetchLatestBlogPost(ctx context.Context) (*BlogPost, error) {
	ctx, cancel := context.WithTimeout(ctx, blogFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BlogFeedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build feed request: %w", err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.8, */*;q=0.5")
	req.Header.Set("User-Agent", "entire-cli")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", BlogFeedURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // body close errors are not actionable here

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %s", BlogFeedURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, blogFetchMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("read feed body: %w", err)
	}

	var envelope rssEnvelope
	if err := xml.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	if len(envelope.Channel.Items) == 0 {
		return nil, errNoBlogPosts
	}

	first := envelope.Channel.Items[0]
	return &BlogPost{
		Title:       strings.TrimSpace(first.Title),
		Link:        strings.TrimSpace(first.Link),
		PubDate:     strings.TrimSpace(first.PubDate),
		Description: strings.TrimSpace(first.Description),
		Content:     strings.TrimSpace(first.Encoded),
	}, nil
}

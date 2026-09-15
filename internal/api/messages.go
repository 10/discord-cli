package api

import (
	"encoding/json"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Attachment struct {
	ID           string   `json:"id"`
	Filename     string   `json:"filename"`
	Size         int64    `json:"size"`
	URL          string   `json:"url"`
	ProxyURL     string   `json:"proxy_url,omitempty"`
	ContentType  string   `json:"content_type,omitempty"`
	Width        *int     `json:"width,omitempty"`
	Height       *int     `json:"height,omitempty"`
	Title        *string  `json:"title,omitempty"`
	Description  *string  `json:"description,omitempty"`
	DurationSecs *float64 `json:"duration_secs,omitempty"`
	Waveform     *string  `json:"waveform,omitempty"`
	Flags        *uint64  `json:"flags,omitempty"`
	Ephemeral    *bool    `json:"ephemeral,omitempty"`
}
type Emoji struct {
	ID       *string `json:"id"`
	Name     string  `json:"name"`
	Animated bool    `json:"animated,omitempty"`
}
type Reaction struct {
	Count        int             `json:"count"`
	Me           bool            `json:"me"`
	Emoji        Emoji           `json:"emoji"`
	CountDetails json.RawMessage `json:"count_details,omitempty"`
}
type Reference struct {
	MessageID string `json:"message_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	ServerID  string `json:"guild_id,omitempty"`
	Type      int    `json:"type,omitempty"`
}
type Message struct {
	ID                string          `json:"id"`
	ChannelID         string          `json:"channel_id"`
	Author            User            `json:"author"`
	Content           string          `json:"content"`
	Timestamp         string          `json:"timestamp"`
	EditedTimestamp   *string         `json:"edited_timestamp"`
	Reference         *Reference      `json:"message_reference,omitempty"`
	ReferencedMessage json.RawMessage `json:"referenced_message,omitempty"`
	Attachments       []Attachment    `json:"attachments"`
	Reactions         []Reaction      `json:"reactions"`
	Hit               *bool           `json:"hit,omitempty"`
	Type              *int            `json:"type,omitempty"`
	Flags             *uint64         `json:"flags,omitempty"`
	Pinned            *bool           `json:"pinned,omitempty"`
	MentionEveryone   *bool           `json:"mention_everyone,omitempty"`
	WebhookID         string          `json:"webhook_id,omitempty"`
	// Preserve received content without interpreting its evolving nested schemas.
	Embeds           json.RawMessage `json:"embeds,omitempty"`
	Components       json.RawMessage `json:"components,omitempty"`
	Poll             json.RawMessage `json:"poll,omitempty"`
	MessageSnapshots json.RawMessage `json:"message_snapshots,omitempty"`
	StickerItems     json.RawMessage `json:"sticker_items,omitempty"`
	Mentions         json.RawMessage `json:"mentions,omitempty"`
	MentionRoles     json.RawMessage `json:"mention_roles,omitempty"`
	MentionChannels  json.RawMessage `json:"mention_channels,omitempty"`
	Thread           json.RawMessage `json:"thread,omitempty"`
}

func validMessage(m Message) bool {
	if !ID(m.ID) || !ID(m.ChannelID) || !ID(m.Author.ID) {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, m.Timestamp); e != nil {
		return false
	}
	for _, a := range m.Attachments {
		if !ID(a.ID) || a.Size < 0 || a.Filename == "" || a.URL == "" {
			return false
		}
	}
	return true
}
func (c *Client) history(channel string, limit int, before, after, around string) ([]Message, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	for k, v := range map[string]string{"before": before, "after": after, "around": around} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var items []Message
	if err := c.Request("GET", "/channels/"+channel+"/messages?"+q.Encode(), nil, &items, false); err != nil {
		return nil, err
	}
	if len(items) > limit {
		return nil, Upstream()
	}
	for _, m := range items {
		if !validMessage(m) || m.ChannelID != channel {
			return nil, Upstream()
		}
	}
	if items == nil {
		items = []Message{}
	}
	return items, nil
}
func (c *Client) History(channel string, limit int, before, after string) (any, error) {
	if _, e := c.Channel(channel); e != nil {
		return nil, e
	}
	items, err := c.history(channel, limit, before, after, "")
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return lessID(items[j].ID, items[i].ID) })
	p := Page{Items: items, HasMore: len(items) == limit, Complete: true, Order: "newest_first"}
	if len(items) > 0 {
		p.NextBefore = items[len(items)-1].ID
		p.NextAfter = items[0].ID
	}
	return p, nil
}
func (c *Client) Message(channel, message string) (*Message, error) {
	items, err := c.history(channel, 1, "", "", message)
	if err != nil {
		return nil, err
	}
	if len(items) != 1 || items[0].ID != message {
		return nil, HTTPFailure(404)
	}
	return &items[0], nil
}
func (c *Client) GetMessage(channel, message string) (any, error) {
	if _, e := c.Channel(channel); e != nil {
		return nil, e
	}
	return c.Message(channel, message)
}

const MaxSearchOffset = 9975

type SearchOptions struct {
	Server, Channel, Content, Before, After, Sort string
	Channels, Authors, Mentions, Has, AuthorTypes []string
	Pinned                                        bool
	Limit, Offset                                 int
}

func (c *Client) Search(options SearchOptions) (any, error) {
	server, channel := options.Server, options.Channel
	limit, offset := options.Limit, options.Offset
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}, "sort_by": {"timestamp"}, "sort_order": {"desc"}}
	for k, v := range map[string]string{"content": options.Content, "max_id": options.Before, "min_id": options.After} {
		if v != "" {
			q.Set(k, v)
		}
	}
	for k, values := range map[string][]string{"author_id": options.Authors, "mentions": options.Mentions, "channel_id": options.Channels, "author_type": options.AuthorTypes} {
		for _, value := range values {
			q.Add(k, value)
		}
	}
	for _, value := range options.Has {
		q.Add("has", strings.Replace(value, "forward", "snapshot", 1))
	}
	if options.Pinned {
		q.Set("pinned", "true")
	}
	order := "newest_first"
	switch options.Sort {
	case "oldest":
		q.Set("sort_order", "asc")
		order = "oldest_first"
	case "relevance":
		q.Set("sort_by", "relevance")
		order = "relevance"
	}
	path := "/guilds/" + server + "/messages/search"
	if channel != "" {
		ch, e := c.Channel(channel)
		if e != nil {
			return nil, e
		}
		if ch.ServerID != "" {
			path = "/guilds/" + ch.ServerID + "/messages/search"
			q.Set("channel_id", channel)
		} else {
			path = "/channels/" + channel + "/messages/search"
		}
	}
	var result struct {
		Total    *int        `json:"total_results"`
		Messages [][]Message `json:"messages"`
		Indexed  *bool       `json:"is_indexed"`
		Deep     bool        `json:"doing_deep_historical_index"`
	}
	if err := c.Request("GET", path+"?"+q.Encode(), nil, &result, false); err != nil {
		return nil, err
	}
	if result.Total == nil || *result.Total < 0 || result.Messages == nil {
		return nil, Upstream()
	}
	items := []Message{}
	seen := map[string]bool{}
	for _, group := range result.Messages {
		for _, m := range group {
			if !validMessage(m) {
				return nil, Upstream()
			}
			if m.Hit != nil && !*m.Hit {
				continue
			}
			if channel != "" && m.ChannelID != channel {
				return nil, Upstream()
			}
			if len(options.Channels) > 0 && !slices.Contains(options.Channels, m.ChannelID) {
				return nil, Upstream()
			}
			if !seen[m.ID] {
				items = append(items, m)
				seen[m.ID] = true
			}
		}
	}
	if len(items) > limit {
		return nil, Upstream()
	}
	complete := (result.Indexed == nil || *result.Indexed) && !result.Deep
	if !complete && len(items) == 0 {
		e := HTTPFailure(202)
		e.Details = map[string]any{"complete": false, "offset": offset}
		return nil, e
	}
	if options.Sort != "relevance" {
		sort.Slice(items, func(i, j int) bool {
			if options.Sort == "oldest" {
				return lessID(items[i].ID, items[j].ID)
			}
			return lessID(items[j].ID, items[i].ID)
		})
	}
	more := offset+len(items) < *result.Total || !complete
	p := Page{Items: items, Complete: complete, HasMore: more, Order: order, TotalResults: result.Total}
	if more {
		// ponytail: keep count-based continuation; short pages cannot promise completeness.
		if len(items) < limit {
			p.Complete = false
		}
		next := offset + len(items)
		if next > MaxSearchOffset || len(items) == 0 {
			p.ContinuationLimited = true
			p.Complete = false
		} else {
			p.NextOffset = &next
		}
	}
	return p, nil
}

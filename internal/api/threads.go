package api

import (
	"net/url"
	"sort"
	"strconv"
	"time"
)

type ThreadMetadata struct {
	Archived            *bool   `json:"archived"`
	ArchiveTimestamp    string  `json:"archive_timestamp"`
	AutoArchiveDuration int     `json:"auto_archive_duration"`
	Locked              bool    `json:"locked"`
	Invitable           *bool   `json:"invitable,omitempty"`
	CreateTimestamp     *string `json:"create_timestamp,omitempty"`
}

type Thread struct {
	Channel
	OwnerID        string          `json:"owner_id,omitempty"`
	MessageCount   *int            `json:"message_count,omitempty"`
	MemberCount    *int            `json:"member_count,omitempty"`
	AppliedTags    []string        `json:"applied_tags,omitempty"`
	Flags          *uint64         `json:"flags,omitempty"`
	ThreadMetadata *ThreadMetadata `json:"thread_metadata"`
}

func validThread(t Thread) bool {
	if !validChannel(t.Channel) || t.Name == "" || !ID(t.ServerID) || t.ParentID == nil || !ID(*t.ParentID) || t.ThreadMetadata == nil || t.ThreadMetadata.Archived == nil {
		return false
	}
	if *t.Type != 10 && *t.Type != 11 && *t.Type != 12 {
		return false
	}
	_, e := time.Parse(time.RFC3339Nano, t.ThreadMetadata.ArchiveTimestamp)
	return e == nil
}

func (c *Client) ActiveThreads(channel string, limit int, before string) (any, error) {
	// The guild-wide active endpoint is bot-only; user sessions use channel search.
	q := url.Values{"archived": {"false"}, "sort_by": {"creation_time"}, "sort_order": {"desc"}, "limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("max_id", before)
	}
	var result struct {
		Threads []Thread `json:"threads"`
		HasMore *bool    `json:"has_more"`
		Total   *int     `json:"total_results"`
	}
	if e := c.Request("GET", "/channels/"+channel+"/threads/search?"+q.Encode(), nil, &result, false); e != nil {
		return nil, e
	}
	if result.Threads == nil || result.HasMore == nil || len(result.Threads) > limit || (result.Total != nil && *result.Total < 0) {
		return nil, Upstream()
	}
	for _, t := range result.Threads {
		if !validThread(t) || *t.ParentID != channel || *t.ThreadMetadata.Archived || (before != "" && !lessID(t.ID, before)) {
			return nil, Upstream()
		}
	}
	sort.Slice(result.Threads, func(i, j int) bool { return lessID(result.Threads[j].ID, result.Threads[i].ID) })
	p := Page{Items: result.Threads, HasMore: *result.HasMore, Complete: true, Order: "id_descending", TotalResults: result.Total}
	if p.HasMore {
		if len(result.Threads) < limit {
			p.Complete = false
		}
		if len(result.Threads) == 0 {
			p.ContinuationLimited = true
		} else {
			p.NextBefore = result.Threads[len(result.Threads)-1].ID
		}
	}
	return p, nil
}

func (c *Client) ArchivedThreads(channel, kind string, limit int, before string) (any, error) {
	path := "/channels/" + channel + "/threads/archived/" + kind
	if kind == "joined-private" {
		path = "/channels/" + channel + "/users/@me/threads/archived/private"
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	var result struct {
		Threads []Thread `json:"threads"`
		HasMore *bool    `json:"has_more"`
	}
	if e := c.Request("GET", path+"?"+q.Encode(), nil, &result, false); e != nil {
		return nil, e
	}
	if result.Threads == nil || result.HasMore == nil || len(result.Threads) > limit {
		return nil, Upstream()
	}
	for _, t := range result.Threads {
		if !validThread(t) || *t.ParentID != channel || !*t.ThreadMetadata.Archived {
			return nil, Upstream()
		}
		if (kind == "public" && *t.Type == 12) || (kind != "public" && *t.Type != 12) {
			return nil, Upstream()
		}
	}
	order := "archive_timestamp_descending"
	if kind == "joined-private" {
		order = "id_descending"
	}
	sort.Slice(result.Threads, func(i, j int) bool {
		if kind != "joined-private" {
			a, _ := time.Parse(time.RFC3339Nano, result.Threads[i].ThreadMetadata.ArchiveTimestamp)
			b, _ := time.Parse(time.RFC3339Nano, result.Threads[j].ThreadMetadata.ArchiveTimestamp)
			if !a.Equal(b) {
				return a.After(b)
			}
		}
		return lessID(result.Threads[j].ID, result.Threads[i].ID)
	})
	p := Page{Items: result.Threads, HasMore: *result.HasMore, Complete: true, Order: order}
	if p.HasMore {
		if len(result.Threads) == 0 {
			p.ContinuationLimited = true
			p.Complete = false
		} else {
			last := result.Threads[len(result.Threads)-1]
			p.NextBefore = last.ThreadMetadata.ArchiveTimestamp
			if kind == "joined-private" {
				p.NextBefore = last.ID
			}
		}
	}
	return p, nil
}

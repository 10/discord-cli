package api

import "sort"

type HistoryExport struct {
	Page
	PagesRead  int    `json:"pages_read"`
	StopReason string `json:"stop_reason"`
}

// ExportHistory walks older history within explicit message/page budgets and ID bounds.
func (c *Client) ExportHistory(channel, before, after string, maxMessages, maxPages int) (*HistoryExport, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	items := []Message{}
	result := &HistoryExport{Page: Page{Order: "newest_first"}}
	seen := map[string]bool{}
	cursor := before
	for result.PagesRead < maxPages && len(items) < maxMessages {
		limit := min(100, maxMessages-len(items))
		page, err := c.history(channel, limit, cursor, "", "")
		if err != nil {
			return nil, err
		}
		result.PagesRead++
		sort.Slice(page, func(i, j int) bool { return lessID(page[j].ID, page[i].ID) })
		for _, m := range page {
			if cursor != "" && !lessID(m.ID, cursor) {
				return nil, Upstream()
			}
			if after != "" && !lessID(after, m.ID) {
				result.Complete = true
				result.StopReason = "after_bound"
				break
			}
			if !seen[m.ID] {
				items = append(items, m)
				seen[m.ID] = true
			}
		}
		if result.Complete {
			break
		}
		if len(page) < limit {
			result.Complete = true
			result.StopReason = "exhausted"
			break
		}
		cursor = page[len(page)-1].ID
		if result.PagesRead == maxPages {
			result.StopReason = "max_pages"
		}
		if len(items) == maxMessages {
			result.StopReason = "max_messages"
		}
	}
	result.Items = items
	result.HasMore = !result.Complete
	if len(items) > 0 {
		result.NextAfter = items[0].ID
		result.NextBefore = items[len(items)-1].ID
	}
	return result, nil
}

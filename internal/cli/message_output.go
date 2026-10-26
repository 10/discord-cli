package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/10/discord-cli/internal/api"
)

type MessageOutput struct {
	Format string `default:"json" enum:"json,transcript" help:"Output format: original JSON or a chronological text transcript."`
}

func lessMessageID(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func (o MessageOutput) print(ctx *Context, value any) error {
	if o.Format == "json" {
		return ctx.Print(value)
	}
	var page api.Page
	metadata := map[string]any{}
	switch v := value.(type) {
	case api.Page:
		page = v
	case *api.Message:
		page = api.Page{Items: []api.Message{*v}, Complete: true, Order: "exact"}
	default:
		return api.Upstream()
	}
	items, ok := page.Items.([]api.Message)
	if !ok {
		return api.Upstream()
	}
	metadata["message_count"], metadata["complete"], metadata["has_more"], metadata["order"] = len(items), page.Complete, page.HasMore, page.Order
	if page.NextBefore != "" {
		metadata["next_before"] = page.NextBefore
	}
	if page.NextAfter != "" {
		metadata["next_after"] = page.NextAfter
	}
	if page.NextOffset != nil {
		metadata["next_offset"] = *page.NextOffset
	}
	if page.TotalResults != nil {
		metadata["total_results"] = *page.TotalResults
	}
	if page.ContinuationLimited {
		metadata["continuation_limited"] = true
	}
	items = append([]api.Message(nil), items...)
	sort.Slice(items, func(i, j int) bool { return lessMessageID(items[i].ID, items[j].ID) })
	metadata["order"] = "oldest_first"
	var out strings.Builder
	text := func(label, value string) {
		value = label + value
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return '\uFFFD'
			}
			return r
		}, value)
		fmt.Fprintf(&out, "  %s\n", strings.ReplaceAll(value, "\n", "\n  "))
	}
	for _, m := range items {
		name := m.Author.Username
		if m.Author.GlobalName != nil && *m.Author.GlobalName != "" {
			name = *m.Author.GlobalName
		}
		fmt.Fprintf(&out, "[%s] %q (@%q; user=%s) message=%s channel=%s\n", m.Timestamp, name, m.Author.Username, m.Author.ID, m.ID, m.ChannelID)
		if m.Reference != nil && m.Reference.MessageID != "" {
			text("reply_to: ", m.Reference.MessageID)
		}
		if m.Content != "" {
			text("", m.Content)
		}
		var embeds []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			URL         string `json:"url"`
			Fields      []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"fields"`
		}
		if len(m.Embeds) > 0 && json.Unmarshal(m.Embeds, &embeds) == nil {
			for _, e := range embeds {
				if e.Title != "" {
					text("embed: ", e.Title)
				}
				if e.Description != "" {
					text("", e.Description)
				}
				if e.URL != "" {
					text("link: ", e.URL)
				}
				for _, f := range e.Fields {
					text(f.Name+": ", f.Value)
				}
			}
		} else if len(m.Embeds) > 0 {
			text("embeds: ", string(m.Embeds))
		}
		for _, a := range m.Attachments {
			text("attachment: ", fmt.Sprintf("%q id=%s type=%q size=%d", a.Filename, a.ID, a.ContentType, a.Size))
		}
		for _, field := range []struct {
			name  string
			value json.RawMessage
		}{
			{"components", m.Components}, {"poll", m.Poll}, {"forwarded", m.MessageSnapshots}, {"stickers", m.StickerItems},
		} {
			if len(field.value) > 0 && string(field.value) != "null" && string(field.value) != "[]" {
				text(field.name+": ", string(field.value))
			}
		}
		out.WriteByte('\n')
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	fmt.Fprintf(&out, "[coverage] %s\n", encoded)
	_, err = io.WriteString(ctx.Stdout, redact(out.String(), ctx.Secrets))
	return err
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
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
	author := func(m api.Message) string {
		name := m.Author.Username
		if m.Author.GlobalName != nil && *m.Author.GlobalName != "" {
			name = *m.Author.GlobalName
		}
		return fmt.Sprintf("[%s] %q (@%q; user=%s)", m.Timestamp, name, m.Author.Username, m.Author.ID)
	}
	attachment := func(label string, a api.Attachment) {
		text(label, fmt.Sprintf("%q id=%s type=%q size=%d", a.Filename, a.ID, a.ContentType, a.Size))
	}
	// Quote a referenced message once, only when the transcript lacks it.
	quoted := map[string]bool{}
	for _, m := range items {
		quoted[m.ID] = true
	}
	for _, m := range items {
		fmt.Fprintf(&out, "%s message=%s channel=%s", author(m), m.ID, m.ChannelID)
		if m.Type != nil && *m.Type != 0 && *m.Type != 19 {
			fmt.Fprintf(&out, " type=%d", *m.Type)
		}
		if m.EditedTimestamp != nil {
			if _, err := time.Parse(time.RFC3339Nano, *m.EditedTimestamp); err == nil {
				fmt.Fprintf(&out, " edited=%s", *m.EditedTimestamp)
			}
		}
		out.WriteByte('\n')
		if ref := m.Reference; ref != nil && ref.MessageID != "" {
			source := fmt.Sprintf("message=%s channel=%s", ref.MessageID, ref.ChannelID)
			switch {
			case ref.Type == 1:
				text("forwarded_from: ", source)
			case m.Type == nil || *m.Type == 19:
				text("reply_to: ", ref.MessageID)
			case *m.Type == 21:
				text("thread_starter: ", source)
			default:
				text("references: ", source)
			}
			var original api.Message
			if !quoted[ref.MessageID] && json.Unmarshal(m.ReferencedMessage, &original) == nil && original.ID == ref.MessageID {
				quoted[original.ID] = true
				text("> ", author(original))
				if original.Content != "" {
					text("> ", strings.ReplaceAll(original.Content, "\n", "\n> "))
				}
				for _, a := range original.Attachments {
					attachment("> attachment: ", a)
				}
			}
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
			attachment("attachment: ", a)
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
		if len(m.Reactions) > 0 {
			counts := make([]string, len(m.Reactions))
			for i, r := range m.Reactions {
				emoji := r.Emoji.Name
				if r.Emoji.ID != nil {
					emoji += ":" + *r.Emoji.ID
				}
				counts[i] = fmt.Sprintf("%s %d", emoji, r.Count)
			}
			text("reactions: ", strings.Join(counts, ", "))
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

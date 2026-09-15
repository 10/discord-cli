package api

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func EmojiValue(value string) (string, error) {
	if strings.HasPrefix(value, "<:") || strings.HasPrefix(value, "<a:") {
		value = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(value, "<a:"), "<:"), ">")
	}
	if strings.Contains(value, ":") {
		name, id, ok := strings.Cut(value, ":")
		if !ok || !ID(id) || len(name) < 2 || len(name) > 32 {
			return "", Invalid("custom emoji must be name:id or <:name:id>")
		}
		for _, r := range name {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return "", Invalid("invalid custom emoji name")
			}
		}
		return name + ":" + id, nil
	}
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > 128 {
		return "", Invalid("provide a Unicode emoji or custom name:id")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '?' || r == '#' || r == '%' {
			return "", Invalid("invalid emoji")
		}
	}
	return value, nil
}
func (c *Client) Reactions(channel, message string) (any, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	m, err := c.Message(channel, message)
	if err != nil {
		return nil, err
	}
	items := m.Reactions
	if items == nil {
		items = []Reaction{}
	}
	return map[string]any{"channel_id": channel, "message_id": message, "items": items}, nil
}
func (c *Client) ReactionUsers(channel, message, emoji, after string, limit int, kind int) (any, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "type": {strconv.Itoa(kind)}}
	if after != "" {
		q.Set("after", after)
	}
	var users []User
	if err := c.Request("GET", reactionPath(channel, message, emoji)+"?"+q.Encode(), nil, &users, false); err != nil {
		return nil, err
	}
	if len(users) > limit {
		return nil, Upstream()
	}
	for _, u := range users {
		if !ID(u.ID) {
			return nil, Upstream()
		}
	}
	sort.Slice(users, func(i, j int) bool { return lessID(users[i].ID, users[j].ID) })
	if users == nil {
		users = []User{}
	}
	p := Page{Items: users, HasMore: len(users) == limit, Complete: true, Order: "id_ascending"}
	if p.HasMore {
		p.NextAfter = users[len(users)-1].ID
	}
	return p, nil
}
func reactionPath(channel, message, emoji string) string {
	return "/channels/" + channel + "/messages/" + message + "/reactions/" + url.PathEscape(emoji)
}
func (c *Client) React(channel, message, emoji, user string, add bool) (any, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	if user == "" {
		user = "@me"
	}
	method, operation := "DELETE", "remove_reaction"
	if add {
		method, operation = "PUT", "add_reaction"
	}
	if err := c.Request(method, reactionPath(channel, message, emoji)+"/"+user, nil, nil, false); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "operation": operation, "channel_id": channel, "message_id": message, "emoji": emoji, "user_id": user}, nil
}

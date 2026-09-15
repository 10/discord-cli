package api

import (
	"net/url"
	"sort"
	"strconv"
)

type Server struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Channel struct {
	ID            string  `json:"id"`
	Type          *int    `json:"type"`
	Name          string  `json:"name,omitempty"`
	ServerID      string  `json:"guild_id,omitempty"`
	ParentID      *string `json:"parent_id,omitempty"`
	Recipients    []User  `json:"recipients,omitempty"`
	LastMessageID *string `json:"last_message_id,omitempty"`
}
type Page struct {
	ContinuationLimited bool   `json:"continuation_limited,omitempty"`
	Items               any    `json:"items"`
	HasMore             bool   `json:"has_more"`
	Complete            bool   `json:"complete"`
	Order               string `json:"order"`
	NextBefore          string `json:"next_before,omitempty"`
	NextAfter           string `json:"next_after,omitempty"`
	NextOffset          *int   `json:"next_offset,omitempty"`
	TotalResults        *int   `json:"total_results,omitempty"`
}

func (c *Client) Servers(limit int, after string) (any, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("after", after)
	}
	var items []Server
	if err := c.Request("GET", "/users/@me/guilds?"+q.Encode(), nil, &items, false); err != nil {
		return nil, err
	}
	for _, v := range items {
		if !ID(v.ID) || v.Name == "" {
			return nil, Upstream()
		}
	}
	sort.Slice(items, func(i, j int) bool { return lessID(items[i].ID, items[j].ID) })
	if items == nil {
		items = []Server{}
	}
	p := Page{Items: items, HasMore: len(items) == limit, Complete: true, Order: "id_ascending"}
	if p.HasMore {
		p.NextAfter = items[len(items)-1].ID
	}
	return p, nil
}
func lessID(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
func validChannel(v Channel) bool {
	return ID(v.ID) && v.Type != nil && (v.ServerID == "" || ID(v.ServerID))
}
func (c *Client) Channels(server string, private bool) (any, error) {
	path := "/guilds/" + server + "/channels"
	if private {
		path = "/users/@me/channels"
	}
	var items []Channel
	if err := c.Request("GET", path, nil, &items, false); err != nil {
		return nil, err
	}
	for _, v := range items {
		if !validChannel(v) {
			return nil, Upstream()
		}
		for _, u := range v.Recipients {
			if !ID(u.ID) {
				return nil, Upstream()
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return lessID(items[i].ID, items[j].ID) })
	if items == nil {
		items = []Channel{}
	}
	return Page{Items: items, Complete: true, Order: "id_ascending"}, nil
}
func (c *Client) Channel(id string) (*Channel, error) {
	var v Channel
	if err := c.Request("GET", "/channels/"+id, nil, &v, false); err != nil {
		return nil, err
	}
	if !validChannel(v) || v.ID != id {
		return nil, Upstream()
	}
	switch *v.Type {
	case 0, 1, 2, 3, 5, 10, 11, 12, 13:
		return &v, nil
	default:
		return nil, Invalid("this channel is a container, not a message destination")
	}
}
func (c *Client) Friends() (any, error) {
	var relationships []struct {
		Type int  `json:"type"`
		User User `json:"user"`
	}
	if err := c.Request("GET", "/users/@me/relationships", nil, &relationships, false); err != nil {
		return nil, err
	}
	friends := []User{}
	for _, r := range relationships {
		if !ID(r.User.ID) {
			return nil, Upstream()
		}
		if r.Type == 1 {
			friends = append(friends, r.User)
		}
	}
	sort.Slice(friends, func(i, j int) bool { return lessID(friends[i].ID, friends[j].ID) })
	return Page{Items: friends, Complete: true, Order: "id_ascending"}, nil
}
func (c *Client) OpenDM(user string) (any, error) {
	var channel Channel
	if err := c.Request("POST", "/users/@me/channels", JSONBody(map[string]any{"recipients": []string{user}}), &channel, true); err != nil {
		return nil, err
	}
	if !validChannel(channel) || *channel.Type != 1 || len(channel.Recipients) != 1 || channel.Recipients[0].ID != user {
		return nil, uncertain(200, "/users/@me/channels")
	}
	return channel, nil
}

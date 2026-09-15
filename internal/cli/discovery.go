package cli

import "github.com/10/discord-cli/internal/api"

type ServersCmd struct {
	List ServersListCmd `cmd:"" help:"List servers by ascending ID; use next_after to continue."`
}
type ServersListCmd struct {
	Limit int    `default:"100" help:"Maximum servers (1–200)."`
	After string `help:"Return server IDs after this cursor."`
}

func (c *ServersListCmd) Validate() error {
	if c.Limit < 1 || c.Limit > 200 {
		return api.Invalid("--limit must be 1–200")
	}
	if c.After != "" {
		return api.RequireIDs(c.After)
	}
	return nil
}
func (c *ServersListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.Servers(c.Limit, c.After)
	return ctx.Result(v, e)
}

type ChannelsCmd struct {
	List ChannelsListCmd `cmd:"" help:"List one server's channels by ID, including container types."`
}
type ChannelsListCmd struct {
	Server string `arg:"" help:"Server ID."`
}

func (c *ChannelsListCmd) Validate() error { return api.RequireIDs(c.Server) }
func (c *ChannelsListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.Channels(c.Server, false)
	return ctx.Result(v, e)
}

type DMsCmd struct {
	List DMsListCmd `cmd:"" help:"List existing DMs and group DMs by ID; does not open conversations."`
	Open DMOpenCmd  `cmd:"" help:"Explicitly open a one-to-one DM for a user ID."`
}
type DMsListCmd struct{}

func (c *DMsListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.Channels("", true)
	return ctx.Result(v, e)
}

type DMOpenCmd struct {
	User string `arg:"" help:"Recipient user ID."`
}

func (c *DMOpenCmd) Validate() error { return api.RequireIDs(c.User) }
func (c *DMOpenCmd) Run(ctx *Context) error {
	v, e := ctx.Client.OpenDM(c.User)
	return ctx.Result(v, e)
}

type FriendsCmd struct {
	List FriendsListCmd `cmd:"" help:"List friends by user ID; does not open DMs."`
}
type FriendsListCmd struct{}

func (c *FriendsListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.Friends()
	return ctx.Result(v, e)
}
func (c *Context) Result(v any, e error) error {
	if e != nil {
		return e
	}
	return c.Print(v)
}

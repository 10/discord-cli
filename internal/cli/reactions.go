package cli

import "github.com/10/discord-cli/internal/api"

type ReactionsCmd struct {
	List   ReactionListCmd   `cmd:"" help:"List a message's reaction counts and emoji."`
	Users  ReactionUsersCmd  `cmd:"" help:"List accounts for one reaction, ascending by ID."`
	Add    ReactionAddCmd    `cmd:"" help:"Add your reaction."`
	Remove ReactionRemoveCmd `cmd:"" help:"Remove your or a specified account's reaction where permitted."`
}
type ReactionTarget struct {
	Channel string `arg:"" help:"Channel ID."`
	Message string `arg:"" help:"Message ID."`
	Emoji   string `arg:"" help:"Unicode emoji or custom name:id (also accepts <:name:id>)."`
}

func (c *ReactionTarget) Validate() error {
	if e := api.RequireIDs(c.Channel, c.Message); e != nil {
		return e
	}
	value, e := api.EmojiValue(c.Emoji)
	c.Emoji = value
	return e
}

type ReactionListCmd struct {
	Channel string `arg:"" help:"Channel ID."`
	Message string `arg:"" help:"Message ID."`
}

func (c *ReactionListCmd) Validate() error { return api.RequireIDs(c.Channel, c.Message) }
func (c *ReactionListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.Reactions(c.Channel, c.Message)
	return ctx.Result(v, e)
}

type ReactionUsersCmd struct {
	ReactionTarget
	Limit int    `default:"50" help:"Maximum accounts (1–100)."`
	After string `help:"User ID cursor."`
	Type  int    `default:"0" help:"Reaction type: 0 normal, 1 burst."`
}

func (c *ReactionUsersCmd) Validate() error {
	if e := c.ReactionTarget.Validate(); e != nil {
		return e
	}
	if c.Type < 0 || c.Type > 1 {
		return api.Invalid("--type must be 0 or 1")
	}
	return bounds(c.Limit, 100, "", c.After, true)
}
func (c *ReactionUsersCmd) Run(ctx *Context) error {
	v, e := ctx.Client.ReactionUsers(c.Channel, c.Message, c.Emoji, c.After, c.Limit, c.Type)
	return ctx.Result(v, e)
}

type ReactionAddCmd struct{ ReactionTarget }

func (c *ReactionAddCmd) Run(ctx *Context) error {
	v, e := ctx.Client.React(c.Channel, c.Message, c.Emoji, "", true)
	return ctx.Result(v, e)
}

type ReactionRemoveCmd struct {
	ReactionTarget
	User string `help:"User ID to moderate; omit to remove your own reaction."`
}

func (c *ReactionRemoveCmd) Validate() error {
	if e := c.ReactionTarget.Validate(); e != nil {
		return e
	}
	if c.User != "" {
		return api.RequireIDs(c.User)
	}
	return nil
}
func (c *ReactionRemoveCmd) Run(ctx *Context) error {
	v, e := ctx.Client.React(c.Channel, c.Message, c.Emoji, c.User, false)
	return ctx.Result(v, e)
}

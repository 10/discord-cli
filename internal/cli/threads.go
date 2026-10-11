package cli

import (
	"time"

	"github.com/10/discord-cli/internal/api"
)

type ThreadsCmd struct {
	List     ThreadsListCmd     `cmd:"" help:"Read one page of active threads in a parent channel, including forum posts."`
	Archived ThreadsArchivedCmd `cmd:"" help:"Read one archive page from a parent channel; use next_before to continue."`
}

type ThreadsListCmd struct {
	Channel string `arg:"" help:"Parent text, announcement, forum, or media channel ID."`
	Limit   int    `default:"25" help:"Maximum active threads, newest IDs first (1–25)."`
	Before  string `help:"Read threads before this thread ID; use next_before to continue."`
}

func (c *ThreadsListCmd) Validate() error {
	if e := api.RequireIDs(c.Channel); e != nil {
		return e
	}
	return bounds(c.Limit, 25, c.Before, "", true)
}
func (c *ThreadsListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.ActiveThreads(c.Channel, c.Limit, c.Before)
	return ctx.Result(v, e)
}

type ThreadsArchivedCmd struct {
	Channel string `arg:"" help:"Parent text, announcement, forum, or media channel ID."`
	Type    string `default:"public" enum:"public,private,joined-private" help:"Archive kind; private requires Manage Threads, joined-private only lists threads you joined."`
	Limit   int    `default:"50" help:"Maximum archived threads (2–100)."`
	Before  string `help:"Cursor: RFC3339 archive timestamp for public/private, thread ID for joined-private."`
}

func (c *ThreadsArchivedCmd) Validate() error {
	if e := api.RequireIDs(c.Channel); e != nil {
		return e
	}
	if c.Limit < 2 || c.Limit > 100 {
		return api.Invalid("--limit must be 2–100")
	}
	if c.Before != "" {
		if c.Type == "joined-private" {
			return api.RequireIDs(c.Before)
		}
		if _, e := time.Parse(time.RFC3339Nano, c.Before); e != nil {
			return api.Invalid("--before must be an RFC3339 archive timestamp for public/private threads")
		}
	}
	return nil
}
func (c *ThreadsArchivedCmd) Run(ctx *Context) error {
	v, e := ctx.Client.ArchivedThreads(c.Channel, c.Type, c.Limit, c.Before)
	return ctx.Result(v, e)
}

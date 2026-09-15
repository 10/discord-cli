package cli

import (
	"github.com/10/discord-cli/internal/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

type MessageListCmd struct {
	Channel string `arg:"" help:"Channel ID."`
	Limit   int    `default:"50" help:"Maximum messages, newest first (1–100)."`
	Before  string `help:"Read older messages before this message ID."`
	After   string `help:"Read newer messages after this message ID."`
}

func (c *MessageListCmd) Validate() error {
	if e := api.RequireIDs(c.Channel); e != nil {
		return e
	}
	return bounds(c.Limit, 100, c.Before, c.After, true)
}
func (c *MessageListCmd) Run(ctx *Context) error {
	v, e := ctx.Client.History(c.Channel, c.Limit, c.Before, c.After)
	return ctx.Result(v, e)
}

type MessageSearchCmd struct {
	Server     string   `help:"Search this server ID; mutually exclusive with --channel."`
	Channel    string   `help:"Search this channel, DM, group DM, or known thread ID."`
	InChannel  []string `sep:"none" help:"Restrict --server to these channel IDs; repeat to select multiple."`
	Query      string   `help:"Content to search for (maximum 1024 characters)."`
	Author     []string `sep:"none" help:"Restrict to these user IDs; repeat to select multiple (maximum 100)."`
	Mentions   []string `sep:"none" help:"Mentioned user IDs; repeat to select multiple (maximum 100)."`
	Has        []string `sep:"none" help:"Repeat: image, video, link, file, embed, sound, poll, sticker, forward (or snapshot). Prefix with - to exclude; use --has=-image."`
	AuthorType []string `sep:"none" help:"Repeat: user, bot, webhook; prefix with - to exclude (e.g. --author-type=-bot)."`
	Pinned     *bool    `help:"Only pinned messages. Explicit false is unsupported by Discord search."`
	Before     string   `help:"Only messages before this message ID; cannot combine ID and date bounds."`
	After      string   `help:"Only messages after this message ID; cannot combine ID and date bounds."`
	On         string   `help:"Messages on this local calendar day (YYYY-MM-DD); cannot combine with other bounds."`
	BeforeDate string   `help:"Messages before this local calendar day (YYYY-MM-DD), excluding that day."`
	AfterDate  string   `help:"Messages after this local calendar day (YYYY-MM-DD), excluding that day."`
	Sort       string   `default:"newest" enum:"newest,oldest,relevance" help:"Result order: newest, oldest, relevance."`
	Limit      int      `default:"25" help:"Maximum search hits (1–25)."`
	Offset     int      `default:"0" help:"Search result offset (0–9975); use next_offset to continue."`
}

// Prepare preserves actionable filter errors after parsing, before authentication.
func (c *MessageSearchCmd) Prepare(_ *Context) error {
	if (c.Server == "") == (c.Channel == "") {
		return api.Invalid("search requires exactly one of --server or --channel")
	}
	for _, id := range []string{c.Server, c.Channel} {
		if id != "" {
			if e := api.RequireIDs(id); e != nil {
				return e
			}
		}
	}
	if len(c.InChannel) > 0 && c.Server == "" {
		return api.Invalid("--in-channel requires --server")
	}
	for _, list := range []struct {
		ids []string
		max int
	}{{c.Author, 100}, {c.Mentions, 100}, {c.InChannel, 500}} {
		if len(list.ids) > list.max {
			return api.Invalid("too many IDs for this search filter")
		}
		if e := api.RequireIDs(list.ids...); e != nil {
			return e
		}
	}
	for _, filter := range []struct {
		values, allowed []string
	}{
		{c.Has, []string{"image", "video", "link", "file", "embed", "sound", "poll", "sticker", "forward", "snapshot"}},
		{c.AuthorType, []string{"user", "bot", "webhook"}},
	} {
		for _, value := range filter.values {
			if !slices.Contains(filter.allowed, strings.TrimPrefix(value, "-")) {
				return api.Invalid("unknown search filter value; use --help for supported values")
			}
		}
	}
	if !utf8.ValidString(c.Query) || utf8.RuneCountInString(c.Query) > 1024 {
		return api.Invalid("--query must be valid UTF-8 with at most 1024 characters")
	}
	if c.Pinned != nil && !*c.Pinned {
		return api.Invalid("Discord does not reliably exclude pinned messages; omit --pinned or use --pinned=true")
	}
	if c.Offset < 0 || c.Offset > api.MaxSearchOffset {
		return api.Invalid("--offset must be 0–9975")
	}
	before, after, e := c.searchBounds()
	if e != nil {
		return e
	}
	return bounds(c.Limit, 25, before, after, false)
}
func (c *MessageSearchCmd) Run(ctx *Context) error {
	before, after, e := c.searchBounds()
	if e != nil {
		return e
	}
	v, e := ctx.Client.Search(api.SearchOptions{
		Server: c.Server, Channel: c.Channel, Channels: c.InChannel, Content: c.Query,
		Authors: c.Author, Mentions: c.Mentions, Has: c.Has, AuthorTypes: c.AuthorType,
		Pinned: c.Pinned != nil && *c.Pinned, Before: before, After: after,
		Sort: c.Sort, Limit: c.Limit, Offset: c.Offset,
	})
	return ctx.Result(v, e)
}

func (c *MessageSearchCmd) searchBounds() (string, string, error) {
	if c.On == "" && c.BeforeDate == "" && c.AfterDate == "" {
		return c.Before, c.After, nil
	}
	if c.Before != "" || c.After != "" || (c.On != "" && (c.BeforeDate != "" || c.AfterDate != "")) {
		return "", "", api.Invalid("choose ID bounds, --on, or --before-date/--after-date")
	}
	epoch := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	bound := func(value string, nextDay, inclusive bool) (string, error) {
		if value == "" {
			return "", nil
		}
		day, e := time.ParseInLocation(time.DateOnly, value, time.Local)
		if e != nil || day.Format(time.DateOnly) != value {
			return "", api.Invalid("dates must be valid calendar days in YYYY-MM-DD format")
		}
		if nextDay {
			day = day.AddDate(0, 0, 1)
		}
		if day.Before(epoch) || day.After(epoch.Add(time.Duration(1<<42-1)*time.Millisecond)) {
			return "", api.Invalid("date is outside Discord's message-ID time range")
		}
		id := uint64(discord.NewSnowflake(day))
		if inclusive {
			if id == 0 {
				return "", nil
			}
			id-- // min_id is strict; include the first ID at midnight.
		}
		return strconv.FormatUint(id, 10), nil
	}
	beforeDate, afterDate := c.BeforeDate, c.AfterDate
	if c.On != "" {
		beforeDate, afterDate = c.On, c.On
	}
	before, e := bound(beforeDate, c.On != "", false)
	if e != nil {
		return "", "", e
	}
	after, e := bound(afterDate, c.On == "", true)
	return before, after, e
}
func bounds(limit, max int, before, after string, exclusive bool) error {
	if limit < 1 || limit > max {
		return api.Invalid("--limit is outside the range shown in --help")
	}
	if exclusive && before != "" && after != "" {
		return api.Invalid("choose --before or --after")
	}
	for _, id := range []string{before, after} {
		if id != "" {
			if e := api.RequireIDs(id); e != nil {
				return e
			}
		}
	}
	if !exclusive && before != "" && after != "" && (len(after) > len(before) || (len(after) == len(before) && after >= before)) {
		return api.Invalid("--after must precede --before")
	}
	return nil
}

type MessageGetCmd struct {
	Channel string `arg:"" help:"Channel ID."`
	Message string `arg:"" help:"Exact message ID."`
}

func (c *MessageGetCmd) Validate() error { return api.RequireIDs(c.Channel, c.Message) }
func (c *MessageGetCmd) Run(ctx *Context) error {
	v, e := ctx.Client.GetMessage(c.Channel, c.Message)
	return ctx.Result(v, e)
}

type TextFiles struct {
	Content *string  `help:"Exact text (maximum 2000 UTF-16 units); an empty value clears text when editing."`
	Stdin   bool     `help:"Read exact message text from standard input."`
	File    []string `sep:"none" help:"Explicit file path to attach; repeat for multiple files."`
}

func (c *TextFiles) validate() error {
	if c.Stdin && c.Content != nil {
		return api.Invalid("choose --content or --stdin")
	}
	if len(c.File) > 10 {
		return api.Invalid("at most 10 files may be attached")
	}
	if c.Content != nil && (!utf8.ValidString(*c.Content) || len(utf16.Encode([]rune(*c.Content))) > 2000) {
		return api.Invalid("message text must be valid UTF-8 and at most 2000 UTF-16 units")
	}
	return nil
}
func (c *TextFiles) prepare(ctx *Context, send bool) error {
	if c.Stdin {
		data, err := readInput(ctx.Context, ctx.Stdin, 8001)
		if err != nil {
			return err
		}
		text := string(data)
		c.Content = &text
		c.Stdin = false
	}
	if err := c.validate(); err != nil {
		return err
	}
	if send && len(c.File) == 0 && (c.Content == nil || strings.TrimSpace(*c.Content) == "") {
		return api.Invalid("provide nonempty text or an attachment")
	}
	files, err := api.OpenUploads(c.File)
	if err != nil {
		return err
	}
	ctx.Uploads = files
	return nil
}

type MessageSendCmd struct {
	Channel string `arg:"" help:"Destination channel ID."`
	TextFiles
	ReplyTo string `help:"Message ID to reply to in this channel."`
}

func (c *MessageSendCmd) Validate() error {
	if e := api.RequireIDs(c.Channel); e != nil {
		return e
	}
	if c.ReplyTo != "" {
		if e := api.RequireIDs(c.ReplyTo); e != nil {
			return e
		}
	}
	if !c.Stdin && c.Content == nil && len(c.File) == 0 {
		return api.Invalid("provide --content, --stdin, or --file")
	}
	return c.TextFiles.validate()
}
func (c *MessageSendCmd) Prepare(ctx *Context) error { return c.TextFiles.prepare(ctx, true) }
func (c *MessageSendCmd) Run(ctx *Context) error {
	v, e := ctx.Client.WriteMessage(c.Channel, "", c.Content, c.ReplyTo, ctx.Uploads, nil, ctx.Identity.ID)
	return ctx.Result(v, e)
}

type MessageEditCmd struct {
	Channel string `arg:"" help:"Channel ID."`
	Message string `arg:"" help:"Own message ID."`
	TextFiles
	RemoveAttachment []string `sep:"none" help:"Attachment ID to remove; repeat. All other attachments are retained."`
}

func (c *MessageEditCmd) Validate() error {
	if e := api.RequireIDs(append([]string{c.Channel, c.Message}, c.RemoveAttachment...)...); e != nil {
		return e
	}
	if !c.Stdin && c.Content == nil && len(c.File) == 0 && len(c.RemoveAttachment) == 0 {
		return api.Invalid("provide a text or attachment change")
	}
	return c.TextFiles.validate()
}
func (c *MessageEditCmd) Prepare(ctx *Context) error { return c.TextFiles.prepare(ctx, false) }
func (c *MessageEditCmd) Run(ctx *Context) error {
	v, e := ctx.Client.WriteMessage(c.Channel, c.Message, c.Content, "", ctx.Uploads, c.RemoveAttachment, ctx.Identity.ID)
	return ctx.Result(v, e)
}

type MessageDeleteCmd struct {
	Channel string `arg:"" help:"Channel ID."`
	Message string `arg:"" help:"Message ID."`
}

func (c *MessageDeleteCmd) Validate() error { return api.RequireIDs(c.Channel, c.Message) }
func (c *MessageDeleteCmd) Run(ctx *Context) error {
	v, e := ctx.Client.DeleteMessage(c.Channel, c.Message)
	return ctx.Result(v, e)
}

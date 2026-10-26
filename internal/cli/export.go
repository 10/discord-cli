package cli

import "github.com/10/discord-cli/internal/api"

type MessageExportCmd struct {
	Channel     string `arg:"" help:"Channel ID."`
	MaxMessages int    `default:"500" help:"Maximum messages to collect (1–10000)."`
	MaxPages    int    `default:"10" help:"Maximum history requests (1–100); pages contain at most 100 messages."`
	Before      string `help:"Read messages before this ID; use next_before to resume."`
	After       string `help:"Stop at this message ID, excluding it."`
	On          string `help:"Only this local calendar day (YYYY-MM-DD)."`
	BeforeDate  string `help:"Stop before this local calendar day, excluding that day."`
	AfterDate   string `help:"Read after this local calendar day, excluding that day."`
	MessageOutput
}

func (c *MessageExportCmd) Prepare(_ *Context) error {
	if err := api.RequireIDs(c.Channel); err != nil {
		return err
	}
	if c.MaxMessages < 1 || c.MaxMessages > 10000 || c.MaxPages < 1 || c.MaxPages > 100 {
		return api.Invalid("--max-messages must be 1–10000 and --max-pages must be 1–100")
	}
	filters := MessageSearchCmd{On: c.On, BeforeDate: c.BeforeDate, AfterDate: c.AfterDate}
	before, after, err := filters.searchBounds()
	if err != nil {
		return err
	}
	for _, id := range []string{c.Before, c.After} {
		if id != "" {
			if err := api.RequireIDs(id); err != nil {
				return err
			}
		}
	}
	if c.Before != "" && (before == "" || lessMessageID(c.Before, before)) {
		before = c.Before
	}
	if c.After != "" && (after == "" || lessMessageID(after, c.After)) {
		after = c.After
	}
	if err = bounds(1, 100, before, after, false); err != nil {
		return err
	}
	c.Before, c.After = before, after
	return nil
}

func (c *MessageExportCmd) Run(ctx *Context) error {
	result, err := ctx.Client.ExportHistory(c.Channel, c.Before, c.After, c.MaxMessages, c.MaxPages)
	if err != nil {
		return err
	}
	return c.MessageOutput.print(ctx, result)
}

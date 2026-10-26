package cli

import (
	"github.com/10/discord-cli/internal/api"
	"os"
)

type AttachmentsCmd struct {
	Download     AttachmentDownloadCmd     `cmd:"" help:"Download one selected message attachment into an explicit directory."`
	DownloadMany AttachmentDownloadManyCmd `cmd:"" help:"Download attachments from explicit messages; return a per-file manifest."`
}

type AttachmentDownloadManyCmd struct {
	Channel  string   `arg:"" help:"Channel ID."`
	Messages []string `arg:"" name:"message" help:"Explicit message IDs (1–100); all their attachments are selected."`
	Dir      string   `required:"" help:"Existing output directory; files are saved under message-ID/attachment-ID/."`
}

func (c *AttachmentDownloadManyCmd) Prepare(_ *Context) error {
	if len(c.Messages) < 1 || len(c.Messages) > 100 {
		return api.Invalid("provide 1–100 explicit message IDs")
	}
	if err := api.RequireIDs(append([]string{c.Channel}, c.Messages...)...); err != nil {
		return err
	}
	info, err := os.Stat(c.Dir)
	if err != nil || !info.IsDir() {
		return api.Invalid("--dir must name an existing directory")
	}
	return nil
}

func (c *AttachmentDownloadManyCmd) Run(ctx *Context) error {
	result, err := ctx.Client.DownloadMany(ctx.Context, c.Channel, c.Messages, c.Dir, ctx.DownloadTransport)
	if err != nil {
		return err
	}
	if err := ctx.Print(result); err != nil {
		return err
	}
	if !result.Complete {
		return &api.Error{Code: "partial_download", Message: "Some attachments could not be downloaded; inspect the per-file manifest on stdout", Details: map[string]int{"failed": result.Failed, "succeeded": result.Succeeded}}
	}
	return nil
}

type AttachmentDownloadCmd struct {
	Channel    string `arg:"" help:"Channel ID."`
	Message    string `arg:"" help:"Message ID."`
	Attachment string `arg:"" help:"Attachment ID from message metadata."`
	Dir        string `required:"" help:"Existing output directory; files are never overwritten."`
	Name       string `help:"Optional simple output filename (defaults to attachment filename)."`
}

func (c *AttachmentDownloadCmd) Validate() error {
	if e := api.RequireIDs(c.Channel, c.Message, c.Attachment); e != nil {
		return e
	}
	if c.Name != "" && !api.SafeFilename(c.Name) {
		return api.Invalid("--name must be a safe, simple filename")
	}
	info, e := os.Stat(c.Dir)
	if e != nil || !info.IsDir() {
		return api.Invalid("--dir must name an existing directory")
	}
	return nil
}
func (c *AttachmentDownloadCmd) Run(ctx *Context) error {
	v, e := ctx.Client.Download(ctx.Context, c.Channel, c.Message, c.Attachment, c.Dir, c.Name, ctx.DownloadTransport)
	return ctx.Result(v, e)
}

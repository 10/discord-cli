package cli

import (
	"github.com/10/discord-cli/internal/api"
	"os"
)

type AttachmentsCmd struct {
	Download AttachmentDownloadCmd `cmd:"" help:"Download one selected message attachment into an explicit directory."`
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

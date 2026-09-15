package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/10/discord-cli/internal/api"
	"github.com/10/discord-cli/internal/auth"
	"github.com/10/discord-cli/internal/config"
	"github.com/alecthomas/kong"
)

type Root struct {
	Attachments AttachmentsCmd `cmd:"" help:"Explicitly download attachments without forwarding credentials."`
	Reactions   ReactionsCmd   `cmd:"" help:"Inspect, add, and remove message reactions."`
	Servers     ServersCmd     `cmd:"" help:"Discover accessible servers."`
	Channels    ChannelsCmd    `cmd:"" help:"Discover server channels."`
	Dms         DMsCmd         `cmd:"" help:"Discover private conversations or open an explicit DM."`
	Friends     FriendsCmd     `cmd:"" help:"Discover friends and their user IDs."`

	Version  kong.VersionFlag `help:"Print version without authentication."`
	Timeout  time.Duration    `default:"60s" help:"Total command deadline, including authentication and transfers."`
	Auth     AuthCmd          `cmd:"" help:"Inspect identity or manage an optional token override."`
	Messages MessagesCmd      `cmd:"" help:"Read, search, and act on messages by explicit IDs."`
}
type MessagesCmd struct {
	Edit   MessageEditCmd   `cmd:"" help:"Edit own message text or explicitly add/remove attachments."`
	Delete MessageDeleteCmd `cmd:"" help:"Delete a message where Discord permits; no confirmation prompt."`
	List   MessageListCmd   `cmd:"" help:"Read a bounded history page, newest first."`
	Search MessageSearchCmd `cmd:"" help:"Search exactly one server or conversation; inspect complete and has_more."`
	Get    MessageGetCmd    `cmd:"" help:"Read exactly one message."`
	Send   MessageSendCmd   `cmd:"" help:"Send text, a reply, or explicit files."`
}
type Context struct {
	DownloadTransport   http.RoundTripper
	Uploads             []api.Upload
	ConfigPath, APIBase string
	context.Context
	Client         *api.Client
	Identity       *api.User
	Source         string
	Secrets        []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func (c *Context) Print(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = io.WriteString(c.Stdout, redact(string(data), c.Secrets)+"\n")
	return err
}

// Options contains only composition-time test substitutions; none are CLI flags.
type Options struct {
	DownloadTransport http.RoundTripper
	ConfigPath        string
	LookupEnv         func(string) (string, bool)
	APIBase           string
	DesktopPaths      []string
	TempDir           string
	DesktopKey        auth.KeyFunc
	DesktopPlatform   string
}

func Run(parent context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, opts Options) int {
	var root Root
	exited := -1
	parser, err := kong.New(&root, kong.Name("discord"), kong.Description("Bounded Discord account operations as JSON."), kong.Vars{"version": "discord dev"}, kong.Writers(stdout, io.Discard), kong.Exit(func(code int) { exited = code }))
	if err != nil {
		return report(stderr, err)
	}
	parsed, err := parser.Parse(args)
	if exited >= 0 {
		return exited
	}
	// Parser diagnostics may contain arbitrary arguments, including accidentally pasted tokens.
	if err != nil {
		return report(stderr, api.Invalid("invalid command or arguments; use --help for command syntax"))
	}
	if root.Timeout <= 0 {
		return report(stderr, api.Invalid("--timeout must be positive"))
	}
	ctx, cancel := context.WithTimeout(parent, root.Timeout)
	defer cancel()
	c := &Context{Context: ctx, Stdin: stdin, Stdout: stdout, Stderr: stderr, DownloadTransport: opts.DownloadTransport}
	if preparer, ok := parsed.Selected().Target.Addr().Interface().(interface{ Prepare(*Context) error }); ok {
		if err := preparer.Prepare(c); err != nil {
			return report(stderr, err)
		}
	}
	defer api.CloseUploads(c.Uploads)
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if strings.HasPrefix(parsed.Command(), "auth set-token") || strings.HasPrefix(parsed.Command(), "auth clear-token") {
		c.ConfigPath = opts.ConfigPath
		c.APIBase = opts.APIBase
		if c.ConfigPath == "" {
			c.ConfigPath, err = config.Path()
			if err != nil {
				return report(stderr, err)
			}
		}
	} else if err := c.Authenticate(opts); err != nil {
		return report(stderr, err, c.Secrets...)
	}
	returnCode := 0
	if err := parsed.Run(c); err != nil {
		returnCode = report(stderr, err, c.Secrets...)
	}
	return returnCode
}
func report(w io.Writer, err error, secrets ...string) int {
	var e *api.Error
	if !errors.As(err, &e) {
		e = &api.Error{Code: "runtime_error", Message: "command failed"}
	}
	data, marshalErr := json.Marshal(map[string]any{"error": e})
	if marshalErr != nil {
		return 1
	}
	if _, err := io.WriteString(w, redact(string(data), secrets)+"\n"); err != nil {
		return 1
	}
	return e.ExitCode()
}

// Readers at the command boundary can be pipes. A blocked input must not outlive
// the invocation deadline; the executable exits after Run returns.
func readInput(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { b, e := io.ReadAll(io.LimitReader(r, limit)); done <- result{b, e} }()
	select {
	case <-ctx.Done():
		return nil, &api.Error{Code: "cancelled", Message: "Command deadline or cancellation reached while reading input"}
	case got := <-done:
		if got.err != nil {
			return nil, api.Invalid("could not read standard input")
		}
		if int64(len(got.data)) >= limit {
			return nil, api.Invalid("standard input exceeds the supported size")
		}
		return got.data, nil
	}
}

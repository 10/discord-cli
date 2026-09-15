package cli

import (
	"github.com/10/discord-cli/internal/api"
	"github.com/10/discord-cli/internal/auth"
	"github.com/10/discord-cli/internal/config"
	"golang.org/x/term"
	"io"
	"os"
	"strings"
)

type AuthCmd struct {
	SetToken   AuthSetTokenCmd   `cmd:"" help:"Validate and save a token read from stdin or a hidden terminal prompt."`
	ClearToken AuthClearTokenCmd `cmd:"" help:"Remove only the saved override; no Discord access."`
	Status     AuthStatusCmd     `cmd:"" help:"Show resolved account identity and credential source."`
}
type AuthStatusCmd struct{}

func (c *AuthStatusCmd) Run(ctx *Context) error {
	return ctx.Print(map[string]any{"account": ctx.Identity, "source": ctx.Source})
}
func (c *Context) Authenticate(opts Options) error {
	candidates, err := auth.Candidates(c.Context, opts.LookupEnv, opts.ConfigPath, opts.DesktopPaths, opts.TempDir, opts.DesktopKey, opts.DesktopPlatform)
	if err != nil {
		return err
	}
	identities := map[string]bool{}
	var accounts []any
	for _, candidate := range candidates {
		client := api.New(c.Context, candidate.Token, opts.APIBase)
		c.Secrets = append(c.Secrets, candidate.Token)
		user, err := client.Me()
		if err != nil {
			return err
		}
		if !identities[user.ID] {
			identities[user.ID] = true
			accounts = append(accounts, map[string]any{"account": user, "source": candidate.Source})
		}
		if c.Client == nil {
			c.Client = client
			c.Identity = user
			c.Source = candidate.Source
		}
	}
	if len(identities) > 1 {
		return &api.Error{Code: "ambiguous_account", Message: "Multiple desktop accounts are signed in; select one with DISCORD_TOKEN or auth set-token", Details: accounts}
	}
	return nil
}
func redact(data string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			data = strings.ReplaceAll(data, secret, "[REDACTED]")
		}
	}
	return data
}

type AuthSetTokenCmd struct{}
type AuthClearTokenCmd struct{}

func (c *AuthSetTokenCmd) Run(ctx *Context) error {
	var data []byte
	var err error
	if f, ok := ctx.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		_, _ = io.WriteString(ctx.Stderr, "Discord token: ")
		state, stateErr := term.GetState(int(f.Fd()))
		if stateErr != nil {
			return api.Invalid("could not access terminal")
		}
		defer term.Restore(int(f.Fd()), state)
		type secretResult struct {
			data []byte
			err  error
		}
		done := make(chan secretResult, 1)
		go func() { b, e := term.ReadPassword(int(f.Fd())); done <- secretResult{b, e} }()
		select {
		case <-ctx.Done():
			return &api.Error{Code: "cancelled", Message: "Command deadline or cancellation reached while reading token"}
		case result := <-done:
			data, err = result.data, result.err
		}
		_, _ = io.WriteString(ctx.Stderr, "\n")
	} else {
		data, err = readInput(ctx.Context, ctx.Stdin, 4097)
	}
	if err != nil {
		return err
	}
	token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	clear(data)
	if err := auth.Validate(token); err != nil {
		return err
	}
	ctx.Secrets = append(ctx.Secrets, token)
	client := api.New(ctx.Context, token, ctx.APIBase)
	user, err := client.Me()
	if err != nil {
		return err
	}
	if err := config.Save(ctx.ConfigPath, token); err != nil {
		return err
	}
	return ctx.Print(map[string]any{"ok": true, "operation": "set_token", "account": user, "source": "configuration"})
}
func (c *AuthClearTokenCmd) Run(ctx *Context) error {
	if err := config.Clear(ctx.ConfigPath); err != nil {
		return err
	}
	return ctx.Print(map[string]any{"ok": true, "operation": "clear_token"})
}

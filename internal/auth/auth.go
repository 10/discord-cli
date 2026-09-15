package auth

import (
	"context"
	"regexp"

	"github.com/10/discord-cli/internal/api"
	"github.com/10/discord-cli/internal/config"
)

type Candidate struct {
	Token  string
	Source string
}

var tokenPattern = regexp.MustCompile(`^(?:[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|mfa\.[A-Za-z0-9_-]+)$`)

func Validate(token string) error {
	if len(token) > 4096 || !tokenPattern.MatchString(token) {
		return &api.Error{Code: "authentication_failed", Message: "The selected token is empty or malformed; supply a valid user token without a Bot or Bearer prefix"}
	}
	return nil
}
func Candidates(ctx context.Context, lookup func(string) (string, bool), path string, desktopPaths []string, tempDir string, key KeyFunc, platform string) ([]Candidate, error) {
	if token, set := lookup("DISCORD_TOKEN"); set {
		if err := Validate(token); err != nil {
			return nil, err
		}
		return []Candidate{{token, "environment"}}, nil
	}
	if path == "" {
		var err error
		path, err = config.Path()
		if err != nil {
			return nil, err
		}
	}
	token, set, err := config.Read(path)
	if err != nil {
		return nil, err
	}
	if set {
		if err := Validate(token); err != nil {
			return nil, err
		}
		return []Candidate{{token, "configuration"}}, nil
	}
	return Desktop(ctx, desktopPaths, tempDir, key, platform)
}

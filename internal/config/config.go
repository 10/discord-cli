package config

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/10/discord-cli/internal/api"
)

func Path() (string, error) {
	dir, e := os.UserConfigDir()
	if e != nil {
		return "", failure()
	}
	return filepath.Join(dir, "discord-cli", "config.json"), nil
}
func failure() error {
	return &api.Error{Code: "config_error", Message: "Could not access saved configuration; check its JSON and permissions, or clear the saved override"}
}
func Read(path string) (string, bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, failure()
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return "", false, failure()
	}
	var v struct {
		Token *string `json:"token"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return "", false, failure()
	}
	if v.Token == nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal(b, &fields) != nil {
			return "", false, failure()
		}
		if _, exists := fields["token"]; exists {
			return "", false, failure()
		}
		return "", false, nil
	}
	return *v.Token, true, nil
}
func Save(path, token string) error {
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0700) != nil || restrict(dir, true) != nil {
		return failure()
	}
	f, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return failure()
	}
	name := f.Name()
	defer os.Remove(name)
	if restrict(name, false) != nil {
		f.Close()
		return failure()
	}
	err = json.NewEncoder(f).Encode(map[string]string{"token": token})
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return failure()
	}
	if os.Rename(name, path) != nil {
		return failure()
	}
	return nil
}
func Clear(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return failure()
	}
	return nil
}

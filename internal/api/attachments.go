package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func SafeFilename(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00<>\"|?*") || strings.TrimRight(name, ". ") != name {
		return false
	}
	for _, r := range name {
		if r < 32 {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}
func attachmentURL(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "cdn.discordapp.com" || u.Hostname() == "media.discordapp.net") && strings.HasPrefix(u.Path, "/attachments/")
}
func (c *Client) Download(ctx context.Context, channel, message, attachment, dir, name string, transport http.RoundTripper) (any, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	m, err := c.Message(channel, message)
	if err != nil {
		return nil, err
	}
	var selected *Attachment
	for i := range m.Attachments {
		if m.Attachments[i].ID == attachment {
			selected = &m.Attachments[i]
			break
		}
	}
	if selected == nil {
		return nil, HTTPFailure(404)
	}
	if name == "" {
		name = selected.Filename
	}
	if !SafeFilename(name) {
		return nil, Invalid("unsafe attachment filename; choose a simple filename with --name")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, Invalid("--dir must name an accessible existing directory")
	}
	defer root.Close()
	if _, err := root.Lstat(name); !os.IsNotExist(err) {
		return nil, Invalid("destination already exists or cannot be checked; choose another filename")
	}
	target, err := url.Parse(selected.URL)
	if err != nil || !attachmentURL(target) {
		return nil, Upstream()
	}
	client := http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !attachmentURL(r.URL) {
			return errors.New("unsupported attachment redirect")
		}
		r.Header = make(http.Header)
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return nil, Upstream()
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &Error{Code: "network_failure", Message: "Attachment request failed or its deadline elapsed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &Error{Code: "attachment_unavailable", Message: "The attachment is expired, unavailable, or forbidden; retrieve the message again before retrying", Status: response.StatusCode}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	temporary := ".discord-download-" + hex.EncodeToString(random[:])
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, &Error{Code: "file_error", Message: "Could not create a temporary download in the output directory"}
	}
	defer root.Remove(temporary)
	defer file.Close()
	if selected.Size == int64(^uint64(0)>>1) {
		return nil, Upstream()
	}
	count, err := io.Copy(file, io.LimitReader(response.Body, selected.Size+1))
	if err != nil || count != selected.Size || (response.ContentLength >= 0 && count != response.ContentLength) || ctx.Err() != nil {
		return nil, &Error{Code: "network_failure", Message: "Attachment transfer was incomplete; no final file was created"}
	}
	if file.Sync() != nil || file.Close() != nil {
		return nil, &Error{Code: "file_error", Message: "Could not finish the downloaded file"}
	}
	// A same-directory hard link publishes the completed file atomically and cannot overwrite.
	if err := root.Link(temporary, name); err != nil {
		return nil, &Error{Code: "file_error", Message: "Could not publish the download without overwriting; check filesystem hard-link support and destination"}
	}
	absolute, err := filepath.Abs(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "channel_id": channel, "message_id": message, "attachment_id": attachment, "path": absolute, "bytes": count}, nil
}

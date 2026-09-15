package api

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
)

type Upload struct {
	File *os.File
	Name string
	Size int64
}

func OpenUploads(paths []string) ([]Upload, error) {
	var files []Upload
	for _, path := range paths {
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() {
			CloseUploads(files)
			return nil, Invalid("attachments must be readable regular files")
		}
		f, e := os.Open(path)
		if e != nil {
			CloseUploads(files)
			return nil, Invalid("could not open an explicit attachment file")
		}
		opened, e := f.Stat()
		if e != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			f.Close()
			CloseUploads(files)
			return nil, Invalid("attachments must be readable regular files")
		}
		files = append(files, Upload{f, filepath.Base(path), info.Size()})
	}
	return files, nil
}
func CloseUploads(files []Upload) {
	for _, f := range files {
		_ = f.File.Close()
	}
}
func uploadBody(payload map[string]any, files []Upload) Body {
	if len(files) == 0 {
		return JSONBody(payload)
	}
	return func() (io.ReadCloser, string, error) {
		var header bytes.Buffer
		writer := multipart.NewWriter(&header)
		// Discord multipart metadata is small; file bodies remain streamed.
		stream, _, err := JSONBody(payload)()
		if err != nil {
			return nil, "", err
		}
		raw, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			return nil, "", err
		}
		if err := writer.WriteField("payload_json", string(raw)); err != nil {
			return nil, "", err
		}
		var readers []io.Reader
		for i, file := range files {
			if _, err := writer.CreateFormFile(fmt.Sprintf("files[%d]", i), file.Name); err != nil {
				return nil, "", err
			}
			readers = append(readers, bytes.NewReader(bytes.Clone(header.Bytes())), io.NewSectionReader(file.File, 0, file.Size))
			header.Reset()
		}
		if err := writer.Close(); err != nil {
			return nil, "", err
		}
		readers = append(readers, bytes.NewReader(bytes.Clone(header.Bytes())))
		return io.NopCloser(io.MultiReader(readers...)), writer.FormDataContentType(), nil
	}
}
func (c *Client) WriteMessage(channel, message string, content *string, reply string, files []Upload, remove []string, identity string) (any, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if content != nil {
		payload["content"] = *content
	}
	if reply != "" {
		payload["message_reference"] = map[string]any{"message_id": reply, "channel_id": channel, "fail_if_not_exists": true}
	}
	attachments := []map[string]any{}
	if message != "" {
		current, err := c.Message(channel, message)
		if err != nil {
			return nil, err
		}
		if current.Author.ID != identity {
			return nil, &Error{Code: "permission_denied", Message: "Discord only permits editing the acting account's own messages"}
		}
		wanted := map[string]bool{}
		for _, id := range remove {
			wanted[id] = true
		}
		for _, a := range current.Attachments {
			if wanted[a.ID] {
				delete(wanted, a.ID)
			} else {
				attachments = append(attachments, map[string]any{"id": a.ID, "filename": a.Filename})
			}
		}
		if len(wanted) > 0 {
			return nil, Invalid("an attachment selected for removal does not belong to this message")
		}
	}
	for i, f := range files {
		attachments = append(attachments, map[string]any{"id": strconv.Itoa(i), "filename": f.Name})
	}
	if len(attachments) > 10 {
		return nil, Invalid("a message supports at most 10 attachments")
	}
	if len(files) > 0 || len(remove) > 0 {
		payload["attachments"] = attachments
	}
	path := "/channels/" + channel + "/messages"
	method := "POST"
	if message != "" {
		path += "/" + message
		method = "PATCH"
	}
	var result Message
	if err := c.Request(method, path, uploadBody(payload, files), &result, message == ""); err != nil {
		return nil, err
	}
	if !validMessage(result) || result.ChannelID != channel || (message != "" && result.ID != message) {
		if message == "" {
			return nil, uncertain(200, path)
		}
		return nil, Upstream()
	}
	return result, nil
}
func (c *Client) DeleteMessage(channel, message string) (any, error) {
	if _, err := c.Channel(channel); err != nil {
		return nil, err
	}
	if err := c.Request("DELETE", "/channels/"+channel+"/messages/"+message, nil, nil, false); err != nil {
		return nil, err
	}
	return Confirmation("delete_message", channel, message), nil
}

//go:build !windows

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSpecialAttachmentDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), []string{"--timeout", "20ms", "messages", "send", "456", "--file", path}, strings.NewReader(""), &out, &errs, Options{LookupEnv: func(string) (string, bool) { return "", true }})
	}()
	select {
	case code := <-done:
		if code != 2 || !strings.Contains(errs.String(), "invalid_arguments") {
			t.Fatalf("%d %s", code, errs.String())
		}
	case <-time.After(time.Second):
		// Unblock the old implementation so the regression itself leaves no goroutine.
		f, e := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if e == nil {
			defer f.Close()
		}
		<-done
		t.Fatal("special attachment blocked beyond the invocation deadline")
	}
}

func TestBatchDownloadCannotEscapeOutputRoot(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "9007199254740993")); err != nil {
		t.Fatal(err)
	}
	cdnCalls := 0
	code, out, errs := invoke(t, []string{"attachments", "download-many", "456", "9007199254740993", "--dir", dir}, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me":
			io.WriteString(w, `{"id":"111","username":"owner"}`)
		case "/channels/456":
			io.WriteString(w, `{"id":"456","type":1}`)
		default:
			io.WriteString(w, "["+strings.Replace(sampleMessage, `"attachments":[]`, `"attachments":[{"id":"700","filename":"image.png","size":5,"url":"https://cdn.discordapp.com/attachments/456/700/image.png"}]`, 1)+"]")
		}
	}, func(o *Options) {
		o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			cdnCalls++
			return nil, fmt.Errorf("unexpected CDN request")
		})
	})
	files, _ := os.ReadDir(outside)
	if code != 1 || !strings.Contains(errs, "partial_download") || !strings.Contains(out, "file_error") || cdnCalls != 0 || len(files) != 0 {
		t.Fatalf("output root escaped: %d %s %s CDN=%d files=%d", code, out, errs, cdnCalls, len(files))
	}
}

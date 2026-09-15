//go:build !windows

package cli

import (
	"bytes"
	"context"
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

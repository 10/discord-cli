//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

type readyReader struct {
	io.Reader
	ready bool
}

func (r *readyReader) Read(p []byte) (int, error) {
	if !r.ready {
		r.ready = true
		_, _ = io.WriteString(os.Stdout, "READY\n")
	}
	return r.Reader.Read(p)
}
func TestSignalChild(t *testing.T) {
	if os.Getenv("DISCORD_CLI_SIGNAL_CHILD") != "1" {
		return
	}
	os.Exit(run([]string{"--timeout", "10s", "auth", "set-token"}, &readyReader{Reader: os.Stdin}, io.Discard, os.Stderr))
}
func TestSIGTERMCancelsCommand(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalChild$")
	cmd.Env = append(os.Environ(), "DISCORD_CLI_SIGNAL_CHILD=1")
	input, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer input.Close()
	output, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	line, e := bufio.NewReader(output).ReadString('\n')
	if e != nil || line != "READY\n" {
		t.Fatal("child did not reach token input")
	}
	if e := cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	e = cmd.Wait()
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), `"code":"cancelled"`) {
		t.Fatalf("exit=%v stderr=%s", e, stderr.String())
	}
}

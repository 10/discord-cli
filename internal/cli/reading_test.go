package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHistoryPagination(t *testing.T) {
	for _, tc := range []struct {
		name            string
		flags           []string
		count, requests int
		more            bool
		oldest, newest  string
	}{
		{"exhausted", []string{"--limit", "500"}, 103, 2, false, "1001", "1103"},
		{"requested limit", []string{"--limit", "101"}, 101, 2, true, "1003", "1103"},
		{"before", []string{"--limit", "500", "--before", "1100"}, 99, 1, false, "1001", "1099"},
		{"after", []string{"--limit", "101", "--after", "1000"}, 101, 2, true, "1001", "1101"},
		{"empty", []string{"--limit", "500", "--after", "1103"}, 0, 1, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			code, out, errs := invoke(t, append([]string{"messages", "list", "456"}, tc.flags...), "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				requests++
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				if limit < 1 || limit > 100 {
					t.Errorf("unbounded page: %s", r.URL)
				}
				rows := []json.RawMessage{}
				add := func(id int) {
					rows = append(rows, json.RawMessage(strings.Replace(sampleMessage, "9007199254740993", strconv.Itoa(id), 1)))
				}
				if raw := r.URL.Query().Get("after"); raw != "" {
					after, _ := strconv.Atoi(raw)
					for id := max(1001, after+1); id <= 1103 && len(rows) < limit; id++ {
						add(id)
					}
				} else {
					before := 1104
					if raw := r.URL.Query().Get("before"); raw != "" {
						before, _ = strconv.Atoi(raw)
					}
					for id := min(1103, before-1); id >= 1001 && len(rows) < limit; id-- {
						add(id)
					}
				}
				json.NewEncoder(w).Encode(rows)
			}), nil)
			var result struct {
				Items    []struct{ ID string }
				Complete bool
				HasMore  bool `json:"has_more"`
				Order    string
				Before   string `json:"next_before"`
				After    string `json:"next_after"`
			}
			if code != 0 || errs != "" || json.Unmarshal([]byte(out), &result) != nil {
				t.Fatalf("%d %s %s", code, out, errs)
			}
			if len(result.Items) != tc.count || !result.Complete || result.HasMore != tc.more || requests != tc.requests || result.Before != tc.oldest || result.After != tc.newest || result.Order != "newest_first" {
				t.Fatalf("wrong coverage: %s requests=%d", out, requests)
			}
			previous := uint64(^uint64(0))
			for _, m := range result.Items {
				id, _ := strconv.ParseUint(m.ID, 10, 64)
				if id >= previous {
					t.Fatal("duplicate or out-of-order message", out)
				}
				previous = id
			}
		})
	}
}

func TestMessageTranscripts(t *testing.T) {
	rich := strings.TrimSuffix(sampleMessage, "}") + `,"content":"first line\nsynthetic.token.signature\u001b[31m","message_reference":{"message_id":"777"},"embeds":[{"title":"Release","description":"Build ready","fields":[{"name":"Status","value":"Passed"}]}],"attachments":[{"id":"700","filename":"image.png","size":9,"content_type":"image/png","url":"https://cdn.discordapp.com/attachments/456/700/image.png"}],"components":[{"type":10,"content":"Component text"}],"poll":{"question":{"text":"Ship?"}},"message_snapshots":[{"message":{"content":"Forwarded text"}}]}`
	old := strings.Replace(rich, "9007199254740993", "9007199254740992", 1)
	for _, operation := range []string{"list", "get"} {
		t.Run(operation, func(t *testing.T) {
			args := []string{"messages", operation, "456"}
			count := 2
			if operation == "get" {
				args = append(args, "9007199254740993")
				count = 1
			}
			args = append(args, "--format", "transcript")
			code, out, errs := invoke(t, args, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				if operation == "get" {
					io.WriteString(w, "["+rich+"]")
				} else {
					io.WriteString(w, "["+rich+","+old+"]")
				}
			}), nil)
			if code != 0 || errs != "" || strings.Contains(out, "synthetic.token.signature") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
			for _, want := range []string{"first line", "[REDACTED]", "reply_to: 777", "embed: Release", "Build ready", "Status: Passed", "attachment:", "Component text", "Ship?", "Forwarded text", "[coverage]", `"order":"oldest_first"`, `"message_count":` + strconv.Itoa(count)} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q: %s", want, out)
				}
			}
			if strings.Contains(out, "\x1b") {
				t.Fatal("terminal control sequence retained")
			}
			if count == 2 && strings.Index(out, "message=9007199254740992") > strings.Index(out, "message=9007199254740993") {
				t.Fatal("transcript is not chronological")
			}
		})
	}
}

func TestBatchAttachmentManifest(t *testing.T) {
	dir := t.TempDir()
	reads := 0
	code, out, errs := invoke(t, []string{"attachments", "download-many", "456", "9007199254740993", "9007199254740994", "9007199254740993", "--dir", dir}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		reads++
		id := r.URL.Query().Get("around")
		a := "700"
		if id == "9007199254740994" {
			a = "701"
		}
		m := strings.Replace(sampleMessage, "9007199254740993", id, 1)
		m = strings.Replace(m, `"attachments":[]`, fmt.Sprintf(`"attachments":[{"id":%q,"filename":"image.png","size":3,"url":"https://cdn.discordapp.com/attachments/456/%s/image.png"}]`, a, a), 1)
		io.WriteString(w, "["+m+"]")
	}), func(o *Options) {
		o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "" {
				t.Error("credential forwarded to CDN")
			}
			status := 200
			if strings.Contains(r.URL.Path, "701") {
				status = 404
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("hello")), ContentLength: 5, Request: r}, nil
		})
	})
	var result struct {
		Items []struct {
			OK    bool
			Path  string
			Error *struct{ Code string }
		}
		Complete          bool
		Succeeded, Failed int
	}
	if code != 1 || !strings.Contains(errs, "partial_download") || json.Unmarshal([]byte(out), &result) != nil {
		t.Fatalf("partial failure hidden: %d %s %s", code, out, errs)
	}
	// Duplicate message IDs are read once; one file succeeds and one reports its own error.
	if reads != 2 || len(result.Items) != 2 || result.Complete || result.Succeeded != 1 || result.Failed != 1 {
		t.Fatalf("wrong selection/coverage: %s reads=%d", out, reads)
	}
	for _, item := range result.Items {
		if item.OK {
			if b, err := os.ReadFile(item.Path); err != nil || string(b) != "hello" {
				t.Fatalf("bad file: %q %v", b, err)
			}
		} else if item.Error == nil || item.Error.Code != "attachment_unavailable" {
			t.Fatalf("missing per-file error: %s", out)
		}
	}
}

func TestDownloadFramingAndCeiling(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length int64
		body   string
		exit   int
		code   string
	}{
		// Metadata can describe transformed bytes; the 3-byte size must not truncate the 5-byte original.
		{"unknown length larger than metadata", -1, "hello", 0, ""},
		{"oversized response", 1<<30 + 1, "", 1, "file_error"},
		{"truncated response", 10, "hello", 1, "network_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			code, out, errs := invoke(t, []string{"attachments", "download", "456", "9007199254740993", "700", "--dir", dir}, "", withAttachment(`{"id":"700","filename":"image.png","size":3,"content_type":"image/webp","url":"https://cdn.discordapp.com/attachments/456/700/image.png"}`), func(o *Options) {
				o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: tc.length, Request: r}, nil
				})
			})
			files, _ := os.ReadDir(dir)
			if code != tc.exit {
				t.Fatalf("%d %s %s", code, out, errs)
			}
			if tc.exit != 0 {
				if out != "" || !strings.Contains(errs, tc.code) || len(files) != 0 {
					t.Fatalf("failed transfer published data: %d %s %s files=%d", code, out, errs, len(files))
				}
			} else if data, err := os.ReadFile(filepath.Join(dir, "image.png")); err != nil || string(data) != tc.body {
				t.Fatalf("complete response was rejected or truncated: %q %v %s", data, err, errs)
			}
		})
	}
}

func TestHistoryFailureDiscardsCollectedMessages(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"messages", "list", "456", "--limit", "101", "--format", "transcript"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			w.WriteHeader(500)
			io.WriteString(w, `{}`)
			return
		}
		rows := make([]json.RawMessage, 100)
		for i := range rows {
			rows[i] = json.RawMessage(strings.Replace(sampleMessage, "9007199254740993", strconv.Itoa(2000-i), 1))
		}
		json.NewEncoder(w).Encode(rows)
	}), nil)
	if code != 1 || out != "" || calls != 2 || !strings.Contains(errs, "upstream_error") {
		t.Fatalf("failed history read leaked output: %d %s %s calls=%d", code, out, errs, calls)
	}
}

func TestHistoryInternalRequestCeiling(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"messages", "list", "456", "--limit", "101"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		calls++
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		row := json.RawMessage(strings.Replace(sampleMessage, "9007199254740993", strconv.Itoa(2001-calls), 1))
		rows := make([]json.RawMessage, limit)
		for i := range rows {
			rows[i] = row
		}
		json.NewEncoder(w).Encode(rows)
	}), nil)
	var result struct {
		Items    []struct{ ID string }
		Complete bool
		HasMore  bool   `json:"has_more"`
		Before   string `json:"next_before"`
		After    string `json:"next_after"`
	}
	if code != 0 || errs != "" || json.Unmarshal([]byte(out), &result) != nil || calls != 100 || len(result.Items) != 100 || result.Complete || !result.HasMore || result.Before != "1901" || result.After != "2000" {
		t.Fatalf("unbounded or misleading read: %d %s %s requests=%d", code, out, errs, calls)
	}
}

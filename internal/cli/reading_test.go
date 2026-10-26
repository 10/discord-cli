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

func TestAttachmentRepresentationSize(t *testing.T) {
	for _, size := range []int{3, 9} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := t.TempDir()
			code, out, errs := invoke(t, []string{"attachments", "download", "456", "9007199254740993", "700", "--dir", dir}, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				default:
					a := fmt.Sprintf(`"attachments":[{"id":"700","filename":"image.png","size":%d,"content_type":"image/webp","url":"https://cdn.discordapp.com/attachments/456/700/image.png"}]`, size)
					io.WriteString(w, "["+strings.Replace(sampleMessage, `"attachments":[]`, a, 1)+"]")
				}
			}, func(o *Options) {
				o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "" {
						t.Error("credential sent to CDN")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(strings.NewReader("hello")), ContentLength: 5, Request: r}, nil
				})
			})
			data, err := os.ReadFile(filepath.Join(dir, "image.png"))
			if code != 0 || errs != "" || err != nil || string(data) != "hello" {
				t.Fatalf("exit=%d stdout=%s stderr=%s file=%q error=%v", code, out, errs, data, err)
			}
		})
	}
}

func TestSafeArgumentDiagnostics(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"attachments", "download", "456", "789", "--dir", dir},
		{"attachments", "download", "456", "789"},
	} {
		code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input reached network") }, func(o *Options) {
			o.LookupEnv = func(string) (string, bool) { t.Error("invalid input read credentials"); return "", false }
		})
		if code != 2 || out != "" || !strings.Contains(errs, "attachment") || !strings.Contains(errs, "discord attachments download --help") {
			t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, errs)
		}
	}
}

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
		{"single page", []string{"--limit", "50"}, 50, 1, true, "1054", "1103"},
		{"before", []string{"--limit", "500", "--before", "1100"}, 99, 1, false, "1001", "1099"},
		{"resume older", []string{"--limit", "500", "--before", "1004"}, 3, 1, false, "1001", "1003"},
		{"after", []string{"--limit", "101", "--after", "1000"}, 101, 2, true, "1001", "1101"},
		{"after exhausted", []string{"--limit", "500", "--after", "1099"}, 4, 1, false, "1100", "1103"},
		{"empty", []string{"--limit", "500", "--after", "1103"}, 0, 1, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, channels := 0, 0
			code, out, errs := invoke(t, append([]string{"messages", "list", "456"}, tc.flags...), "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					channels++
					io.WriteString(w, `{"id":"456","type":1}`)
				case "/channels/456/messages":
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
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(404)
				}
			}, nil)
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
			if len(result.Items) != tc.count || !result.Complete || result.HasMore != tc.more || requests != tc.requests || channels != 1 || result.Before != tc.oldest || result.After != tc.newest || result.Order != "newest_first" {
				t.Fatalf("wrong coverage: %s requests=%d channels=%d", out, requests, channels)
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

func TestHistoryRejectsNonProgress(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"messages", "list", "456", "--limit", "101", "--before", "9007199254740993"}, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me":
			io.WriteString(w, `{"id":"111","username":"owner"}`)
		case "/channels/456":
			io.WriteString(w, `{"id":"456","type":1}`)
		default:
			calls++
			io.WriteString(w, "["+sampleMessage+"]")
		}
	}, nil)
	if code != 1 || out != "" || calls != 1 || !strings.Contains(errs, "invalid_upstream_data") {
		t.Fatalf("non-progressing history accepted: %d %s %s calls=%d", code, out, errs, calls)
	}
}

func TestMessageTranscripts(t *testing.T) {
	rich := strings.TrimSuffix(sampleMessage, "}") + `,"content":"first line\nsynthetic.token.signature\u001b[31m","message_reference":{"message_id":"777"},"embeds":[{"title":"Release","description":"Build ready","fields":[{"name":"Status","value":"Passed"}]}],"attachments":[{"id":"700","filename":"image.png","size":9,"content_type":"image/png","url":"https://cdn.discordapp.com/attachments/456/700/image.png"}],"components":[{"type":10,"content":"Component text"}],"poll":{"question":{"text":"Ship?"}},"message_snapshots":[{"message":{"content":"Forwarded text"}}]}`
	old := strings.Replace(rich, "9007199254740993", "9007199254740992", 1)
	for _, operation := range []string{"list", "get", "search"} {
		t.Run(operation, func(t *testing.T) {
			args := []string{"messages", operation, "456"}
			count := 2
			if operation == "get" {
				args = append(args, "9007199254740993")
				count = 1
			}
			if operation == "search" {
				args = []string{"messages", "search", "--server", "123"}
			}
			args = append(args, "--format", "transcript")
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				case "/guilds/123/messages/search":
					io.WriteString(w, `{"total_results":2,"messages":[[`+rich+`],[`+old+`]]}`)
				default:
					if operation == "get" {
						io.WriteString(w, "["+rich+"]")
					} else {
						io.WriteString(w, "["+rich+","+old+"]")
					}
				}
			}, nil)
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
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			dir := t.TempDir()
			reads, channelReads := 0, 0
			code, out, errs := invoke(t, []string{"attachments", "download-many", "456", "9007199254740993", "9007199254740994", "9007199254740993", "--dir", dir}, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					channelReads++
					io.WriteString(w, `{"id":"456","type":1}`)
				default:
					reads++
					id := r.URL.Query().Get("around")
					a := "700"
					if id == "9007199254740994" {
						a = "701"
					}
					m := strings.Replace(sampleMessage, "9007199254740993", id, 1)
					m = strings.Replace(m, `"attachments":[]`, fmt.Sprintf(`"attachments":[{"id":%q,"filename":"image.png","size":3,"url":"https://cdn.discordapp.com/attachments/456/%s/image.png"}]`, a, a), 1)
					io.WriteString(w, "["+m+"]")
				}
			}, func(o *Options) {
				o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "" {
						t.Error("credential forwarded to CDN")
					}
					status := 200
					if partial && strings.Contains(r.URL.Path, "701") {
						status = 404
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("hello")), ContentLength: 5, Request: r}, nil
				})
			})
			var result struct {
				Items []struct {
					OK                            bool
					Path, MessageID, AttachmentID string
					Error                         *struct{ Code string }
				}
				Complete          bool
				Succeeded, Failed int
			}
			if json.Unmarshal([]byte(out), &result) != nil {
				t.Fatalf("invalid manifest: %d %s %s", code, out, errs)
			}
			if reads != 2 || channelReads != 1 || len(result.Items) != 2 || result.Complete == partial {
				t.Fatalf("wrong selection/coverage: %d %s %s reads=%d", code, out, errs, reads)
			}
			successes := 0
			for _, item := range result.Items {
				if item.OK {
					b, err := os.ReadFile(item.Path)
					if err != nil || string(b) != "hello" {
						t.Fatalf("bad file: %q %v", b, err)
					}
					successes++
				} else if item.Error == nil || item.Error.Code != "attachment_unavailable" {
					t.Fatalf("missing per-file error: %s", out)
				}
			}
			if result.Succeeded != successes || result.Failed != 2-successes {
				t.Fatal("wrong result totals", out)
			}
			if partial {
				if code != 1 || !strings.Contains(errs, "partial_download") || successes != 1 {
					t.Fatalf("partial failure hidden: %d %s %s", code, out, errs)
				}
			} else if code != 0 || errs != "" || successes != 2 {
				t.Fatalf("successful batch failed: %d %s %s", code, out, errs)
			}
		})
	}
}

func TestReadingValidationBeforeAuthentication(t *testing.T) {
	for _, args := range [][]string{
		{"messages", "list", "456", "--limit", "0"},
		{"messages", "list", "456", "--limit", "10001"},
		{"messages", "list", "456", "--around", "789", "--limit", "101"},
		{"messages", "list", "456", "--before", "100", "--after", "200"},
		{"messages", "list", "456", "--format", "jsonl"},
		{"messages", "export", "456"},
		{"messages", "list", "456", "--max-pages", "1"},
		{"attachments", "download-many", "456", "bad", "--dir", t.TempDir()},
		{"attachments", "download", "456", "secret.token.value"},
	} {
		code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input reached network") }, func(o *Options) {
			o.LookupEnv = func(string) (string, bool) { t.Error("invalid input read credentials"); return "", false }
		})
		if code != 2 || out != "" || !strings.Contains(errs, "invalid_arguments") || strings.Contains(errs, "secret.token.value") {
			t.Fatalf("invalid/secret diagnostic: %d %s %s", code, out, errs)
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
		{"unknown length", -1, "hello", 0, ""},
		{"oversized response", 1<<30 + 1, "", 1, "file_error"},
		{"truncated response", 10, "hello", 1, "network_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			code, out, errs := invoke(t, []string{"attachments", "download", "456", "9007199254740993", "700", "--dir", dir}, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				default:
					io.WriteString(w, "["+strings.Replace(sampleMessage, `"attachments":[]`, `"attachments":[{"id":"700","filename":"image.png","size":3,"url":"https://cdn.discordapp.com/attachments/456/700/image.png"}]`, 1)+"]")
				}
			}, func(o *Options) {
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
			} else if len(files) != 1 {
				t.Fatal("complete unknown-length response was rejected", out, errs)
			}
		})
	}
}

func TestHistoryFailureDiscardsCollectedMessages(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"messages", "list", "456", "--limit", "101", "--format", "transcript"}, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me":
			io.WriteString(w, `{"id":"111","username":"owner"}`)
		case "/channels/456":
			io.WriteString(w, `{"id":"456","type":1}`)
		default:
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
		}
	}, nil)
	if code != 1 || out != "" || calls != 2 || !strings.Contains(errs, "upstream_error") {
		t.Fatalf("failed history read leaked output: %d %s %s calls=%d", code, out, errs, calls)
	}
}

func TestHistoryInternalRequestCeiling(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"messages", "list", "456", "--limit", "101"}, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me":
			io.WriteString(w, `{"id":"111","username":"owner"}`)
		case "/channels/456":
			io.WriteString(w, `{"id":"456","type":1}`)
		default:
			calls++
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			row := json.RawMessage(strings.Replace(sampleMessage, "9007199254740993", strconv.Itoa(2001-calls), 1))
			rows := make([]json.RawMessage, limit)
			for i := range rows {
				rows[i] = row
			}
			json.NewEncoder(w).Encode(rows)
		}
	}, nil)
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

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
	"time"
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

func TestHistoryExportCoverage(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		flags                  []string
		count, pages           int
		complete               bool
		reason, oldest, newest string
	}{
		{"exhausted", nil, 103, 2, true, "exhausted", "1001", "1103"},
		{"message budget", []string{"--max-messages", "101"}, 101, 2, false, "max_messages", "1003", "1103"},
		{"page budget", []string{"--max-pages", "1"}, 100, 1, false, "max_pages", "1004", "1103"},
		{"range", []string{"--before", "1100", "--after", "1096"}, 3, 1, true, "after_bound", "1097", "1099"},
		{"resume", []string{"--before", "1004"}, 3, 1, true, "exhausted", "1001", "1003"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			args := append([]string{"messages", "export", "456"}, tc.flags...)
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				case "/channels/456/messages":
					requests++
					limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
					if limit < 1 || limit > 100 {
						t.Errorf("unbounded page: %s", r.URL)
					}
					before := 1104
					if raw := r.URL.Query().Get("before"); raw != "" {
						before, _ = strconv.Atoi(raw)
					}
					var rows []json.RawMessage
					for id := 1103; id >= 1001 && len(rows) < limit; id-- {
						if id < before {
							rows = append(rows, json.RawMessage(strings.Replace(sampleMessage, "9007199254740993", strconv.Itoa(id), 1)))
						}
					}
					if rows == nil {
						rows = []json.RawMessage{}
					}
					json.NewEncoder(w).Encode(rows)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					w.WriteHeader(404)
				}
			}, nil)
			var result struct {
				Items    []struct{ ID string }
				Complete bool
				HasMore  bool   `json:"has_more"`
				Pages    int    `json:"pages_read"`
				Reason   string `json:"stop_reason"`
				Before   string `json:"next_before"`
				After    string `json:"next_after"`
			}
			if code != 0 || errs != "" || json.Unmarshal([]byte(out), &result) != nil {
				t.Fatalf("exit=%d out=%s err=%s", code, out, errs)
			}
			if len(result.Items) != tc.count || result.Complete != tc.complete || result.HasMore == tc.complete || result.Pages != tc.pages || requests != tc.pages || result.Reason != tc.reason || result.Before != tc.oldest || result.After != tc.newest {
				t.Fatalf("wrong coverage: %s requests=%d", out, requests)
			}
			seen := map[string]bool{}
			for _, m := range result.Items {
				if seen[m.ID] {
					t.Fatal("duplicate message", m.ID)
				}
				seen[m.ID] = true
			}
		})
	}
}

func TestExportDateBoundsAndNonProgress(t *testing.T) {
	day := time.Date(2026, 9, 15, 12, 0, 0, 0, time.Local)
	cursor := strconv.FormatUint(uint64(day.UnixMilli()-1420070400000)<<22, 10)
	code, out, errs := invoke(t, []string{"messages", "export", "456", "--on", "2026-09-15", "--before", cursor}, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me":
			io.WriteString(w, `{"id":"111","username":"owner"}`)
		case "/channels/456":
			io.WriteString(w, `{"id":"456","type":1}`)
		default:
			if r.URL.Query().Get("before") != cursor {
				t.Errorf("resume lost date/cursor bounds: %s", r.URL)
			}
			older := strconv.FormatUint(uint64(day.Add(-13*time.Hour).UnixMilli()-1420070400000)<<22, 10)
			io.WriteString(w, "["+strings.Replace(sampleMessage, "9007199254740993", older, 1)+"]")
		}
	}, nil)
	if code != 0 || errs != "" || !strings.Contains(out, `"complete":true`) || !strings.Contains(out, `"items":[]`) || !strings.Contains(out, `"stop_reason":"after_bound"`) {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	calls := 0
	code, out, errs = invoke(t, []string{"messages", "export", "456", "--before", "9007199254740993"}, "", func(w http.ResponseWriter, r *http.Request) {
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

func TestMessageReadingFormats(t *testing.T) {
	rich := strings.TrimSuffix(sampleMessage, "}") + `,"content":"first line\nsynthetic.token.signature\u001b[31m","message_reference":{"message_id":"777"},"embeds":[{"title":"Release","description":"Build ready","fields":[{"name":"Status","value":"Passed"}]}],"attachments":[{"id":"700","filename":"image.png","size":9,"content_type":"image/png","url":"https://cdn.discordapp.com/attachments/456/700/image.png"}],"components":[{"type":10,"content":"Component text"}],"poll":{"question":{"text":"Ship?"}},"message_snapshots":[{"message":{"content":"Forwarded text"}}]}`
	old := strings.Replace(rich, "9007199254740993", "9007199254740992", 1)
	for _, format := range []string{"jsonl", "transcript"} {
		for _, operation := range []string{"list", "get", "search", "export"} {
			t.Run(format+"/"+operation, func(t *testing.T) {
				args := []string{"messages", operation, "456"}
				count := 2
				if operation == "get" {
					args = append(args, "9007199254740993")
					count = 1
				}
				if operation == "search" {
					args = []string{"messages", "search", "--server", "123"}
				}
				args = append(args, "--format", format)
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
					t.Fatalf("exit=%d out=%s err=%s", code, out, errs)
				}
				if format == "jsonl" {
					lines := strings.Split(strings.TrimSpace(out), "\n")
					if len(lines) != count+1 {
						t.Fatalf("missing JSONL records: %s", out)
					}
					for i, line := range lines {
						var row map[string]json.RawMessage
						if json.Unmarshal([]byte(line), &row) != nil {
							t.Fatal("invalid JSONL", line)
						}
						if i < count {
							var m struct {
								ID     string
								Embeds json.RawMessage
							}
							if string(row["type"]) != `"message"` || json.Unmarshal(row["message"], &m) != nil || m.ID == "" || !strings.Contains(string(m.Embeds), "Passed") {
								t.Fatal("message lost fields", line)
							}
						} else if string(row["type"]) != `"coverage"` || !strings.Contains(string(row["coverage"]), `"message_count":`+strconv.Itoa(count)) {
							t.Fatal("missing coverage", line)
						}
					}
				} else {
					for _, want := range []string{"first line", "[REDACTED]", "reply_to: 777", "embed: Release", "Build ready", "Status: Passed", "attachment:", "Component text", "Ship?", "Forwarded text", "[coverage]", `"order":"oldest_first"`} {
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
				}
			})
		}
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
		{"messages", "export", "456", "--max-pages", "0"},
		{"messages", "export", "456", "--max-messages", "10001"},
		{"messages", "export", "456", "--on", "2026-02-30"},
		{"messages", "export", "456", "--before", "100", "--after", "200"},
		{"messages", "export", "456", "--format", "bad"},
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

func TestExportFailureDiscardsCollectedMessages(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"messages", "export", "456", "--max-messages", "101", "--format", "jsonl"}, "", func(w http.ResponseWriter, r *http.Request) {
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
		t.Fatalf("failed export leaked output: %d %s %s calls=%d", code, out, errs, calls)
	}
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func invoke(t *testing.T, args []string, input string, handler http.HandlerFunc, change func(*Options)) (int, string, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	opts := Options{ConfigPath: filepath.Join(t.TempDir(), "config.json"), LookupEnv: func(string) (string, bool) { return "synthetic.token.signature", true }, APIBase: server.URL, DesktopPaths: []string{}, TempDir: t.TempDir()}
	if change != nil {
		change(&opts)
	}
	var out, errs bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(input), &out, &errs, opts)
	return code, out.String(), errs.String()
}

func TestLocalCommandsAndValidation(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"messages", "send", "--help"}, {"messages", "get", "invalid", "123"}, {"--token", "a-secret"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) { calls++ }, func(o *Options) {
				o.LookupEnv = func(string) (string, bool) { t.Fatal("credentials read"); return "", false }
			})
			if calls != 0 {
				t.Fatal("unexpected network")
			}
			if strings.Contains(strings.Join(args, " "), "help") || args[0] == "--version" {
				if code != 0 || out == "" || errs != "" {
					t.Fatalf("%d %s %s", code, out, errs)
				}
			} else {
				var e struct{ Error struct{ Code string } }
				if code != 2 || out != "" || json.Unmarshal([]byte(errs), &e) != nil || e.Error.Code != "invalid_arguments" || strings.Contains(errs, "a-secret") {
					t.Fatalf("%d %s %s", code, out, errs)
				}
			}
		})
	}
}

func TestExplicitCredentialPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, env    string
		set          bool
		config       string
		status, exit int
		source       string
	}{
		{"environment", "synthetic.token.signature", true, `broken`, 200, 0, "environment"},
		{"empty environment", "", true, `{"token":"saved.token.signature"}`, 200, 3, ""},
		{"invalid environment", "bad", true, `{"token":"saved.token.signature"}`, 200, 3, ""},
		{"expired environment", "synthetic.token.signature", true, `{"token":"saved.token.signature"}`, 401, 3, ""},
		{"saved", "", false, `{"token":"saved.token.signature"}`, 200, 0, "configuration"},
		{"broken configuration", "", false, `broken`, 200, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/users/@me" {
					t.Fatal(r.URL.Path)
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"id":"1084003247154548807","username":"owner"}`)
			}, func(o *Options) {
				o.LookupEnv = func(string) (string, bool) { return tc.env, tc.set }
				if err := os.WriteFile(o.ConfigPath, []byte(tc.config), 0600); err != nil {
					t.Fatal(err)
				}
			})
			if code != tc.exit || (tc.exit == 0 && !strings.Contains(out, tc.source)) || strings.Contains(out+errs, "signature") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
			if tc.exit != 0 && tc.status != 401 && calls != 0 {
				t.Fatal("network after invalid local credential")
			}
		})
	}
}

func TestSavedOverrideLifecycle(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	for _, args := range [][]string{{"auth", "set-token"}, {"auth", "clear-token"}} {
		code, out, errs := invoke(t, args, "saved.token.signature\n", func(w http.ResponseWriter, r *http.Request) {
			if args[1] == "clear-token" {
				t.Fatal("clear made request")
			}
			if r.Header.Get("Authorization") != "saved.token.signature" {
				t.Fatal("wrong token validated")
			}
			io.WriteString(w, `{"id":"1084003247154548807","username":"owner"}`)
		}, func(o *Options) {
			o.ConfigPath = cfg
			o.LookupEnv = func(string) (string, bool) { t.Fatal("local override action read environment"); return "", false }
		})
		if code != 0 || strings.Contains(out+errs, "signature") {
			t.Fatalf("%d %s %s", code, out, errs)
		}
		if args[1] == "set-token" {
			data, e := os.ReadFile(cfg)
			if e != nil || !strings.Contains(string(data), "saved.token.signature") {
				t.Fatal("token not saved")
			}
		} else {
			if _, e := os.Stat(cfg); !os.IsNotExist(e) {
				t.Fatal("override not removed")
			}
		}
	}
}

func TestDiscoveryAndExactDMTarget(t *testing.T) {
	for _, tc := range []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"servers", "list", "--limit", "2"}, "GET", "/users/@me/guilds", `[{"id":"9007199254740993","name":"server"}]`},
		{[]string{"channels", "list", "123"}, "GET", "/guilds/123/channels", `[{"id":"456","type":0,"guild_id":"123","parent_id":"789"}]`},
		{[]string{"dms", "list"}, "GET", "/users/@me/channels", `[{"id":"456","type":3,"recipients":[{"id":"1084003247154548807","username":"friend"}]}]`},
		{[]string{"friends", "list"}, "GET", "/users/@me/relationships", `[{"id":"1084003247154548807","type":1,"user":{"id":"1084003247154548807","username":"friend"}},{"id":"99","type":2,"user":{"id":"99","username":"blocked"}}]`},
		{[]string{"dms", "open", "1084003247154548807"}, "POST", "/users/@me/channels", `{"id":"456","type":1,"recipients":[{"id":"1084003247154548807","username":"friend"}]}`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, tc.args, "", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/users/@me" {
					io.WriteString(w, `{"id":"111","username":"owner"}`)
					return
				}
				calls++
				if r.URL.Path != tc.path || r.Method != tc.method {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				if r.Method == "POST" {
					b, _ := io.ReadAll(r.Body)
					if string(b) != `{"recipients":["1084003247154548807"]}` {
						t.Errorf("wrong recipient payload %s", b)
					}
				}
				io.WriteString(w, tc.body)
			}, nil)
			if code != 0 || calls != 1 || out == "" || strings.Contains(out, "blocked") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

const sampleMessage = `{"id":"9007199254740993","channel_id":"456","author":{"id":"111","username":"owner"},"content":"hello","timestamp":"2026-09-15T00:00:00Z","attachments":[],"reactions":[],"referenced_message":null}`

func TestHistoryGetAndSearch(t *testing.T) {
	for _, tc := range []struct {
		args              []string
		path, query, body string
		exit              int
	}{
		{[]string{"messages", "list", "456"}, "/channels/456/messages", "limit=50", "[" + sampleMessage + "]", 0},
		{[]string{"messages", "list", "456", "--after", "789", "--limit", "1"}, "/channels/456/messages", "after=789&limit=1", "[" + sampleMessage + "]", 0},
		{[]string{"messages", "get", "456", "9007199254740993"}, "/channels/456/messages", "around=9007199254740993&limit=1", "[" + sampleMessage + "]", 0},
		{[]string{"messages", "get", "456", "9007199254740994"}, "/channels/456/messages", "around=9007199254740994&limit=1", "[" + sampleMessage + "]", 4},
		{[]string{"messages", "search", "--channel", "456", "--query", "hello", "--author", "111"}, "/guilds/123/messages/search", "author_id=111&channel_id=456&content=hello&limit=25&offset=0&sort_by=timestamp&sort_order=desc", `{"total_results":1,"messages":[[` + sampleMessage + `]],"is_indexed":true}`, 0},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			code, out, errs := invoke(t, tc.args, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":0,"guild_id":"123"}`)
				default:
					if r.URL.Path != tc.path || r.URL.RawQuery != tc.query {
						t.Errorf("unexpected request %s", r.URL)
					}
					io.WriteString(w, tc.body)
				}
			}, nil)
			if code != tc.exit || (code == 0 && !strings.Contains(out, `"9007199254740993"`)) {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

func TestRichMessageOutput(t *testing.T) {
	decode := func(data string) map[string]any {
		t.Helper()
		var v map[string]any
		d := json.NewDecoder(strings.NewReader(data))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, fixture := range []struct{ name, fields string }{
		{"plain", ``},
		{"embed", `,"content":"","embeds":[{"title":"Release ready","description":"synthetic.token.signature","future_count":9007199254740993}],"mentions":[{"id":"9007199254740995","username":"reader","member":{"nick":"Reviewer"}}],"mention_roles":["9007199254740997"],"mention_channels":[{"id":"789","guild_id":"123","type":0,"name":"releases"}],"mention_everyone":false,"type":0,"flags":0,"pinned":false,"webhook_id":"9007199254740999","thread":{"id":"789","thread_metadata":{"archived":false,"locked":true}}`},
		{"components", `,"content":"","flags":32768,"components":[{"type":17,"components":[{"type":10,"content":"Build failed","future_property":{"count":9007199254740993}}]}]`},
		{"forward", `,"content":"","message_reference":{"type":1,"message_id":"789","channel_id":"456"},"message_snapshots":[{"message":{"content":"Launch moved","type":0,"embeds":[],"components":[]}}]`},
		{"poll", `,"content":"","poll":{"question":{"text":"Ship today?"},"answers":[{"answer_id":1,"poll_media":{"text":"Yes"}}],"allow_multiselect":false,"expiry":null,"layout_type":1}`},
		{"sticker", `,"content":"","sticker_items":[{"id":"9007199254740995","name":"Hello","format_type":1}]`},
		{"attachment", `,"attachments":[{"id":"700","filename":"voice.ogg","size":128,"url":"https://cdn.discordapp.com/attachments/456/700/voice.ogg","title":"","description":"Spoken update","duration_secs":0,"waveform":"AAAA","flags":0,"ephemeral":false}]`},
		{"empty", `,"embeds":[],"components":[],"mentions":[],"mention_roles":[],"mention_channels":[],"message_snapshots":[],"sticker_items":[],"poll":null,"thread":null`},
	} {
		for _, operation := range []string{"list", "get", "search", "send", "edit"} {
			t.Run(fixture.name+"/"+operation, func(t *testing.T) {
				message := strings.TrimSuffix(sampleMessage, "}") + fixture.fields + `,"unselected_root":{"value":"hidden"}}`
				expected := decode(strings.ReplaceAll(message, "synthetic.token.signature", "[REDACTED]"))
				delete(expected, "unselected_root")
				expected["edited_timestamp"] = nil
				args := []string{"messages", operation, "456"}
				if operation == "get" || operation == "edit" {
					args = append(args, "9007199254740993")
				}
				if operation == "search" {
					args = []string{"messages", "search", "--channel", "456", "--query", "release"}
				}
				if operation == "send" || operation == "edit" {
					args = append(args, "--content", "hello")
				}
				reads, writes := 0, 0
				code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/users/@me":
						io.WriteString(w, `{"id":"111","username":"owner"}`)
					case "/channels/456":
						io.WriteString(w, `{"id":"456","type":0,"guild_id":"123"}`)
					case "/channels/456/messages", "/channels/456/messages/9007199254740993", "/guilds/123/messages/search":
						if r.Method == "GET" {
							reads++
							if operation == "search" {
								io.WriteString(w, `{"total_results":2,"messages":[[`+message+`]],"is_indexed":true}`)
							} else {
								io.WriteString(w, "["+message+"]")
							}
						} else {
							writes++
							method := "POST"
							if operation == "edit" {
								method = "PATCH"
							}
							body, err := io.ReadAll(r.Body)
							if err != nil || r.Method != method || string(body) != `{"content":"hello"}` {
								t.Errorf("unexpected write: %s %s %v", r.Method, body, err)
							}
							io.WriteString(w, message)
						}
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						w.WriteHeader(http.StatusNotFound)
					}
				}, nil)
				if code != 0 || errs != "" {
					t.Fatalf("exit %d: %s", code, errs)
				}
				wantReads, wantWrites := 1, 0
				if operation == "send" {
					wantReads = 0
				}
				if operation == "send" || operation == "edit" {
					wantWrites = 1
				}
				if reads != wantReads || writes != wantWrites {
					t.Fatalf("unexpected request counts: reads=%d writes=%d", reads, writes)
				}
				got := decode(out)
				if operation == "list" || operation == "search" {
					if got["order"] != "newest_first" || got["complete"] != (operation != "search") {
						t.Fatal("page contract changed", out)
					}
					if operation == "search" && (got["has_more"] != true || got["next_offset"] != json.Number("1")) {
						t.Fatal("search continuation changed", out)
					}
					items, ok := got["items"].([]any)
					if !ok || len(items) != 1 {
						t.Fatal("expected one message", out)
					}
					got = items[0].(map[string]any)
				}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("message fields changed or lost:\ngot: %s\nwant: %v", out, expected)
				}
			})
		}
	}
}

func TestMessageWritesAndAttachmentPreservation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(file, []byte("file bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	existing := strings.Replace(sampleMessage, `"attachments":[]`, `"attachments":[{"id":"700","filename":"keep.txt","size":4,"url":"https://cdn.discordapp.com/attachments/456/700/keep.txt"},{"id":"701","filename":"remove.txt","size":4,"url":"https://cdn.discordapp.com/attachments/456/701/remove.txt"}]`, 1)
	for _, tc := range []struct {
		args          []string
		stdin, method string
		check         func(*testing.T, map[string]any)
	}{
		{[]string{"messages", "send", "456", "--stdin", "--reply-to", "9007199254740993"}, "line one\nline two\n", "POST", func(t *testing.T, p map[string]any) {
			if p["content"] != "line one\nline two\n" || p["message_reference"].(map[string]any)["message_id"] != "9007199254740993" {
				t.Fatal(p)
			}
		}},
		{[]string{"messages", "send", "456", "--file", file}, "", "POST", func(t *testing.T, p map[string]any) {
			if _, ok := p["content"]; ok {
				t.Fatal("fabricated text")
			}
		}},
		{[]string{"messages", "edit", "456", "9007199254740993", "--content", "corrected"}, "", "PATCH", func(t *testing.T, p map[string]any) {
			if _, ok := p["attachments"]; ok {
				t.Fatal("text edit changed attachments")
			}
		}},
		{[]string{"messages", "edit", "456", "9007199254740993", "--file", file, "--remove-attachment", "701"}, "", "PATCH", func(t *testing.T, p map[string]any) {
			a := p["attachments"].([]any)
			if len(a) != 2 || a[0].(map[string]any)["id"] != "700" {
				t.Fatal(p)
			}
		}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			mutations := 0
			code, out, errs := invoke(t, tc.args, tc.stdin, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case r.URL.Path == "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				case r.Method == "GET":
					io.WriteString(w, "["+existing+"]")
				default:
					mutations++
					if r.Method != tc.method {
						t.Fatal(r.Method)
					}
					var p map[string]any
					if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
						if e := r.ParseMultipartForm(1 << 20); e != nil {
							t.Fatal(e)
						}
						defer r.MultipartForm.RemoveAll()
						if e := json.Unmarshal([]byte(r.FormValue("payload_json")), &p); e != nil {
							t.Fatal(e)
						}
						f, _, e := r.FormFile("files[0]")
						if e != nil {
							t.Fatal(e)
						}
						defer f.Close()
						b, _ := io.ReadAll(f)
						if string(b) != "file bytes" {
							t.Fatal(string(b))
						}
					} else {
						if e := json.NewDecoder(r.Body).Decode(&p); e != nil {
							t.Fatal(e)
						}
					}
					tc.check(t, p)
					io.WriteString(w, sampleMessage)
				}
			}, nil)
			if code != 0 || mutations != 1 || !strings.Contains(out, `"9007199254740993"`) {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

func TestModerationAndReactions(t *testing.T) {
	for _, tc := range []struct {
		args               []string
		path, method, body string
		status, exit       int
	}{
		{[]string{"messages", "delete", "456", "9007199254740993"}, "/channels/456/messages/9007199254740993", "DELETE", "", 204, 0},
		{[]string{"messages", "delete", "456", "9007199254740993"}, "/channels/456/messages/9007199254740993", "DELETE", `{"code":50013}`, 403, 3},
		{[]string{"reactions", "add", "456", "9007199254740993", "👍"}, "/channels/456/messages/9007199254740993/reactions/👍/@me", "PUT", "", 204, 0},
		{[]string{"reactions", "remove", "456", "9007199254740993", "party:777", "--user", "222"}, "/channels/456/messages/9007199254740993/reactions/party:777/222", "DELETE", "", 204, 0},
		{[]string{"reactions", "users", "456", "9007199254740993", "👍", "--after", "111"}, "/channels/456/messages/9007199254740993/reactions/👍", "GET", `[{"id":"222","username":"friend"}]`, 200, 0},
	} {
		t.Run(tc.path+tc.method+fmt.Sprint(tc.status), func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, tc.args, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					kind := 0
					if tc.status == 403 {
						kind = 1
					}
					fmt.Fprintf(w, `{"id":"456","type":%d}`, kind)
				default:
					calls++
					if r.Method != tc.method || r.URL.Path != tc.path {
						t.Errorf("%s %s", r.Method, r.URL)
					}
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body)
				}
			}, nil)
			if code != tc.exit || calls != 1 || (code == 0 && out == "") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAttachmentDownloadSafety(t *testing.T) {
	for _, scenario := range []string{"complete", "existing", "traversal", "interrupted", "expired", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			name := "photo.txt"
			if scenario == "traversal" {
				name = "../escape"
			}
			if scenario == "existing" {
				os.WriteFile(filepath.Join(dir, name), []byte("original"), 0600)
			}
			cdnCalls := 0
			code, out, errs := invoke(t, []string{"attachments", "download", "456", "9007199254740993", "700", "--dir", dir}, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				default:
					attachment := fmt.Sprintf(`"attachments":[{"id":"700","filename":%q,"size":5,"url":"https://cdn.discordapp.com/attachments/456/700/photo.txt"}]`, name)
					io.WriteString(w, "["+strings.Replace(sampleMessage, `"attachments":[]`, attachment, 1)+"]")
				}
			}, func(o *Options) {
				o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					cdnCalls++
					if r.Header.Get("Authorization") != "" {
						t.Error("token sent to CDN")
					}
					status := 200
					body := "hello"
					headers := http.Header{}
					if scenario == "interrupted" {
						body = "he"
					}
					if scenario == "expired" {
						status = 404
					}
					if scenario == "redirect" && cdnCalls == 1 {
						status = 302
						headers.Set("Location", "https://media.discordapp.net/attachments/456/700/photo.txt")
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), ContentLength: 5, Request: r}, nil
				})
			})
			entries, _ := os.ReadDir(dir)
			if scenario == "complete" || scenario == "redirect" {
				data, e := os.ReadFile(filepath.Join(dir, "photo.txt"))
				if code != 0 || e != nil || string(data) != "hello" {
					t.Fatalf("%d %s %s", code, out, errs)
				}
			} else {
				if code == 0 {
					t.Fatal("unsafe download succeeded")
				}
				if scenario == "existing" {
					data, _ := os.ReadFile(filepath.Join(dir, name))
					if string(data) != "original" {
						t.Fatal("existing file changed")
					}
				} else if len(entries) != 0 {
					t.Fatal("partial file survived")
				}
			}
			if (scenario == "existing" || scenario == "traversal") && cdnCalls != 0 {
				t.Fatal("unnecessary CDN request")
			}
		})
	}
}

func TestFailureContractsAndBoundedWaits(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		code, body   string
	}{
		{400, 2, "bad_request", `{"message":"synthetic.token.signature"}`}, {401, 3, "authentication_failed", `{}`}, {403, 3, "permission_denied", `{}`}, {404, 4, "not_found", `{}`}, {429, 5, "rate_limited", `{"retry_after":30}`}, {202, 1, "indexing_delay", `{"retry_after":30}`}, {500, 1, "upstream_error", `{}`}, {302, 1, "upstream_error", `{}`}, {400, 3, "user_action_required", `{"captcha_key":["required"]}`}, {200, 1, "invalid_upstream_data", `{"id":9007199254740993}`},
	} {
		t.Run(fmt.Sprint(tc.status)+tc.code, func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, []string{"--timeout", "200ms", "servers", "list"}, "", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/users/@me" {
					io.WriteString(w, `{"id":"111","username":"owner"}`)
					return
				}
				calls++
				w.Header().Set("Location", "/credential-trap")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}, nil)
			if code != tc.exit || out != "" || !strings.Contains(errs, `"code":"`+tc.code+`"`) || strings.Contains(errs, "signature") || calls != 1 {
				t.Fatalf("%d calls=%d %s %s", code, calls, out, errs)
			}
		})
	}
}
func TestConfirmedRetriesAndLostSendResponse(t *testing.T) {
	for _, scenario := range []string{"rate limit", "indexing", "lost send"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			args := []string{"messages", "search", "--server", "123"}
			if scenario == "lost send" {
				args = []string{"messages", "send", "456", "--content", "one send"}
			}
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":1}`)
				default:
					calls++
					if scenario == "lost send" {
						conn, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Fatal(e)
						}
						conn.Close()
						return
					}
					if calls == 1 {
						status := 429
						if scenario == "indexing" {
							status = 202
						}
						w.WriteHeader(status)
						io.WriteString(w, `{"retry_after":0.001}`)
						return
					}
					io.WriteString(w, `{"total_results":1,"messages":[[`+sampleMessage+`]],"is_indexed":true}`)
				}
			}, nil)
			if scenario == "lost send" {
				if code != 1 || calls != 1 || !strings.Contains(errs, "uncertain_write") {
					t.Fatalf("%d %d %s %s", code, calls, out, errs)
				}
			} else if code != 0 || calls != 2 {
				t.Fatalf("%d %d %s %s", code, calls, out, errs)
			}
		})
	}
}
func TestInvalidInputsDoNotReadCredentials(t *testing.T) {
	for _, args := range [][]string{
		{"messages", "send", "456", "--content", strings.Repeat("x", 2001)},
		{"messages", "send", "456", "--file", "/does-not-exist"},
		{"messages", "send", "456", "--stdin"},
		{"messages", "list", "456", "--before", "111", "--after", "222"},
		{"messages", "search", "--server", "123", "--channel", "456"},
		{"messages", "search"}, {"messages", "search", "--server", "123", "--offset", "-1"},
		{"reactions", "add", "456", "789", "bad/emoji"},
		{"--timeout", "0s", "auth", "status"},
		{"dms", "create-group"},
	} {
		t.Run(strings.Join(args[:min(len(args), 3)], " "), func(t *testing.T) {
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) { t.Fatal("network after invalid input") }, func(o *Options) {
				o.LookupEnv = func(string) (string, bool) { t.Fatal("credentials read after invalid input"); return "", false }
			})
			if code != 2 || out != "" || !strings.Contains(errs, "invalid_arguments") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

func TestMalformedSavedTokenNeverFallsBack(t *testing.T) {
	for _, saved := range []string{`{"token":null}`, `{"token":42}`, `null`, "{}\n{}"} {
		code, _, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) { t.Fatal("network for malformed config") }, func(o *Options) {
			o.LookupEnv = func(string) (string, bool) { return "", false }
			os.WriteFile(o.ConfigPath, []byte(saved), 0600)
		})
		if code != 1 || !strings.Contains(errs, "config_error") {
			t.Fatalf("%d %s", code, errs)
		}
	}
}
func TestSecretsAreRedactedFromContinuationErrors(t *testing.T) {
	code, _, errs := invoke(t, []string{"--timeout", "100ms", "messages", "search", "--server", "123", "--query", "synthetic.token.signature"}, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/@me" {
			io.WriteString(w, `{"id":"111","username":"owner"}`)
			return
		}
		w.WriteHeader(202)
		io.WriteString(w, `{"retry_after":10}`)
	}, nil)
	if code != 1 || strings.Contains(errs, "synthetic.token.signature") {
		t.Fatalf("%d %s", code, errs)
	}
}
func TestDeadlineCancelsInputAndHTTP(t *testing.T) {
	pipe, writer := io.Pipe()
	defer pipe.Close()
	defer writer.Close()
	var out, errs bytes.Buffer
	start := time.Now()
	code := Run(context.Background(), []string{"--timeout", "10ms", "messages", "send", "456", "--stdin"}, pipe, &out, &errs, Options{LookupEnv: func(string) (string, bool) { t.Fatal("credentials read while waiting for content"); return "", false }})
	if code != 1 || !strings.Contains(errs.String(), "cancelled") || time.Since(start) > time.Second {
		t.Fatalf("%d %s", code, errs.String())
	}
	code, _, text := invoke(t, []string{"--timeout", "20ms", "auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, nil)
	if code != 1 || !strings.Contains(text, "cancelled") {
		t.Fatalf("%d %s", code, text)
	}
}

func TestEveryCommandHelp(t *testing.T) {
	for _, command := range []string{"auth", "auth status", "auth set-token", "auth clear-token", "servers", "servers list", "channels", "channels list", "dms", "dms list", "dms open", "friends", "friends list", "messages", "messages list", "messages get", "messages search", "messages send", "messages edit", "messages delete", "attachments", "attachments download", "reactions", "reactions list", "reactions users", "reactions add", "reactions remove"} {
		args := append(strings.Fields(command), "--help")
		code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) { t.Fatal("help made network request") }, func(o *Options) {
			o.LookupEnv = func(string) (string, bool) { t.Fatal("help accessed credentials"); return "", false }
		})
		if code != 0 || out == "" || errs != "" {
			t.Fatalf("%s: %d %s %s", command, code, out, errs)
		}
	}
}

func TestSearchContinuationAtResultCeiling(t *testing.T) {
	for _, limit := range []string{"25", "1"} {
		code, out, errs := invoke(t, []string{"messages", "search", "--server", "123", "--offset", "9975", "--limit", limit}, "", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/users/@me" {
				io.WriteString(w, `{"id":"111","username":"owner"}`)
				return
			}
			if r.URL.Query().Get("limit") != limit {
				t.Error("search limit changed")
			}
			count, _ := strconv.Atoi(limit)
			groups := make([]string, count)
			for i := range groups {
				m := strings.Replace(sampleMessage, "9007199254740993", fmt.Sprint(9007199254740993+i), 1)
				groups[i] = "[" + m + "]"
			}
			io.WriteString(w, `{"total_results":11000,"messages":[`+strings.Join(groups, ",")+`],"is_indexed":true}`)
		}, nil)
		if code != 0 || !strings.Contains(out, `"continuation_limited":true`) || strings.Contains(out, `"next_offset"`) || !strings.Contains(out, `"complete":false`) {
			t.Fatalf("%d %s %s", code, out, errs)
		}
	}
}

func TestSearchFiltersAcrossScopes(t *testing.T) {
	for _, scope := range []struct {
		name, channel, path string
		args                []string
	}{
		{"server", "", "/guilds/123/messages/search", []string{"--server", "123", "--in-channel", "456", "--in-channel", "789"}},
		{"server channel", `{"id":"456","type":0,"guild_id":"123"}`, "/guilds/123/messages/search", []string{"--channel", "456"}},
		{"DM", `{"id":"456","type":1}`, "/channels/456/messages/search", []string{"--channel", "456"}},
		{"group DM", `{"id":"456","type":3}`, "/channels/456/messages/search", []string{"--channel", "456"}},
		{"wrong returned channel", "", "/guilds/123/messages/search", []string{"--server", "123", "--in-channel", "789"}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			args := append([]string{"messages", "search"}, scope.args...)
			args = append(args, "--query", "release", "--author", "111", "--author", "222", "--mentions", "333", "--mentions", "444", "--author-type", "user", "--author-type", "webhook", "--author-type=-bot", "--pinned", "--before", "9999999999999999", "--after", "100", "--sort", "relevance")
			for _, has := range []string{"image", "video", "link", "file", "embed", "sound", "poll", "sticker", "forward", "-forward", "-image"} {
				args = append(args, "--has="+has)
			}
			want, _ := url.ParseQuery("author_id=111&author_id=222&mentions=333&mentions=444&author_type=user&author_type=webhook&author_type=-bot&has=image&has=video&has=link&has=file&has=embed&has=sound&has=poll&has=sticker&has=snapshot&has=-snapshot&has=-image&pinned=true&content=release&max_id=9999999999999999&min_id=100&limit=25&offset=0&sort_by=relevance&sort_order=desc")
			if scope.name == "server" {
				want["channel_id"] = []string{"456", "789"}
			}
			if scope.name == "server channel" {
				want["channel_id"] = []string{"456"}
			}
			if scope.name == "wrong returned channel" {
				want["channel_id"] = []string{"789"}
			}
			calls := 0
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, scope.channel)
				default:
					calls++
					if r.Method != "GET" || r.URL.Path != scope.path || !reflect.DeepEqual(r.URL.Query(), want) {
						t.Errorf("unexpected search: %s %s", r.Method, r.URL)
					}
					io.WriteString(w, `{"total_results":1,"messages":[[`+sampleMessage+`]],"doing_deep_historical_index":false}`)
				}
			}, nil)
			if scope.name == "wrong returned channel" {
				if code != 1 || out != "" || !strings.Contains(errs, "invalid_upstream_data") {
					t.Fatalf("%d %s %s", code, out, errs)
				}
				return
			}
			if code != 0 || errs != "" || calls != 1 || !strings.Contains(out, `"order":"relevance"`) {
				t.Fatalf("%d calls=%d %s %s", code, calls, out, errs)
			}
		})
	}
}

func TestSearchCalendarBounds(t *testing.T) {
	for _, tc := range []struct {
		name, localZone string
		args            []string
		start, end      string
	}{
		{"UTC day", "UTC", []string{"--on", "2026-09-14"}, "2026-09-14T00:00:00Z", "2026-09-15T00:00:00Z"},
		{"23 hour day", "America/Los_Angeles", []string{"--on", "2026-03-08"}, "2026-03-08T08:00:00Z", "2026-03-09T07:00:00Z"},
		{"25 hour day", "America/Los_Angeles", []string{"--on", "2026-11-01"}, "2026-11-01T07:00:00Z", "2026-11-02T08:00:00Z"},
		{"exclusive calendar range", "UTC", []string{"--after-date", "2026-09-12", "--before-date", "2026-09-14"}, "2026-09-13T00:00:00Z", "2026-09-14T00:00:00Z"},
		{"before only", "UTC", []string{"--before-date", "2026-09-14"}, "", "2026-09-14T00:00:00Z"},
		{"after only", "UTC", []string{"--after-date", "2026-09-14"}, "2026-09-15T00:00:00Z", ""},
		{"epoch day", "UTC", []string{"--on", "2015-01-01"}, "", "2015-01-02T00:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location, err := time.LoadLocation(tc.localZone)
			if err != nil {
				t.Fatal(err)
			}
			previousLocal := time.Local
			time.Local = location
			t.Cleanup(func() { time.Local = previousLocal })
			calls := 0
			code, out, errs := invoke(t, append([]string{"messages", "search", "--server", "123"}, tc.args...), "", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/users/@me" {
					io.WriteString(w, `{"id":"111","username":"owner"}`)
					return
				}
				calls++
				for key, instant := range map[string]string{"min_id": tc.start, "max_id": tc.end} {
					value := r.URL.Query().Get(key)
					if instant == "" {
						if value != "" {
							t.Errorf("unexpected %s=%s", key, value)
						}
						continue
					}
					id, err := strconv.ParseUint(value, 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					if key == "min_id" {
						id++
					} // Strict lower bound must include the first midnight ID.
					got := time.UnixMilli(int64(id>>22) + 1420070400000).UTC().Format(time.RFC3339)
					if got != instant || id&((1<<22)-1) != 0 {
						t.Errorf("%s: boundary %s, want %s", key, got, instant)
					}
				}
				io.WriteString(w, `{"total_results":0,"messages":[]}`)
			}, nil)
			if code != 0 || errs != "" || calls != 1 {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

func TestSearchOrderAndCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name, sort, state       string
		offset, total           int
		ids, wantIDs            []int
		complete, more, limited bool
		next                    *int
		errorCode               string
	}{
		{name: "newest", sort: "newest", total: 3, ids: []int{2, 1, 3}, wantIDs: []int{3, 2, 1}, complete: true},
		{name: "oldest", sort: "oldest", total: 3, ids: []int{2, 1, 3}, wantIDs: []int{1, 2, 3}, complete: true},
		{name: "relevance retains rank and deduplicates", sort: "relevance", total: 3, ids: []int{2, 1, 2, 3}, wantIDs: []int{2, 1, 3}, complete: true},
		{name: "deep indexing", sort: "newest", state: `,"doing_deep_historical_index":true,"is_indexed":true`, total: 3, ids: []int{3, 2, 1}, wantIDs: []int{3, 2, 1}, more: true, next: new(3)},
		{name: "legacy partial index", sort: "newest", state: `,"is_indexed":false`, total: 3, ids: []int{3, 2, 1}, wantIDs: []int{3, 2, 1}, more: true, next: new(3)},
		{name: "short page", sort: "newest", total: 10, ids: []int{3, 2}, wantIDs: []int{3, 2}, more: true, next: new(2)},
		{name: "empty page with more", sort: "newest", total: 10, more: true, limited: true},
		{name: "empty exhausted", sort: "newest", total: 0, complete: true},
		{name: "empty indexing", sort: "newest", state: `,"doing_deep_historical_index":true`, total: 10, errorCode: "indexing_delay"},
		{name: "malformed index state", sort: "newest", state: `,"doing_deep_historical_index":"false"`, total: 0, errorCode: "invalid_upstream_data"},
		{name: "last legal continuation", sort: "newest", offset: 9974, total: 11000, ids: []int{1}, wantIDs: []int{1}, more: true, next: new(9975)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var groups []string
			for _, id := range tc.ids {
				groups = append(groups, "["+strings.Replace(sampleMessage, "9007199254740993", fmt.Sprint(9007199254740992+id), 1)+"]")
			}
			code, out, errs := invoke(t, []string{"messages", "search", "--server", "123", "--sort", tc.sort, "--limit", "3", "--offset", strconv.Itoa(tc.offset)}, "", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/users/@me" {
					io.WriteString(w, `{"id":"111","username":"owner"}`)
					return
				}
				by, direction := "timestamp", "desc"
				if tc.sort == "oldest" {
					direction = "asc"
				}
				if tc.sort == "relevance" {
					by = "relevance"
				}
				if r.URL.Query().Get("sort_by") != by || r.URL.Query().Get("sort_order") != direction {
					t.Errorf("wrong sort request: %s", r.URL)
				}
				io.WriteString(w, fmt.Sprintf(`{"total_results":%d,"messages":[%s]%s}`, tc.total, strings.Join(groups, ","), tc.state))
			}, nil)
			if tc.errorCode != "" {
				if code != 1 || out != "" || !strings.Contains(errs, tc.errorCode) {
					t.Fatalf("%d %s %s", code, out, errs)
				}
				return
			}
			var page struct {
				Items    []struct{ ID string }
				Complete bool
				Order    string
				HasMore  bool `json:"has_more"`
				Limited  bool `json:"continuation_limited"`
				Next     *int `json:"next_offset"`
			}
			if code != 0 || errs != "" || json.Unmarshal([]byte(out), &page) != nil {
				t.Fatalf("%d %s %s", code, out, errs)
			}
			wantOrder := tc.sort + "_first"
			if tc.sort == "relevance" {
				wantOrder = "relevance"
			}
			if page.Complete != tc.complete || page.HasMore != tc.more || page.Limited != tc.limited || !reflect.DeepEqual(page.Next, tc.next) || page.Order != wantOrder {
				t.Fatalf("unexpected page: %s", out)
			}
			if len(page.Items) != len(tc.wantIDs) {
				t.Fatal(out)
			}
			for i, id := range tc.wantIDs {
				if page.Items[i].ID != fmt.Sprint(9007199254740992+id) {
					t.Fatal("wrong order", out)
				}
			}
		})
	}
}

func TestSearchInvalidFiltersBeforeAuthentication(t *testing.T) {
	cases := [][]string{
		{"--author", "111,222"}, {"--author", ""}, {"--mentions", "bad"}, {"--in-channel", "bad"},
		{"--has", "unknown"}, {"--has=--image"}, {"--has", ""}, {"--author-type", "human"}, {"--sort", "random"},
		{"--pinned=false"}, {"--offset", "9976"}, {"--offset", "9999"}, {"--limit", "26"},
		{"--query", strings.Repeat("x", 1025)},
		{"--on", "2026-02-30"}, {"--on", "2026-2-03"}, {"--on", "2014-01-01"}, {"--on", "9999-01-01"},
		{"--on", "2026-09-14", "--timezone", "UTC"},
		{"--on", "2026-09-14", "--before", "123"}, {"--before-date", "2026-09-14", "--after", "123"},
		{"--on", "2026-09-14", "--after-date", "2026-09-12"}, {"--after-date", "2026-09-15", "--before-date", "2026-09-14"},
	}
	for _, flag := range []string{"--author", "--mentions", "--in-channel"} {
		max := 100
		if flag == "--in-channel" {
			max = 500
		}
		var args []string
		for i := 0; i <= max; i++ {
			args = append(args, flag, "111")
		}
		cases = append(cases, args)
	}
	for i, filters := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			code, out, errs := invoke(t, append([]string{"messages", "search", "--server", "123"}, filters...), "", func(w http.ResponseWriter, r *http.Request) { t.Error("network for invalid filter") }, func(o *Options) {
				o.LookupEnv = func(string) (string, bool) { t.Error("credentials read for invalid filter"); return "", false }
			})
			if code != 2 || out != "" || !strings.Contains(errs, "invalid_arguments") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
			if filters[0] == "--pinned=false" && !strings.Contains(errs, "does not reliably exclude pinned") {
				t.Fatal("missing actionable false-pin error", errs)
			}
		})
	}
	code, out, errs := invoke(t, []string{"messages", "search", "--channel", "456", "--in-channel", "789"}, "", func(w http.ResponseWriter, r *http.Request) { t.Error("network for mixed scopes") }, func(o *Options) {
		o.LookupEnv = func(string) (string, bool) { t.Error("credentials read for mixed scopes"); return "", false }
	})
	if code != 2 || out != "" || !strings.Contains(errs, "--in-channel requires --server") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

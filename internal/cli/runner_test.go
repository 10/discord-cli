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

const dm = `{"id":"456","type":1}`

// upstream serves the authenticated identity and channel 456, delegating other requests.
func upstream(channel string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me":
			io.WriteString(w, `{"id":"111","username":"owner"}`)
		case "/channels/456":
			io.WriteString(w, channel)
		default:
			next(w, r)
		}
	}
}

func TestInvalidInputsDoNotReadCredentials(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--token", "a-secret"}, "invalid_arguments"},
		{[]string{"attachments", "download", "456", "789", "--dir", t.TempDir()}, "discord attachments download --help"},
		{[]string{"threads", "archived", "456", "--type", "joined-private", "--before", "2026-09-14T00:00:00Z"}, "invalid_arguments"},
		{[]string{"messages", "search", "--server", "123", "--pinned=false"}, "does not reliably exclude pinned"},
		{[]string{"messages", "search", "--server", "123", "--on", "2014-01-01"}, "invalid_arguments"},
		{[]string{"reactions", "add", "456", "789", "bad/emoji"}, "invalid_arguments"},
	} {
		t.Run(strings.Join(tc.args[:min(len(tc.args), 3)], " "), func(t *testing.T) {
			code, out, errs := invoke(t, tc.args, "", func(w http.ResponseWriter, r *http.Request) { t.Error("network after invalid input") }, func(o *Options) {
				o.LookupEnv = func(string) (string, bool) { t.Error("credentials read after invalid input"); return "", false }
			})
			if code != 2 || out != "" || !strings.Contains(errs, "invalid_arguments") || !strings.Contains(errs, tc.want) || strings.Contains(errs, "a-secret") {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

func TestExplicitCredentialPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		set       bool
		config    string
		exit      int
		source    string
	}{
		{"environment", "synthetic.token.signature", true, `broken`, 0, "environment"},
		{"empty environment", "", true, `{"token":"saved.token.signature"}`, 3, ""},
		{"saved", "", false, `{"token":"saved.token.signature"}`, 0, "configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) {
				calls++
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
			if tc.exit != 0 && calls != 0 {
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

func TestMalformedSavedTokenNeverFallsBack(t *testing.T) {
	for _, saved := range []string{`{"token":null}`, `null`} {
		code, _, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) { t.Fatal("network for malformed config") }, func(o *Options) {
			o.LookupEnv = func(string) (string, bool) { return "", false }
			os.WriteFile(o.ConfigPath, []byte(saved), 0600)
		})
		if code != 1 || !strings.Contains(errs, "config_error") {
			t.Fatalf("%d %s", code, errs)
		}
	}
}

func TestExactDMTarget(t *testing.T) {
	calls := 0
	code, out, errs := invoke(t, []string{"dms", "open", "1084003247154548807"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/users/@me/channels" || string(b) != `{"recipients":["1084003247154548807"]}` {
			t.Errorf("unexpected request %s %s %s", r.Method, r.URL, b)
		}
		io.WriteString(w, `{"id":"456","type":1,"recipients":[{"id":"1084003247154548807","username":"friend"}]}`)
	}), nil)
	if code != 0 || calls != 1 || out == "" {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

const sampleMessage = `{"id":"9007199254740993","channel_id":"456","author":{"id":"111","username":"owner"},"content":"hello","timestamp":"2026-09-15T00:00:00Z","attachments":[],"reactions":[],"referenced_message":null}`

func TestExactMessageRejectsNeighbor(t *testing.T) {
	// Discord's around query returns nearby messages when the requested one is gone.
	code, out, errs := invoke(t, []string{"messages", "get", "456", "9007199254740994"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "["+sampleMessage+"]")
	}), nil)
	if code != 4 || out != "" || !strings.Contains(errs, "not_found") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

func TestRichMessageOutput(t *testing.T) {
	// Every command shares the message schema; one rich fixture covers raw nested
	// schemas, typed zero values, large integers, and redaction.
	message := strings.TrimSuffix(sampleMessage, "}") + `,"content":"","embeds":[{"title":"Release ready","description":"synthetic.token.signature","future_count":9007199254740993}],"mentions":[{"id":"9007199254740995","username":"reader","member":{"nick":"Reviewer"}}],"mention_roles":["9007199254740997"],"mention_channels":[{"id":"789","guild_id":"123","type":0,"name":"releases"}],"mention_everyone":false,"type":0,"flags":32768,"pinned":false,"webhook_id":"9007199254740999","thread":{"id":"789","thread_metadata":{"archived":false,"locked":true}},"components":[{"type":17,"components":[{"type":10,"content":"Build failed","future_property":{"count":9007199254740993}}]}],"message_reference":{"type":1,"message_id":"789","channel_id":"456"},"message_snapshots":[{"message":{"content":"Launch moved","type":0,"embeds":[],"components":[]}}],"poll":{"question":{"text":"Ship today?"},"answers":[{"answer_id":1,"poll_media":{"text":"Yes"}}],"allow_multiselect":false,"expiry":null,"layout_type":1},"sticker_items":[{"id":"9007199254740995","name":"Hello","format_type":1}],"attachments":[{"id":"700","filename":"voice.ogg","size":128,"url":"https://cdn.discordapp.com/attachments/456/700/voice.ogg","title":"","description":"Spoken update","duration_secs":0,"waveform":"AAAA","flags":0,"ephemeral":false}],"unselected_root":{"value":"hidden"}}`
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
	expected := decode(strings.ReplaceAll(message, "synthetic.token.signature", "[REDACTED]"))
	delete(expected, "unselected_root")
	expected["edited_timestamp"] = nil
	code, out, errs := invoke(t, []string{"messages", "get", "456", "9007199254740993"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "["+message+"]")
	}), nil)
	if code != 0 || errs != "" {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := decode(out); !reflect.DeepEqual(got, expected) {
		t.Fatalf("message fields changed or lost:\ngot: %s\nwant: %v", out, expected)
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
			code, out, errs := invoke(t, tc.args, tc.stdin, upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					io.WriteString(w, "["+existing+"]")
					return
				}
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
				} else if e := json.NewDecoder(r.Body).Decode(&p); e != nil {
					t.Fatal(e)
				}
				tc.check(t, p)
				io.WriteString(w, sampleMessage)
			}), nil)
			if code != 0 || mutations != 1 || !strings.Contains(out, `"9007199254740993"`) {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

func TestDeleteAndReactions(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		path, method string
	}{
		{[]string{"messages", "delete", "456", "9007199254740993"}, "/channels/456/messages/9007199254740993", "DELETE"},
		{[]string{"reactions", "add", "456", "9007199254740993", "👍"}, "/channels/456/messages/9007199254740993/reactions/👍/@me", "PUT"},
		{[]string{"reactions", "remove", "456", "9007199254740993", "<:party:777>", "--user", "222"}, "/channels/456/messages/9007199254740993/reactions/party:777/222", "DELETE"},
	} {
		t.Run(strings.Join(tc.args[:2], " "), func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, tc.args, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("%s %s", r.Method, r.URL)
				}
				w.WriteHeader(204)
			}), nil)
			if code != 0 || calls != 1 || out == "" {
				t.Fatalf("%d %s %s", code, out, errs)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// withAttachment returns a channel-456 message list containing one attachment object.
func withAttachment(attachment string) http.HandlerFunc {
	return upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "["+strings.Replace(sampleMessage, `"attachments":[]`, `"attachments":[`+attachment+`]`, 1)+"]")
	})
}

func TestAttachmentDownloadSafety(t *testing.T) {
	for _, scenario := range []string{"existing", "traversal", "redirect"} {
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
			attachment := fmt.Sprintf(`{"id":"700","filename":%q,"size":5,"url":"https://cdn.discordapp.com/attachments/456/700/photo.txt"}`, name)
			code, out, errs := invoke(t, []string{"attachments", "download", "456", "9007199254740993", "700", "--dir", dir}, "", withAttachment(attachment), func(o *Options) {
				o.DownloadTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					cdnCalls++
					if r.Header.Get("Authorization") != "" {
						t.Error("token sent to CDN")
					}
					status, headers := 200, http.Header{}
					if cdnCalls == 1 {
						status = 302
						headers.Set("Location", "https://media.discordapp.net/attachments/456/700/photo.txt")
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("hello")), ContentLength: 5, Request: r}, nil
				})
			})
			if scenario == "redirect" {
				data, e := os.ReadFile(filepath.Join(dir, "photo.txt"))
				if code != 0 || e != nil || string(data) != "hello" {
					t.Fatalf("%d %s %s", code, out, errs)
				}
				return
			}
			if data, _ := os.ReadFile(filepath.Join(dir, "photo.txt")); code == 0 || cdnCalls != 0 || (scenario == "existing" && string(data) != "original") {
				t.Fatalf("unsafe download: %d %s %s CDN=%d", code, out, errs, cdnCalls)
			}
		})
	}
}

func TestFailureContractsAndBoundedWaits(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		code, body   string
	}{
		{400, 2, "bad_request", `{"message":"synthetic.token.signature"}`}, {401, 3, "authentication_failed", `{}`}, {404, 4, "not_found", `{}`}, {429, 5, "rate_limited", `{"retry_after":30}`}, {302, 1, "upstream_error", `{}`}, {400, 3, "user_action_required", `{"captcha_key":["required"]}`}, {200, 1, "invalid_upstream_data", `{"id":9007199254740993}`},
	} {
		t.Run(fmt.Sprint(tc.status)+tc.code, func(t *testing.T) {
			calls := 0
			code, out, errs := invoke(t, []string{"--timeout", "200ms", "servers", "list"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/credential-trap")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}), nil)
			if code != tc.exit || out != "" || !strings.Contains(errs, `"code":"`+tc.code+`"`) || strings.Contains(errs, "signature") || calls != 1 {
				t.Fatalf("%d calls=%d %s %s", code, calls, out, errs)
			}
		})
	}
}

func TestConfirmedRetriesAndLostSendResponse(t *testing.T) {
	for _, scenario := range []string{"rate limit", "lost send"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			args := []string{"messages", "search", "--server", "123"}
			if scenario == "lost send" {
				args = []string{"messages", "send", "456", "--content", "one send"}
			}
			code, out, errs := invoke(t, args, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
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
					w.WriteHeader(429)
					io.WriteString(w, `{"retry_after":0.001}`)
					return
				}
				io.WriteString(w, `{"total_results":1,"messages":[[`+sampleMessage+`]],"is_indexed":true}`)
			}), nil)
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

func TestSecretsAreRedactedFromContinuationErrors(t *testing.T) {
	code, _, errs := invoke(t, []string{"--timeout", "100ms", "messages", "search", "--server", "123", "--query", "synthetic.token.signature"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		io.WriteString(w, `{"retry_after":10}`)
	}), nil)
	if code != 1 || !strings.Contains(errs, "indexing_delay") || strings.Contains(errs, "synthetic.token.signature") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestDeadlineCancelsHTTP(t *testing.T) {
	code, _, errs := invoke(t, []string{"--timeout", "20ms", "auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, nil)
	if code != 1 || !strings.Contains(errs, "cancelled") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestSearchFiltersAcrossScopes(t *testing.T) {
	for _, scope := range []struct {
		name, channel, path string
		args                []string
	}{
		{"server", "", "/guilds/123/messages/search", []string{"--server", "123", "--in-channel", "456", "--in-channel", "789"}},
		{"server channel", `{"id":"456","type":0,"guild_id":"123"}`, "/guilds/123/messages/search", []string{"--channel", "456"}},
		{"DM", dm, "/channels/456/messages/search", []string{"--channel", "456"}},
		{"wrong returned channel", "", "/guilds/123/messages/search", []string{"--server", "123", "--in-channel", "789"}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			args := append([]string{"messages", "search"}, scope.args...)
			args = append(args, "--query", "release", "--author", "111", "--author", "222", "--mentions", "333", "--mentions", "444", "--author-type", "user", "--author-type", "webhook", "--author-type=-bot", "--pinned", "--before", "9999999999999999", "--after", "100", "--sort", "relevance")
			for _, has := range []string{"image", "video", "link", "file", "embed", "sound", "poll", "sticker", "forward", "-forward", "-image"} {
				args = append(args, "--has="+has)
			}
			want, _ := url.ParseQuery("author_id=111&author_id=222&mentions=333&mentions=444&author_type=user&author_type=webhook&author_type=-bot&has=image&has=video&has=link&has=file&has=embed&has=sound&has=poll&has=sticker&has=snapshot&has=-snapshot&has=-image&pinned=true&content=release&max_id=9999999999999999&min_id=100&limit=25&offset=0&sort_by=relevance&sort_order=desc")
			switch scope.name {
			case "server":
				want["channel_id"] = []string{"456", "789"}
			case "server channel":
				want["channel_id"] = []string{"456"}
			case "wrong returned channel":
				want["channel_id"] = []string{"789"}
			}
			calls := 0
			code, out, errs := invoke(t, args, "", upstream(scope.channel, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != scope.path || !reflect.DeepEqual(r.URL.Query(), want) {
					t.Errorf("unexpected search: %s %s", r.Method, r.URL)
				}
				io.WriteString(w, `{"total_results":1,"messages":[[`+sampleMessage+`]],"doing_deep_historical_index":false}`)
			}), nil)
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
		{"23 hour day", "America/Los_Angeles", []string{"--on", "2026-03-08"}, "2026-03-08T08:00:00Z", "2026-03-09T07:00:00Z"},
		{"exclusive calendar range", "UTC", []string{"--after-date", "2026-09-12", "--before-date", "2026-09-14"}, "2026-09-13T00:00:00Z", "2026-09-14T00:00:00Z"},
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
			code, out, errs := invoke(t, append([]string{"messages", "search", "--server", "123"}, tc.args...), "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
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
			}), nil)
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
		{name: "empty page with more", sort: "newest", total: 10, more: true, limited: true},
		{name: "empty indexing", sort: "newest", state: `,"doing_deep_historical_index":true`, total: 10, errorCode: "indexing_delay"},
		{name: "last legal continuation", sort: "newest", offset: 9974, total: 11000, ids: []int{1}, wantIDs: []int{1}, more: true, next: new(9975)},
		{name: "continuation beyond offset ceiling", sort: "newest", offset: 9975, total: 11000, ids: []int{1}, wantIDs: []int{1}, more: true, limited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var groups []string
			for _, id := range tc.ids {
				groups = append(groups, "["+strings.Replace(sampleMessage, "9007199254740993", fmt.Sprint(9007199254740992+id), 1)+"]")
			}
			code, out, errs := invoke(t, []string{"messages", "search", "--server", "123", "--sort", tc.sort, "--limit", "3", "--offset", strconv.Itoa(tc.offset)}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
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
			}), nil)
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

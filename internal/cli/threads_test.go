package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func threadFixture(id, parent string, kind int, archived bool, timestamp string) string {
	return fmt.Sprintf(`{"id":%q,"type":%d,"guild_id":"123","parent_id":%q,"name":"topic","owner_id":"111","message_count":0,"member_count":1,"flags":0,"applied_tags":["700"],"thread_metadata":{"archived":%t,"archive_timestamp":%q,"auto_archive_duration":1440,"locked":false}}`, id, kind, parent, archived, timestamp)
}

func TestActiveThreadDiscovery(t *testing.T) {
	cursor := ""
	for pageNumber := range 2 {
		args := []string{"threads", "list", "456", "--limit", "3"}
		if cursor != "" {
			args = append(args, "--before", cursor)
		}
		calls := 0
		code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("discovery mutated state: %s", r.Method)
			}
			if r.URL.Path == "/users/@me" {
				io.WriteString(w, `{"id":"111","username":"owner"}`)
				return
			}
			calls++
			want := url.Values{"archived": {"false"}, "sort_by": {"creation_time"}, "sort_order": {"desc"}, "limit": {"3"}}
			if pageNumber == 1 {
				want.Set("max_id", "9007199254740993")
			}
			if r.URL.Path != "/channels/456/threads/search" || !reflect.DeepEqual(r.URL.Query(), want) {
				t.Errorf("unexpected request: %s", r.URL)
			}
			if pageNumber == 1 {
				io.WriteString(w, `{"threads":[],"has_more":false,"total_results":0}`)
				return
			}
			io.WriteString(w, `{"threads":[`+
				threadFixture("9007199254740993", "456", 11, false, "2026-09-15T00:00:00Z")+","+
				threadFixture("9007199254740995", "456", 12, false, "2026-09-15T00:00:00Z")+","+
				threadFixture("9007199254740994", "456", 10, false, "2026-09-15T00:00:00Z")+`],"members":[],"has_more":true,"total_results":4}`)
		}, nil)
		var page struct {
			Items []struct {
				ID             string
				ParentID       string `json:"parent_id"`
				Flags          *int
				MessageCount   *int                    `json:"message_count"`
				AppliedTags    []string                `json:"applied_tags"`
				ThreadMetadata struct{ Archived bool } `json:"thread_metadata"`
			}
			Complete   bool
			HasMore    bool   `json:"has_more"`
			NextBefore string `json:"next_before"`
			Order      string
		}
		if code != 0 || errs != "" || calls != 1 || json.Unmarshal([]byte(out), &page) != nil || !page.Complete || page.Order != "id_descending" {
			t.Fatalf("exit=%d calls=%d output=%s errors=%s", code, calls, out, errs)
		}
		want := []string{"9007199254740995", "9007199254740994", "9007199254740993"}
		if pageNumber == 1 {
			want = []string{}
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, item.ID)
			if item.ParentID != "456" || item.ThreadMetadata.Archived || item.Flags == nil || *item.Flags != 0 || item.MessageCount == nil || *item.MessageCount != 0 || !reflect.DeepEqual(item.AppliedTags, []string{"700"}) {
				t.Fatal("thread identity or metadata changed", out)
			}
		}
		if !reflect.DeepEqual(got, want) || page.HasMore != (pageNumber == 0) || (pageNumber == 0 && page.NextBefore != "9007199254740993") || (pageNumber == 1 && (page.NextBefore != "" || !strings.Contains(out, `"items":[]`))) {
			t.Fatal("wrong threads or continuation", out)
		}
		cursor = page.NextBefore
	}
}

func TestArchivedThreadContinuation(t *testing.T) {
	for _, tc := range []struct {
		kind, path, order, cursor string
		typeID                    int
		ids                       []string
	}{
		{"public", "/channels/456/threads/archived/public", "archive_timestamp_descending", "2026-09-14T00:00:00Z", 11, []string{"9007199254740993", "9007199254740994"}},
		{"private", "/channels/456/threads/archived/private", "archive_timestamp_descending", "2026-09-14T00:00:00Z", 12, []string{"9007199254740993", "9007199254740994"}},
		{"joined-private", "/channels/456/users/@me/threads/archived/private", "id_descending", "9007199254740993", 12, []string{"9007199254740994", "9007199254740993"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			cursor := ""
			for pageNumber := range 2 {
				args := []string{"threads", "archived", "456", "--type", tc.kind, "--limit", "2"}
				if cursor != "" {
					args = append(args, "--before", cursor)
				}
				calls := 0
				code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						t.Errorf("archive discovery mutated state: %s", r.Method)
					}
					if r.URL.Path == "/users/@me" {
						io.WriteString(w, `{"id":"111","username":"owner"}`)
						return
					}
					calls++
					want := url.Values{"limit": {"2"}}
					if pageNumber == 1 {
						want.Set("before", tc.cursor)
					}
					if r.URL.Path != tc.path || !reflect.DeepEqual(r.URL.Query(), want) {
						t.Errorf("unexpected archive request: %s", r.URL)
					}
					if pageNumber == 1 {
						io.WriteString(w, `{"threads":[],"members":[],"has_more":false}`)
						return
					}
					io.WriteString(w, `{"threads":[`+
						threadFixture("9007199254740994", "456", tc.typeID, true, "2026-09-14T00:00:00Z")+","+
						threadFixture("9007199254740993", "456", tc.typeID, true, "2026-09-15T00:00:00Z")+`],"members":[],"has_more":true}`)
				}, nil)
				var page struct {
					Items      []struct{ ID string }
					Complete   bool
					HasMore    bool `json:"has_more"`
					Order      string
					NextBefore string `json:"next_before"`
				}
				if code != 0 || errs != "" || calls != 1 || json.Unmarshal([]byte(out), &page) != nil || !page.Complete || page.Order != tc.order {
					t.Fatalf("exit=%d output=%s errors=%s", code, out, errs)
				}
				if pageNumber == 0 {
					if len(page.Items) != 2 || page.Items[0].ID != tc.ids[0] || page.Items[1].ID != tc.ids[1] || !page.HasMore || page.NextBefore != tc.cursor {
						t.Fatal("incorrect archive continuation", out)
					}
				} else if len(page.Items) != 0 || page.HasMore || page.NextBefore != "" {
					t.Fatal("archive did not terminate", out)
				}
				cursor = page.NextBefore
			}
		})
	}
}

func TestThreadResponseValidationAndPermissions(t *testing.T) {
	active := threadFixture("9007199254740993", "456", 11, false, "2026-09-15T00:00:00Z")
	archived := threadFixture("9007199254740993", "456", 11, true, "2026-09-15T00:00:00Z")
	for _, tc := range []struct {
		name, body string
		archive    bool
		status     int
	}{
		{"missing active array", `{}`, false, 200},
		{"wrong active parent", `{"threads":[` + strings.Replace(active, `"parent_id":"456"`, `"parent_id":"789"`, 1) + `],"has_more":false}`, false, 200},
		{"container returned as thread", `{"threads":[` + strings.Replace(active, `"type":11`, `"type":15`, 1) + `],"has_more":false}`, false, 200},
		{"missing metadata", `{"threads":[` + strings.Replace(active, `"thread_metadata"`, `"unknown_metadata"`, 1) + `],"has_more":false}`, false, 200},
		{"missing archive state", `{"threads":[` + strings.Replace(active, `"archived":false,`, "", 1) + `],"has_more":false}`, false, 200},
		{"archived in active list", `{"threads":[` + archived + `],"has_more":false}`, false, 200},
		{"missing archive has_more", `{"threads":[]}`, true, 200},
		{"wrong parent", `{"threads":[` + strings.Replace(archived, `"parent_id":"456"`, `"parent_id":"789"`, 1) + `],"has_more":false}`, true, 200},
		{"invalid archive cursor", `{"threads":[` + strings.Replace(archived, "2026-09-15T00:00:00Z", "not-a-timestamp", 1) + `],"has_more":true}`, true, 200},
		{"private in public archive", `{"threads":[` + strings.Replace(archived, `"type":11`, `"type":12`, 1) + `],"has_more":false}`, true, 200},
		{"permission denied", `{}`, true, 403},
		{"empty outstanding archive", `{"threads":[],"has_more":true}`, true, 200},
		{"empty outstanding active page", `{"threads":[],"has_more":true}`, false, 200},
		{"short outstanding active page", `{"threads":[` + active + `],"has_more":true}`, false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"threads", "list", "456"}
			if tc.archive {
				args = []string{"threads", "archived", "456"}
			}
			calls := 0
			code, out, errs := invoke(t, args, "", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/users/@me" {
					io.WriteString(w, `{"id":"111","username":"owner"}`)
					return
				}
				calls++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}, nil)
			if tc.name == "short outstanding active page" {
				if code != 0 || !strings.Contains(out, `"complete":false`) || !strings.Contains(out, `"next_before":"9007199254740993"`) || strings.Contains(out, `"continuation_limited"`) {
					t.Fatalf("short page lost its continuation: %d %s %s", code, out, errs)
				}
				return
			}
			if strings.HasPrefix(tc.name, "empty outstanding") {
				if code != 0 || !strings.Contains(out, `"continuation_limited":true`) || !strings.Contains(out, `"complete":false`) || strings.Contains(out, `"next_before"`) {
					t.Fatalf("unusable continuation: %d %s %s", code, out, errs)
				}
				return
			}
			wantCode, wantError := 1, "invalid_upstream_data"
			if tc.status == 403 {
				wantCode, wantError = 3, "permission_denied"
			}
			if code != wantCode || out != "" || calls != 1 || !strings.Contains(errs, wantError) {
				t.Fatalf("exit=%d calls=%d output=%s errors=%s", code, calls, out, errs)
			}
		})
	}
}

func TestMessagesAroundContext(t *testing.T) {
	for _, tc := range []struct {
		name, anchor string
		limit        int
		want         []string
	}{
		{"odd window", "9007199254740993", 3, []string{"9007199254740994", "9007199254740993", "9007199254740992"}},
		{"even window", "9007199254740993", 2, []string{"9007199254740994", "9007199254740993"}},
		{"anchor at oldest edge", "9007199254740992", 2, []string{"9007199254740993", "9007199254740992"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs := invoke(t, []string{"messages", "list", "456", "--around", tc.anchor, "--limit", fmt.Sprint(tc.limit)}, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":11,"guild_id":"123"}`)
				case "/channels/456/messages":
					if r.Method != "GET" || r.URL.RawQuery != "around="+tc.anchor+"&limit="+fmt.Sprint(tc.limit) {
						t.Errorf("unexpected context request: %s %s", r.Method, r.URL)
					}
					io.WriteString(w, "["+sampleMessage+","+strings.Replace(sampleMessage, "9007199254740993", "9007199254740992", 1)+","+strings.Replace(sampleMessage, "9007199254740993", "9007199254740994", 1)+"]")
				default:
					t.Errorf("unexpected request: %s", r.URL)
				}
			}, nil)
			var page struct {
				Items      []struct{ ID string }
				Order      string
				Complete   bool
				HasMore    bool   `json:"has_more"`
				NextBefore string `json:"next_before"`
				NextAfter  string `json:"next_after"`
			}
			if code != 0 || errs != "" || json.Unmarshal([]byte(out), &page) != nil || !page.Complete || !page.HasMore || page.Order != "newest_first" || page.NextBefore != tc.want[len(tc.want)-1] || page.NextAfter != tc.want[0] {
				t.Fatalf("incorrect context page: %d %s %s", code, out, errs)
			}
			got := []string{}
			for _, m := range page.Items {
				got = append(got, m.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatal("wrong context window", out)
			}
		})
	}
}

func TestAroundResponseBounds(t *testing.T) {
	for _, tc := range []struct {
		limit, returned, exit int
	}{{100, 101, 0}, {3, 4, 1}, {2, 4, 1}} {
		t.Run(fmt.Sprintf("limit=%d/returned=%d", tc.limit, tc.returned), func(t *testing.T) {
			anchor := "9007199254740950"
			code, out, errs := invoke(t, []string{"messages", "list", "456", "--around", anchor, "--limit", fmt.Sprint(tc.limit)}, "", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/users/@me":
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				case "/channels/456":
					io.WriteString(w, `{"id":"456","type":0,"guild_id":"123"}`)
				case "/channels/456/messages":
					messages := []string{}
					for i := range tc.returned {
						messages = append(messages, strings.Replace(sampleMessage, "9007199254740993", fmt.Sprint(9007199254740900+i), 1))
					}
					io.WriteString(w, "["+strings.Join(messages, ",")+"]")
				default:
					t.Errorf("unexpected request: %s", r.URL)
				}
			}, nil)
			if code != tc.exit {
				t.Fatalf("exit=%d output=%s errors=%s", code, out, errs)
			}
			if tc.exit != 0 {
				if out != "" || !strings.Contains(errs, "invalid_upstream_data") {
					t.Fatal("oversized response was accepted", out, errs)
				}
				return
			}
			var page struct{ Items []struct{ ID string } }
			if errs != "" || json.Unmarshal([]byte(out), &page) != nil || len(page.Items) != tc.limit {
				t.Fatal("window did not honor --limit", out, errs)
			}
			for _, m := range page.Items {
				if m.ID == anchor {
					return
				}
			}
			t.Fatal("anchor was lost from context")
		})
	}
}

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
	calls := 0
	code, out, errs := invoke(t, []string{"threads", "list", "456", "--limit", "3", "--before", "9007199254740996"}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
		calls++
		want := url.Values{"archived": {"false"}, "sort_by": {"creation_time"}, "sort_order": {"desc"}, "limit": {"3"}, "max_id": {"9007199254740996"}}
		if r.Method != "GET" || r.URL.Path != "/channels/456/threads/search" || !reflect.DeepEqual(r.URL.Query(), want) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, `{"threads":[`+
			threadFixture("9007199254740993", "456", 11, false, "2026-09-15T00:00:00Z")+","+
			threadFixture("9007199254740995", "456", 12, false, "2026-09-15T00:00:00Z")+","+
			threadFixture("9007199254740994", "456", 10, false, "2026-09-15T00:00:00Z")+`],"members":[],"has_more":true,"total_results":4}`)
	}), nil)
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
	got := []string{}
	for _, item := range page.Items {
		got = append(got, item.ID)
		if item.ParentID != "456" || item.ThreadMetadata.Archived || item.Flags == nil || *item.Flags != 0 || item.MessageCount == nil || *item.MessageCount != 0 || !reflect.DeepEqual(item.AppliedTags, []string{"700"}) {
			t.Fatal("thread identity or metadata changed", out)
		}
	}
	if !reflect.DeepEqual(got, []string{"9007199254740995", "9007199254740994", "9007199254740993"}) || !page.HasMore || page.NextBefore != "9007199254740993" {
		t.Fatal("wrong threads or continuation", out)
	}
}

func TestArchivedThreadContinuation(t *testing.T) {
	for _, tc := range []struct {
		kind, path, before, order, next string
		typeID                          int
		ids                             []string
	}{
		{"public", "/channels/456/threads/archived/public", "2026-09-16T00:00:00Z", "archive_timestamp_descending", "2026-09-14T00:00:00Z", 11, []string{"9007199254740993", "9007199254740994"}},
		{"private", "/channels/456/threads/archived/private", "2026-09-16T00:00:00Z", "archive_timestamp_descending", "2026-09-14T00:00:00Z", 12, []string{"9007199254740993", "9007199254740994"}},
		{"joined-private", "/channels/456/users/@me/threads/archived/private", "9007199254740999", "id_descending", "9007199254740993", 12, []string{"9007199254740994", "9007199254740993"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			code, out, errs := invoke(t, []string{"threads", "archived", "456", "--type", tc.kind, "--limit", "2", "--before", tc.before}, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != tc.path || !reflect.DeepEqual(r.URL.Query(), url.Values{"limit": {"2"}, "before": {tc.before}}) {
					t.Errorf("unexpected archive request: %s %s", r.Method, r.URL)
				}
				io.WriteString(w, `{"threads":[`+
					threadFixture("9007199254740994", "456", tc.typeID, true, "2026-09-14T00:00:00Z")+","+
					threadFixture("9007199254740993", "456", tc.typeID, true, "2026-09-15T00:00:00Z")+`],"members":[],"has_more":true}`)
			}), nil)
			var page struct {
				Items      []struct{ ID string }
				Complete   bool
				HasMore    bool `json:"has_more"`
				Order      string
				NextBefore string `json:"next_before"`
			}
			if code != 0 || errs != "" || json.Unmarshal([]byte(out), &page) != nil || !page.Complete || page.Order != tc.order {
				t.Fatalf("exit=%d output=%s errors=%s", code, out, errs)
			}
			if len(page.Items) != 2 || page.Items[0].ID != tc.ids[0] || page.Items[1].ID != tc.ids[1] || !page.HasMore || page.NextBefore != tc.next {
				t.Fatal("incorrect archive continuation", out)
			}
		})
	}
}

func TestThreadResponseValidation(t *testing.T) {
	active := threadFixture("9007199254740993", "456", 11, false, "2026-09-15T00:00:00Z")
	archived := threadFixture("9007199254740993", "456", 11, true, "2026-09-15T00:00:00Z")
	for _, tc := range []struct {
		name, body string
		archive    bool
	}{
		{"wrong active parent", `{"threads":[` + strings.Replace(active, `"parent_id":"456"`, `"parent_id":"789"`, 1) + `],"has_more":false}`, false},
		{"private in public archive", `{"threads":[` + strings.Replace(archived, `"type":11`, `"type":12`, 1) + `],"has_more":false}`, true},
		{"empty outstanding archive", `{"threads":[],"has_more":true}`, true},
		{"empty outstanding active page", `{"threads":[],"has_more":true}`, false},
		{"short outstanding active page", `{"threads":[` + active + `],"has_more":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"threads", "list", "456"}
			if tc.archive {
				args = []string{"threads", "archived", "456"}
			}
			code, out, errs := invoke(t, args, "", upstream(dm, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}), nil)
			switch {
			case tc.name == "short outstanding active page":
				if code != 0 || !strings.Contains(out, `"complete":false`) || !strings.Contains(out, `"next_before":"9007199254740993"`) || strings.Contains(out, `"continuation_limited"`) {
					t.Fatalf("short page lost its continuation: %d %s %s", code, out, errs)
				}
			case strings.HasPrefix(tc.name, "empty outstanding"):
				if code != 0 || !strings.Contains(out, `"continuation_limited":true`) || !strings.Contains(out, `"complete":false`) || strings.Contains(out, `"next_before"`) {
					t.Fatalf("unusable continuation: %d %s %s", code, out, errs)
				}
			case code != 1 || out != "" || !strings.Contains(errs, "invalid_upstream_data"):
				t.Fatalf("exit=%d output=%s errors=%s", code, out, errs)
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
		// Discord may return one extra message for even limits; the anchor must survive trimming.
		{"even window", "9007199254740993", 2, []string{"9007199254740994", "9007199254740993"}},
		{"anchor at oldest edge", "9007199254740992", 2, []string{"9007199254740993", "9007199254740992"}},
		{"oversized response", "9007199254740993", 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs := invoke(t, []string{"messages", "list", "456", "--around", tc.anchor, "--limit", fmt.Sprint(tc.limit)}, "", upstream(`{"id":"456","type":11,"guild_id":"123"}`, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.RawQuery != "around="+tc.anchor+"&limit="+fmt.Sprint(tc.limit) {
					t.Errorf("unexpected context request: %s %s", r.Method, r.URL)
				}
				io.WriteString(w, "["+sampleMessage+","+strings.Replace(sampleMessage, "9007199254740993", "9007199254740992", 1)+","+strings.Replace(sampleMessage, "9007199254740993", "9007199254740994", 1)+"]")
			}), nil)
			if tc.want == nil {
				if code != 1 || out != "" || !strings.Contains(errs, "invalid_upstream_data") {
					t.Fatalf("oversized response was accepted: %d %s %s", code, out, errs)
				}
				return
			}
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

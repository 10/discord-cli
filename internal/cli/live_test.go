package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Explicit opt-in only. This test never sends messages or changes account state.
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv("DISCORD_CLI_LIVE") != "1" {
		t.Skip("set DISCORD_CLI_LIVE=1 to opt into desktop identity and a bounded server read")
	}
	if _, set := os.LookupEnv("DISCORD_TOKEN"); set {
		t.Fatal("unset DISCORD_TOKEN to test automatic desktop authentication")
	}
	for _, args := range [][]string{{"auth", "status"}, {"servers", "list", "--limit", "1"}} {
		var out, errs bytes.Buffer
		code := Run(context.Background(), args, strings.NewReader(""), &out, &errs, Options{})
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", strings.Join(args, " "), code, errs.String())
		}
		if args[0] == "auth" {
			var result struct {
				Account struct{ ID string }
				Source  string
			}
			if json.Unmarshal(out.Bytes(), &result) != nil || !strings.HasPrefix(result.Source, "desktop:") {
				t.Fatal("identity was not resolved from desktop storage")
			}
			if expected := os.Getenv("DISCORD_CLI_EXPECT_ACCOUNT_ID"); expected != "" && result.Account.ID != expected {
				t.Fatal("resolved account differs from expected desktop account")
			}
			t.Logf("desktop account %s via %s", result.Account.ID, result.Source)
		} else {
			var result struct{ Items []json.RawMessage }
			if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Items) > 1 {
				t.Fatal("bounded read failed")
			}
			t.Logf("bounded server read succeeded (%d items)", len(result.Items))
		}
	}
}

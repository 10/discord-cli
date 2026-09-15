package auth

import (
	"context"
	"errors"
	"github.com/10/discord-cli/internal/api"
	"testing"
	"time"
)

func TestNativeKeychainMissingItem(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := keychainKey(ctx, "discord-cli-nonexistent-test-item-69249a2e")
	var failure *api.Error
	if !errors.As(err, &failure) || failure.Code != "desktop_locked" {
		t.Fatal("native missing credential must return a redacted actionable error")
	}
}

package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha1"
	"os/exec"
	"path/filepath"
	"strings"
)

func nativeKey(ctx context.Context, root string) ([]byte, error) {
	return keychainKey(ctx, filepath.Base(root))
}
func keychainKey(ctx context.Context, application string) ([]byte, error) {
	password, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", application+" Safe Storage", "-a", application+" Key", "-w").Output()
	if err != nil {
		return nil, desktopError("desktop_locked", "Allow Discord Safe Storage access in macOS Keychain, unlock your keychain, or supply an override")
	}
	defer clear(password)
	key, err := pbkdf2.Key(sha1.New, strings.TrimSpace(string(password)), []byte("saltysalt"), 1003, 16)
	if err != nil {
		return nil, desktopError("desktop_locked", "Could not use Discord Safe Storage")
	}
	return key, nil
}

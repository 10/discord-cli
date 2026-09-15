//go:build !darwin && !windows

package auth

import "context"

func nativeKey(context.Context, string) ([]byte, error) {
	return nil, desktopError("desktop_unsupported", "Native desktop credentials require macOS or Windows; supply an override")
}

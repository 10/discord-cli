package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func unprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, desktopError("desktop_unsupported", "The Windows encrypted credential is empty")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var output windows.DataBlob
	if windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output) != nil {
		return nil, desktopError("desktop_locked", "Windows DPAPI could not unlock Discord credentials for this OS user; sign in again or supply an override")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
func nativeKey(ctx context.Context, root string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, desktopError("cancelled", "Command deadline or cancellation reached")
	}
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, desktopError("desktop_unavailable", "Cannot read Discord Local State; check permissions or supply an override")
	}
	var state struct {
		OSCrypt struct {
			Key string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if len(data) > 4<<20 || json.Unmarshal(data, &state) != nil {
		return nil, desktopError("desktop_unsupported", "Unsupported Discord Local State; supply an override")
	}
	encrypted, err := base64.StdEncoding.DecodeString(state.OSCrypt.Key)
	if err != nil || len(encrypted) <= 5 || string(encrypted[:5]) != "DPAPI" {
		return nil, desktopError("desktop_unsupported", "Unsupported Windows Discord key protection; supply an override")
	}
	return unprotect(encrypted[5:])
}

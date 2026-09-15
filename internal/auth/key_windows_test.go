package auth

import (
	"bytes"
	"golang.org/x/sys/windows"
	"testing"
	"unsafe"
)

func TestNativeDPAPIRoundTrip(t *testing.T) {
	plain := []byte("discord-cli synthetic DPAPI fixture")
	input := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var encrypted windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &encrypted); err != nil {
		t.Fatal(err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(encrypted.Data)))
	decoded, err := unprotect(unsafe.Slice(encrypted.Data, encrypted.Size))
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("native DPAPI round trip failed")
	}
	if _, err := unprotect([]byte("invalid synthetic data")); err == nil {
		t.Fatal("malformed DPAPI ciphertext accepted")
	}
}

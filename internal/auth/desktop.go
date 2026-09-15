package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/10/discord-cli/internal/api"
	"github.com/10/discord-cli/internal/desktopdb"
	ldb "github.com/golang/leveldb/db"
)

type KeyFunc func(context.Context, string) ([]byte, error)

func desktopError(code, message string) error { return &api.Error{Code: code, Message: message} }
func Desktop(ctx context.Context, paths []string, tempDir string, key KeyFunc, platform string) ([]Candidate, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	if key == nil {
		key = nativeKey
	}
	if paths == nil {
		var base string
		switch platform {
		case "darwin":
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, desktopError("desktop_unavailable", "Could not locate the current user's Discord storage")
			}
			base = filepath.Join(home, "Library", "Application Support")
		case "windows":
			base = os.Getenv("APPDATA")
			if base == "" {
				return nil, desktopError("desktop_missing", "APPDATA is unavailable; sign in to Discord or supply an override")
			}
		default:
			return nil, desktopError("desktop_unsupported", "Automatic desktop access supports macOS and Windows; supply DISCORD_TOKEN on this OS")
		}
		for _, name := range []string{"discord", "discordptb", "discordcanary"} {
			paths = append(paths, filepath.Join(base, name))
		}
	}
	var result []Candidate
	for _, root := range paths {
		if ctx.Err() != nil {
			return nil, desktopError("cancelled", "Command deadline or cancellation reached")
		}
		value, err := currentToken(ctx, filepath.Join(root, "Local Storage", "leveldb"), tempDir)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ldb.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var token string
		if err := json.Unmarshal(value, &token); err != nil {
			return nil, desktopError("desktop_unsupported", "Discord's current session record is unsupported; supply an override")
		}
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "dQw4w9WgXcQ:") {
			secret, err := key(ctx, root)
			if err != nil {
				return nil, err
			}
			token, err = decrypt(token, secret, platform)
			clear(secret)
			if err != nil {
				return nil, err
			}
		}
		if Validate(token) != nil {
			return nil, desktopError("desktop_unsupported", "Discord's current session format is unsupported; supply an override")
		}
		result = append(result, Candidate{token, "desktop:" + filepath.Base(root)})
	}
	if len(result) == 0 {
		return nil, desktopError("desktop_missing", "No current desktop session was found; sign in to Discord Stable, PTB, or Canary, or supply an override")
	}
	return result, nil
}

func currentToken(ctx context.Context, source, tempDir string) ([]byte, error) {
	entries, err := os.ReadDir(source)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, desktopError("desktop_unavailable", "Cannot read Discord storage; check permissions or supply an override")
	}
	dest, err := os.MkdirTemp(tempDir, "discord-session-*")
	if err != nil {
		return nil, desktopError("desktop_unavailable", "Cannot create a private temporary session copy")
	}
	defer os.RemoveAll(dest)
	var total int64
	before := map[string]os.FileInfo{}
	for _, entry := range entries {
		name := entry.Name()
		if name == "LOCK" || name == "LOG" || name == "LOG.old" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, desktopError("desktop_unsupported", "Unsupported Discord storage entry; supply an override")
		}
		before[name] = info
		total += info.Size()
		if total > 256<<20 {
			return nil, desktopError("desktop_unavailable", "Discord storage exceeds the bounded copy size; supply an override")
		}
		if err := copyFile(ctx, filepath.Join(source, name), filepath.Join(dest, name), info.Size()); err != nil {
			return nil, err
		}
	}
	// Detect a changed file set or journal during the copy; never repair the live store.
	after, err := os.ReadDir(source)
	if err != nil || len(after) != len(entries) {
		return nil, storageBusy()
	}
	for _, entry := range after {
		if entry.Name() == "LOCK" || entry.Name() == "LOG" || entry.Name() == "LOG.old" {
			continue
		}
		a, e := entry.Info()
		b, f := os.Stat(filepath.Join(dest, entry.Name()))
		if e != nil || f != nil || a.Size() != b.Size() || before[entry.Name()] == nil || !a.ModTime().Equal(before[entry.Name()].ModTime()) {
			return nil, storageBusy()
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "CURRENT")); err != nil {
		return nil, storageBusy()
	}
	// Only the private copy is opened; this reader replays valid Chromium journals
	// whose sequence starts before the manifest watermark. It never opens the live DB.
	db, err := desktopdb.Open(dest)
	if err != nil {
		return nil, storageBusy()
	}
	defer db.Close()
	raw, err := db.Get([]byte("_https://discord.com\x00\x01token"), nil)
	if errors.Is(err, ldb.ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, storageBusy()
	}
	if len(raw) < 2 || len(raw) > 16384 {
		return nil, desktopError("desktop_unsupported", "Unsupported Discord session record encoding; supply an override")
	}
	switch raw[0] {
	case 1:
		return raw[1:], nil
	case 0:
		if (len(raw)-1)%2 != 0 {
			break
		}
		units := make([]uint16, (len(raw)-1)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(raw[1+i*2:])
		}
		return []byte(string(utf16.Decode(units))), nil
	}
	return nil, desktopError("desktop_unsupported", "Unsupported Discord session record encoding; supply an override")
}
func storageBusy() error {
	return desktopError("desktop_unavailable", "Could not read a consistent Discord session copy; retry after closing Discord or supply an override")
}
func copyFile(ctx context.Context, source, dest string, size int64) error {
	in, err := os.Open(source)
	if err != nil {
		return storageBusy()
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return storageBusy()
	}
	defer out.Close()
	buf := make([]byte, 64<<10)
	var count int64
	for {
		if ctx.Err() != nil {
			return desktopError("cancelled", "Command deadline or cancellation reached")
		}
		n, e := in.Read(buf)
		if n > 0 {
			count += int64(n)
			if count > size {
				return storageBusy()
			}
			if _, err := out.Write(buf[:n]); err != nil {
				return storageBusy()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return storageBusy()
		}
	}
	if count != size {
		return storageBusy()
	}
	return nil
}
func decrypt(value string, key []byte, platform string) (string, error) {
	bad := func() (string, error) {
		return "", desktopError("desktop_unsupported", "Cannot decrypt this Discord session format; sign in again or supply an override")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "dQw4w9WgXcQ:"))
	if err != nil || len(data) < 3 || string(data[:3]) != "v10" {
		return bad()
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return bad()
	}
	var plain []byte
	switch platform {
	case "darwin":
		data = data[3:]
		if len(data) == 0 || len(data)%aes.BlockSize != 0 {
			return bad()
		}
		plain = make([]byte, len(data))
		cipher.NewCBCDecrypter(block, []byte("                ")).CryptBlocks(plain, data)
		padding := int(plain[len(plain)-1])
		if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
			return bad()
		}
		for _, b := range plain[len(plain)-padding:] {
			if int(b) != padding {
				return bad()
			}
		}
		plain = plain[:len(plain)-padding]
	case "windows":
		gcm, e := cipher.NewGCM(block)
		if e != nil || len(data) < 3+gcm.NonceSize()+gcm.Overhead() {
			return bad()
		}
		plain, e = gcm.Open(nil, data[3:3+gcm.NonceSize()], data[3+gcm.NonceSize():], nil)
		if e != nil {
			return bad()
		}
	default:
		return bad()
	}
	defer clear(plain)
	return string(plain), nil
}

package cli

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/10/discord-cli/internal/desktopdb"
	ldb "github.com/golang/leveldb/db"
	"github.com/golang/leveldb/record"
)

func sessionFixture(t *testing.T, token string) string {
	t.Helper()
	root := t.TempDir()
	db, e := desktopdb.Open(filepath.Join(root, "Local Storage", "leveldb"))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(token)
	if e := db.Set([]byte("_https://discord.com\x00\x01token"), append([]byte{1}, raw...), &ldb.WriteOptions{Sync: true}); e != nil {
		t.Fatal(e)
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	return root
}
func TestDesktopCurrentRecordAndSwitching(t *testing.T) {
	root := t.TempDir()
	db, err := desktopdb.Open(filepath.Join(root, "Local Storage", "leveldb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := []byte("_https://discord.com\x00\x01token")
	for _, token := range []string{"first.token.signature", "second.token.signature"} {
		if err := db.Set(key, append([]byte{1}, []byte(`"`+token+`"`)...), &ldb.WriteOptions{Sync: true}); err != nil {
			t.Fatal(err)
		}
		code, out, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != token {
				t.Fatal("stale token selected")
			}
			io.WriteString(w, `{"id":"1084003247154548807","username":"owner"}`)
		}, func(o *Options) {
			o.LookupEnv = func(string) (string, bool) { return "", false }
			o.DesktopPaths = []string{root}
			t.Cleanup(func() {
				entries, _ := os.ReadDir(o.TempDir)
				if len(entries) != 0 {
					t.Error("temporary session copy not cleaned")
				}
				if _, e := os.Stat(o.ConfigPath); !os.IsNotExist(e) {
					t.Error("detected token persisted")
				}
			})
		})
		if code != 0 || !strings.Contains(out, "desktop:") || strings.Contains(out+errs, token) {
			t.Fatalf("%d %s %s", code, out, errs)
		}
	}
	if err := db.Delete(key, &ldb.WriteOptions{Sync: true}); err != nil {
		t.Fatal(err)
	}
	code, _, _ := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) { t.Fatal("used deleted token") }, func(o *Options) {
		o.LookupEnv = func(string) (string, bool) { return "", false }
		o.DesktopPaths = []string{root}
	})
	if code != 3 {
		t.Fatal(code)
	}
}

func TestDesktopAmbiguity(t *testing.T) {
	first := sessionFixture(t, "first.token.signature")
	second := sessionFixture(t, "second.token.signature")
	code, out, errs := invoke(t, []string{"messages", "send", "456", "--content", "must not send"}, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/@me" {
			t.Fatal("mutation before unambiguous identity")
		}
		id := "111"
		if r.Header.Get("Authorization") == "second.token.signature" {
			id = "222"
		}
		io.WriteString(w, `{"id":"`+id+`","username":"owner"}`)
	}, func(o *Options) {
		o.LookupEnv = func(string) (string, bool) { return "", false }
		o.DesktopPaths = []string{first, second}
	})
	if code != 3 || out != "" || !strings.Contains(errs, "ambiguous_account") || !strings.Contains(errs, `"222"`) || strings.Contains(errs, "signature") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}
func TestEncryptedDesktopFormats(t *testing.T) {
	key := []byte("0123456789abcdef")
	token := "encrypted.token.signature"
	for _, platform := range []string{"darwin", "windows"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(platform+map[bool]string{false: " valid", true: " corrupt"}[corrupt], func(t *testing.T) {
				block, _ := aes.NewCipher(key)
				data := []byte("v10")
				if platform == "darwin" {
					plain := []byte(token)
					padding := aes.BlockSize - len(plain)%aes.BlockSize
					plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
					encrypted := make([]byte, len(plain))
					cipher.NewCBCEncrypter(block, []byte("                ")).CryptBlocks(encrypted, plain)
					data = append(data, encrypted...)
				} else {
					gcm, _ := cipher.NewGCM(block)
					nonce := make([]byte, gcm.NonceSize())
					data = append(data, nonce...)
					data = gcm.Seal(data, nonce, []byte(token), nil)
				}
				if corrupt {
					data = data[:len(data)-1]
				}
				root := sessionFixture(t, "dQw4w9WgXcQ:"+base64.StdEncoding.EncodeToString(data))
				var sessionTemp string
				code, out, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) {
					if corrupt {
						t.Fatal("malformed credential reached network")
					}
					if r.Header.Get("Authorization") != token {
						t.Fatal("wrong decrypted token")
					}
					io.WriteString(w, `{"id":"111","username":"owner"}`)
				}, func(o *Options) {
					sessionTemp = o.TempDir
					o.LookupEnv = func(string) (string, bool) { return "", false }
					o.DesktopPaths = []string{root}
					o.DesktopPlatform = platform
					o.DesktopKey = func(context.Context, string) ([]byte, error) { return bytes.Clone(key), nil }
				})
				if (corrupt && (code != 3 || !strings.Contains(errs, "desktop_unsupported"))) || (!corrupt && code != 0) || strings.Contains(out+errs, token) {
					t.Fatalf("%d %s %s", code, out, errs)
				}
				if entries, err := os.ReadDir(sessionTemp); err != nil || len(entries) != 0 {
					t.Fatal("temporary session copy not cleaned")
				}
			})
		}
	}
}
func TestChromiumJournalPrecedingManifestWatermark(t *testing.T) {
	root := sessionFixture(t, "current.token.signature")
	path := filepath.Join(root, "Local Storage", "leveldb")
	current, e := os.ReadFile(filepath.Join(path, "CURRENT"))
	if e != nil {
		t.Fatal(e)
	}
	manifest := filepath.Join(path, strings.TrimSpace(string(current)))
	original, e := os.ReadFile(manifest)
	if e != nil {
		t.Fatal(e)
	}
	// Chromium can persist a manifest watermark covering records still in its journal.
	var rewritten bytes.Buffer
	reader := record.NewReader(bytes.NewReader(original))
	writer := record.NewWriter(&rewritten)
	for {
		r, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		w, e := writer.Next()
		if e != nil {
			t.Fatal(e)
		}
		if _, e := io.Copy(w, r); e != nil {
			t.Fatal(e)
		}
	}
	w, e := writer.Next()
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte{4, 2})
	writer.Close() // VersionEdit tag 4 = last sequence, value 2.
	if e := os.WriteFile(manifest, rewritten.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	code, out, errs := invoke(t, []string{"auth", "status"}, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "current.token.signature" {
			t.Fatal("did not replay current journal")
		}
		io.WriteString(w, `{"id":"111","username":"owner"}`)
	}, func(o *Options) {
		o.LookupEnv = func(string) (string, bool) { return "", false }
		o.DesktopPaths = []string{root}
	})
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

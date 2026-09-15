# Contributor map

| Location | Responsibility |
| --- | --- |
| `cmd/discord/main.go` | Standard streams, cancellation, process exit |
| `internal/cli/cli.go` | Kong root, invocation composition, JSON/error output |
| `internal/cli/{auth,discovery,messages,reactions,attachments}.go` | Command arguments, validation, execution |
| `internal/api` | Discord HTTP, rate limits, response validation, uploads/downloads |
| `internal/auth` | Override precedence, current desktop records, native key access |
| `internal/desktopdb` | LevelDB compatibility and handle cleanup for disposable session copies and synthetic fixtures |
| `internal/config` | Saved override location, atomic writing, OS permissions |
| `internal/cli/runner_test.go` | Command-boundary behavior using a local HTTP server |
| `internal/cli/desktop_test.go` | Synthetic desktop records through the same boundary |
| `internal/auth/key_*_test.go` | Native OS operations on synthetic/missing credentials |
| `internal/cli/live_test.go` | Explicitly opted-in, read-only native acceptance |

The CLI composes concrete API/auth/config modules. They never import the CLI or
parse arguments. Test substitutions live in `cli.Options`; production endpoints
have no environment or flag override. No gateway, cache, or daemon is started.

## One flow to trace

For `messages send CHANNEL --stdin`, start at `MessageSendCmd` in
`internal/cli/messages.go`. Kong validates arguments; `Prepare` reads and validates
text and opens explicit files. `Run` then resolves candidates in `auth.Candidates`,
validates each identity with `api.Client.Me`, and rejects ambiguity. The command
calls `api.Client.WriteMessage`; the shared client performs bounded HTTP with
Arikawa's retry count set to one. JSON output returns through `Context.Print`.
`TestMessageWritesAndAttachmentPreservation` exercises this flow with captured
streams and observed HTTP requests. Refactoring internals should preserve it.

## Compatibility details

Arikawa's retry count of one means one attempt; zero means unlimited attempts.
The CLI owns explicit rate-limit and indexing retries within the invocation
deadline so transport failures cannot silently duplicate a message send.

Desktop authentication reads the current token from a disposable LevelDB copy.
The reader must replay journal records preceding the manifest sequence watermark;
`TestChromiumJournalPrecedingManifestWatermark` covers an observed Chromium case.
`internal/desktopdb` handles Windows sync permissions and manifest-handle cleanup
on those copies and synthetic fixtures.

## Checks

```sh
go build ./...
go test ./internal/cli -run TestMessageWritesAndAttachmentPreservation
go test ./internal/cli -run TestChromiumJournalPrecedingManifestWatermark
go vet ./...
go test ./...
```

Ordinary tests are offline and need no account or installed Discord. Native tests
use generated DPAPI data on Windows and a nonexistent Keychain item on macOS.
Run checks natively on each supported OS; cross-compilation does not verify
credential access. See the [live test instructions](../README.md#development)
for the optional desktop-authentication and bounded-read check. Live tests require
`DISCORD_CLI_LIVE=1` and are excluded by default.

## Walkthrough check

An unfamiliar contributor should locate the `messages send` definition, trace the
flow above, locate its behavioral test, and run that test plus `go build ./...`.
Report any navigation ambiguity as part of review. Product behavior belongs in
[README.md](../README.md); vocabulary belongs in [CONTEXT.md](../CONTEXT.md).

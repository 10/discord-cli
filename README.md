<div align="center">

# discord

*Read, search, and send Discord messages from your terminal.*

[![Go](https://img.shields.io/badge/Go-1.26.3+-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Windows-5865F2?style=flat)](#authentication)
[![Output](https://img.shields.io/badge/Output-JSON-3665F3?style=flat)](#output-and-recovery)

</div>

---

```console
$ discord auth status | jq '.source'
"desktop:discord"

$ discord messages search --channel CHANNEL_ID --query "release" |
    jq '.items[] | {author: .author.username, content}'
{
  "author": "alex",
  "content": "The release is ready."
}
```

Work with your existing DMs, group DMs, and server conversations using your
Discord desktop session. One Go binary, JSON output, and explicit IDs for every
message action. Examples use placeholder IDs and illustrative output.

## Features

- **Find conversations** — list servers, channels, DMs, and friends, or open a one-to-one DM by user ID.
- **Read and search** — page through history, retrieve an exact message, or filter one server or conversation by text, authors, mentions, content type, author type, pins, and dates.
- **Send and edit** — write text, reply to a message, attach files, and edit or delete messages where permitted.
- **Reactions** — inspect counts and participants, add emoji, and remove your own or another user's reaction where permitted.
- **Attachments** — upload explicit files and download selected attachments without overwriting existing files.
- **Desktop authentication** — use the current session from Discord Stable, PTB, or Canary on macOS and Windows, with optional token overrides.

## Install

```bash
go install github.com/10/discord-cli/cmd/discord@latest
```

Requires Go `1.26.3` or later, with `~/go/bin` (Windows: `%USERPROFILE%\go\bin`)
or `GOBIN` on `PATH`.

## CLI

Sign in to the Discord desktop app, then check the account and discover IDs:

```bash
discord auth status
discord servers list --limit 10
discord channels list SERVER_ID
discord dms list
discord friends list
discord messages list CHANNEL_ID --limit 50
discord messages get CHANNEL_ID MESSAGE_ID
discord messages search --channel CHANNEL_ID --query "release" --limit 25
discord messages search --server SERVER_ID --author USER_ID --query "release"
```

Replace `SERVER_ID`, `CHANNEL_ID`, and other placeholders with IDs returned by
these commands. Use any command's `--help` for arguments, defaults, and filters.
Known accessible threads use the same channel-ID commands; categories and forum
containers cannot be message destinations. Group management is unavailable.

Successful commands print one JSON value on stdout. Lists and search results
put rows in `items`; IDs stay strings. Errors go to stderr, so results work
directly with `jq`:

```bash
set -o pipefail
discord messages list CHANNEL_ID --limit 50 |
  jq '.items[] | {id, author: .author.username, content}'
```

## Search filters

```bash
discord messages search --server SERVER_ID \
  --author USER_A --author USER_B --mentions USER_C \
  --has forward --on 2026-09-14 \
  --sort oldest
```

This searches that day for forwarded messages from either author mentioning
USER_C, oldest first. Repeat `--author`, `--mentions`, `--has`, or
`--author-type` to select multiple values. Use repeatable `--in-channel` with
`--server` to restrict the selected channels. IDs remain explicit.

`--has` accepts image, video, link, file, embed, sound, poll, sticker, and
forward (also spelled snapshot). `--author-type` accepts user, bot, and webhook.
Use `--has=-image` or `--author-type=-bot` to exclude a type. `--pinned` or
`--pinned=true` selects pinned messages; **`--pinned=false` is rejected** because
live Discord searches returned pinned messages for that filter. Omit it to
search either pin state.

`--on` selects one calendar day. `--before-date` and `--after-date` exclude the
named day and may be combined for a range. Dates use `YYYY-MM-DD` in the
machine’s local timezone. Day boundaries follow the calendar, including
23- and 25-hour daylight-saving days. These are
explicit CLI date semantics; exact Discord-app date-picker parity is unverified.
Keep date filters separate from existing `--before`/`--after` message-ID bounds,
and use `--on` separately from other bounds.

`--sort` accepts newest (default), oldest, or relevance. Relevance preserves
Discord's ranking. The advanced API controls for roles, replies, embed/link/
attachment details, and everyone mentions are not exposed by this command.

## Authentication

This CLI uses a Discord user account. [Discord prohibits automating normal user
accounts](https://support.discord.com/hc/en-us/articles/115002192352-Automated-User-Accounts-Self-Bots)
and warns that doing so can result in account termination.

Every invocation resolves the account afresh. Credentials resolve from
**`DISCORD_TOKEN` → saved override → desktop session**. An explicitly set but
empty, malformed, or expired override fails without fallback.

To save an override, enter a token at the hidden prompt or supply it on stdin:

```bash
discord auth set-token
discord auth status
discord auth clear-token
```

`auth set-token` validates the identity before saving. It has no token flag or
positional argument. `auth clear-token` removes only the saved override;
`DISCORD_TOKEN` continues to take precedence if set.

The saved file is `discord-cli/config.json` under the OS per-user configuration
directory: `~/Library/Application Support` on macOS or `%AppData%` on Windows.
CLI-created saved files are restricted to the OS user. Automatically detected
tokens are never saved, and tokens are redacted from output.

macOS may show a Keychain prompt; Windows uses the current OS user's DPAPI.
Different accounts signed in across client installations produce
`ambiguous_account`, with identity and source details to help you select an override.

Automatic authentication and a bounded read passed with Discord Stable on
macOS 26.6.2 (client 0.0.411) and Windows 11 Pro 25H2, x64 (client 1.0.9257).
**The Windows acceptance check required Discord to be closed.** Other OS/client
versions and storage formats have not been verified natively.

## Messages and files

Message results preserve received embeds, component trees, forwarded snapshots,
polls, stickers, mentions, pin state, message type/flags, webhook identity, and
thread metadata. Attachments also include available descriptions, titles, and
audio/video metadata. These fields accompany the original `content`; nested
content is not flattened into text or fetched separately. Missing nested fields
remain absent, while explicit `null` and empty arrays are preserved. This applies
to history, exact reads, search hits, and send/edit results. Reading these fields
does not add commands for creating polls, sending stickers, or interacting with
components.

These commands execute immediately, without a confirmation prompt:

```bash
discord dms open USER_ID
discord messages send CHANNEL_ID --content "The release is ready."
printf 'First line\nSecond line\n' | discord messages send CHANNEL_ID --stdin
discord messages send CHANNEL_ID --file ./report.pdf --reply-to MESSAGE_ID
discord messages edit CHANNEL_ID MESSAGE_ID --content "Corrected text"
discord messages edit CHANNEL_ID MESSAGE_ID --file ./new.pdf --remove-attachment ATTACHMENT_ID
discord messages delete CHANNEL_ID MESSAGE_ID
```

Use `--content` or `--stdin` for text, and repeat `--file` to attach multiple
files. Text is limited to 2,000 UTF-16 units; messages are never split
automatically. Edits retain existing attachments unless you explicitly remove
them with `--remove-attachment`.

Inspect or change reactions with Unicode emoji or custom `name:EMOJI_ID` values:

```bash
discord reactions list CHANNEL_ID MESSAGE_ID
discord reactions users CHANNEL_ID MESSAGE_ID '👍'
discord reactions add CHANNEL_ID MESSAGE_ID '👍'
discord reactions remove CHANNEL_ID MESSAGE_ID '👍'
discord reactions remove CHANNEL_ID MESSAGE_ID 'party:EMOJI_ID' --user USER_ID
```

Get attachment IDs from message metadata, then download a selected file:

```bash
mkdir -p ./downloads
discord attachments download CHANNEL_ID MESSAGE_ID ATTACHMENT_ID --dir ./downloads
```

Files stream during transfer. Downloads require an existing directory, preserve
existing files, and use HTTPS Discord CDN URLs without forwarding credentials.
Use `--name report.pdf` to choose a simple filename. Completed downloads are
published with an atomic hard link, so the destination filesystem must support
hard links. For a large transfer, increase the total deadline with `--timeout 5m`.

## Pagination

| Command | Default limit | Continue with |
| --- | --- | --- |
| `servers list` | 100 (max 200) | `--after NEXT_AFTER` |
| `messages list` | 50 (max 100) | `--before NEXT_BEFORE` for older messages, or `--after NEXT_AFTER` for newer |
| `messages search` | 25 (max 25) | Same filters and `--offset NEXT_OFFSET` |
| `reactions users` | 50 (max 100) | `--after NEXT_AFTER` |

History returns messages newest first; search defaults to newest first and
supports `--sort oldest` or `--sort relevance`. Servers and reaction
participants use ascending IDs. Other discovery lists return one bounded API
response, sorted by ID.

**Check `has_more` and `complete` even on exit 0.** `has_more` is conservative
when a page fills its limit. `complete` describes the page or search-index
state, not exhaustive history. Nonempty history pages include both
`next_before` and `next_after`; choose one direction per request.

Search requires exactly one of `--server` or `--channel` and never fans out to
other scopes. Offsets range from 0 through 9975. When the next offset would
exceed that limit, or an empty page reports more results, `continuation_limited:
true` and `complete: false` mean you should narrow the filters or retry later;
no unusable continuation is returned. `indexing_delay` is an error, not an empty
result.

Search marks historical indexing and short pages with outstanding results as
`complete: false`. Nonempty pages retain count-based `next_offset` advancement.
Discord's search pages and totals can change, so continuation does not guarantee
an exhaustive snapshot; deduplicate by message ID when combining pages.

## Output and recovery

Errors use `{"error":{"code":"...","message":"..."}}` on stderr, with
`status`, `retry_after` (seconds), and `details` when useful. Help and version
output are text.

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Other runtime failure, including indexing delay or uncertain write |
| 2 | Invalid arguments or Discord rejected the request |
| 3 | Authentication, account selection, challenge, or permission failure |
| 4 | Missing resource or unavailable attachment |
| 5 | Rate-limit exhaustion |

Stable runtime codes include `authentication_failed`, `ambiguous_account`,
`permission_denied`, `user_action_required`, `desktop_missing`, `desktop_locked`,
`desktop_unavailable`, `desktop_unsupported`, `config_error`, `not_found`,
`attachment_unavailable`, `rate_limited`, `indexing_delay`, `network_failure`,
`invalid_upstream_data`, `uncertain_write`, `cancelled`, and `file_error`.

The default total deadline is 60 seconds, including authentication and transfers.
Explicit rate limits and indexing delays get at most five attempts within that
deadline. For `uncertain_write`, inspect the destination before deciding whether
to resend. No transport error or server failure automatically repeats a message
creation.

## Agents

[`SKILL.md`](skills/discord-cli/SKILL.md) guides agents through account and
destination selection, bounded reads and searches, requested writes,
attachments, and error recovery. Copy the self-contained `skills/discord-cli`
folder into your agent's skills directory.

For Codex, from this checkout:

```bash
mkdir -p ~/.codex/skills
cp -R skills/discord-cli ~/.codex/skills/
```

On Windows (PowerShell):

```powershell
New-Item -ItemType Directory -Force "$HOME\.codex\skills" | Out-Null
Copy-Item -Recurse .\skills\discord-cli "$HOME\.codex\skills\"
```

For Claude Code, use `~/.claude/skills` instead. This installs the instructions;
[install the CLI](#install) separately.

## Development

```bash
go build -o bin/discord ./cmd/discord
./bin/discord --help
go test -race ./...
go vet ./...
```

Ordinary tests are offline and need no account or installed Discord. To opt into
desktop authentication and a bounded, read-only server check, use a signed-in
desktop session with no environment or saved token override:

```bash
DISCORD_CLI_LIVE=1 go test ./internal/cli -run '^TestLiveReadOnly$' -count=1 -v
```

Set `DISCORD_CLI_EXPECT_ACCOUNT_ID` to verify a specific account. The live test
performs no mutations.

- [Contributor map and command flow](docs/contributing.md)
- [Domain glossary](CONTEXT.md)

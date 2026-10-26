---
name: discord-cli
description: Read and search Discord conversations and download their attachments using the discord CLI and the owner's desktop account. Also use for explicitly requested message or reaction actions.
---

# Discord CLI

Use `discord` for bounded account operations. Each invocation resolves its
credential, performs HTTP work, emits JSON, and exits. Use the installed binary;
from a checkout, build with `go build -o bin/discord ./cmd/discord` and
substitute `./bin/discord`. On Windows, build with output
`bin/discord.exe` and invoke `.\bin\discord.exe`. If neither a binary
nor a checkout is available, install with
`go install github.com/10/discord-cli/cmd/discord@latest` (Go 1.26.3 or later).

Run `discord --help` and the selected command's `--help` for exact arguments
and limits. Help/version need no account. If installed help differs from the
checkout, rebuild before concluding that a command is unavailable.

## Resolve the account and destination

1. Run `discord auth status`. Read `account.id`, identifying display fields,
   and `source`; match the acting account to the user's intent before a write.
   Precedence is explicitly set `DISCORD_TOKEN`, then a saved override, then the
   desktop session. Empty or invalid explicit overrides fail without fallback.
   Each invocation resolves again; recheck identity when the selected desktop
   account or override changes.
2. Resolve the requested destination with the appropriate discovery command:

   | Destination | Discovery |
   | --- | --- |
   | Server channel | `servers list`, then `channels list SERVER_ID` |
   | Active thread or forum post | `channels list SERVER_ID`, then `threads list PARENT_CHANNEL_ID` |
   | Archived thread or forum post | `threads archived PARENT_CHANNEL_ID`; choose `--type public`, `private`, or `joined-private` |
   | Existing DM or group DM | `dms list`; match `recipients` by user ID |
   | Friend's user ID | `friends list` |
   | New one-to-one conversation requested by the user | `dms open USER_ID`; use its returned channel `id` |

   Discovery lists use `items`. Preserve IDs as strings, including in JSON tools;
   do not convert them to floating-point numbers. For a one-to-one message, choose
   a type `1` DM with that recipient; type `3` is a shared group conversation.
   Names help discover candidates; all actions use explicit IDs. If the intended
   account or destination is ambiguous, resolve that ambiguity before acting.
3. Use the selected channel ID for message, attachment, and reaction commands.
   Use a discovered thread's `id` as the message destination, rather than its
   `parent_id`; category and forum containers are not message destinations.
   Thread discovery never joins or unarchives threads. Open a DM only as part of a
   requested conversation or send.

Completion: the intended account and channel are identified by ID, and the
operation stays within the user's requested scope. Reading a conversation does
not itself request a message or reaction.

## Read only as far as needed

```sh
discord messages list CHANNEL_ID --limit 50 --format transcript
discord messages get CHANNEL_ID MESSAGE_ID
discord messages list CHANNEL_ID --around MESSAGE_ID --limit 25
discord messages list CHANNEL_ID --limit 500 --format transcript
discord messages search --channel CHANNEL_ID --query "release" --author USER_ID
discord messages search --server SERVER_ID --query "release"
discord reactions list CHANNEL_ID MESSAGE_ID
discord reactions users CHANNEL_ID MESSAGE_ID '👍'
```

Prefer `--format transcript` for reading conversation text. It sorts selected
messages chronologically and retains IDs, reply references, embed text/fields,
and attachment references. It ends with `[coverage]` plus JSON metadata. Use
default JSON for the full received schema. These formats also work on exact
reads and search; transcript search results are reordered chronologically.

For more history, increase `messages list --limit` (maximum 10,000) rather than
writing a pagination loop. The CLI fetches pages automatically within its timeout
and internal request ceiling. Check `complete` and `has_more`; `complete:true`
describes the returned collection, not exhaustive channel history. If
`has_more:true`, continue with `next_before` for older messages or `next_after`
for newer messages when the task requires it. `--around` supports at most 100
messages. Calendar filters stay on `messages search`.

Search requires exactly one of `--server` or `--channel`. A server-channel scope
is routed correctly by the CLI; do not supply both. For calendar searches, use
`--on` or `--before-date`/`--after-date`; dates always use the machine’s local
timezone. Before/after dates exclude the named day.
Search/history `--before` and `--after` still take **message IDs**; keep search
ID bounds separate from dates. History accepts one of `--before`, `--after`, or
`--around`; search can use both ID bounds. `--around` reads surrounding context,
newest first; use `messages get` when the exact message is required.
Use search help for repeatable filter values and sorting.
`--pinned` selects pinned messages; omit it for either state. Explicit false is
rejected because the live API did not honor it. Message results include author, timestamp,
reply references, attachment metadata, and reactions. A missing/deleted reply
reference does not supply the original content. Reads do not download files.

For more context, keep the scope and filters and use the returned continuation:

- **History:** newest-first `items`; `next_before` reads older messages,
  `next_after` reads newer ones. Omit `--around` when continuing in either direction.
- **Servers / reaction users:** ascending IDs; pass `next_after` as `--after`.
- **Search:** `items` follow the selected `--sort` (default newest); preserve
  the same sort and filters and pass `next_offset` as `--offset`.
- **Active threads:** descending thread IDs; preserve the parent channel and pass
  `next_before` as `--before`. This command uses channel search and may have an
  indexing delay or incomplete page.
- **Thread archives:** preserve the parent channel and `--type`; pass
  `next_before` unchanged as `--before`. Public/private cursors are RFC3339 archive
  timestamps; joined-private cursors are thread IDs. `private` requires Manage
  Threads; `joined-private` only reads threads the account joined.

Stop when the requested context is sufficient or the chosen message budget is
reached. `complete:true` describes the page/index state, not exhaustive history;
inspect `has_more` too. At `continuation_limited:true`, narrow search filters
instead of incrementing the offset. Report partial coverage when stopping early
or when `complete:false`. Search marks historical indexing and short pages with
outstanding results incomplete; offsets cannot establish an exhaustive snapshot
of changing search results. Deduplicate messages by `id` when combining pages.

## Carry out the requested write

Commands execute immediately, without confirmation flags. Once the user's action,
account, and target IDs are resolved, perform that action and retain the returned
message ID or confirmation.

```sh
discord messages send CHANNEL_ID --content 'Requested message'
discord messages send CHANNEL_ID --stdin < message.txt
discord messages send CHANNEL_ID --file ./report.pdf --reply-to MESSAGE_ID
discord messages edit CHANNEL_ID MESSAGE_ID --content 'Corrected text'
discord messages edit CHANNEL_ID MESSAGE_ID --file ./replacement.pdf --remove-attachment ATTACHMENT_ID
discord reactions add CHANNEL_ID MESSAGE_ID '👍'
discord reactions remove CHANNEL_ID MESSAGE_ID 'party:EMOJI_ID' --user USER_ID
discord messages delete CHANNEL_ID MESSAGE_ID
```

The stdin example uses POSIX shell redirection. For generated or multiline text,
feed UTF-8 stdin using the current shell's facilities; the CLI preserves input,
including trailing newlines. Choose `--content` or `--stdin`, not both. Repeat
`--file` for explicit attachments; attachment-only sends are supported. Oversized
text is rejected rather than split.

Edits affect the acting account's own message. Omitting text preserves it;
`--content ''` requests clearing it. Text-only edits retain attachments. Adding
files retains existing files unless their IDs appear in `--remove-attachment`.
Deletion and another user's reaction removal follow Discord permissions; a
moderator is not restricted to their own messages. Omit reaction `--user` to
remove your own. Emoji accepts Unicode or custom `name:id` (also `<:name:id>`).
Group management and bulk deletion are unavailable.

## Download selected attachments

Read its message first, select an attachment `id`, and choose an existing output
directory. Download from that metadata, not from arbitrary message text:

```sh
discord attachments download CHANNEL_ID MESSAGE_ID ATTACHMENT_ID --dir ./downloads --name report.pdf
discord attachments download-many CHANNEL_ID MESSAGE_A MESSAGE_B --dir ./downloads
```

For multiple explicitly selected messages, prefer `download-many` over shell
loops. It downloads every attachment in those messages, deduplicates message IDs,
and returns a per-file manifest with local paths and actual byte counts. Files
are placed under `MESSAGE_ID/ATTACHMENT_ID/FILENAME`. On `partial_download`
(exit 1), inspect both the stdout manifest and stderr error; retain successful
files and use the single-file command for the selected failures. Missing messages
are failed manifest entries. `complete:true` means the selected batch succeeded.

The CLI sends no account token to the CDN and publishes only complete files.
Existing destinations are preserved; choose another simple `--name` on collision.
The destination filesystem must support hard links. If an attachment expires,
retrieve its message again before retrying. Increase the global `--timeout` for
a deliberately large transfer. Each file has a 1 GiB ceiling. Discord can serve
original image bytes whose size/type differs from message metadata; the CLI uses
HTTP framing to validate the transfer.

## Interpret failures before retrying

Default success is one JSON value on stdout; transcript is an explicit format
option. On nonzero exit, read the JSON envelope on
stderr: `error.code`, `message`, and optional `status`, `retry_after` (seconds),
and `details`. Check exit status before consuming JSON (`pipefail` in Bash/zsh
pipelines; `$LASTEXITCODE` in PowerShell). Help/version are text exceptions.

| Condition | Next action |
| --- | --- |
| `invalid_arguments`, `bad_request` (exit 2) | Correct arguments/content using the message, `details.usage`, missing argument/flag names, and `details.help`. |
| `partial_download` (exit 1) | Inspect the stdout manifest, keep successful files, and select failed attachments explicitly before retrying. |
| `desktop_missing`, `desktop_locked`, `desktop_unavailable`, `desktop_unsupported` (exit 3) | Follow the native sign-in/access/storage guidance. Windows Stable 1.0.9257 acceptance required Discord closed; a consistency error may require the owner to quit it normally, then retry. |
| `authentication_failed`, `ambiguous_account`, `config_error` | Resolve the selected source; preserve explicit overrides unless the user requests changing them. |
| `permission_denied`, `user_action_required` (exit 3) | Stop the action. Report the permission denial or required desktop verification; do not work around it. |
| `not_found`, `attachment_unavailable` (exit 4) | Recheck the selected IDs or refresh attachment metadata. |
| `rate_limited` (exit 5), `indexing_delay` (exit 1) | The CLI already waits/retries within its deadline. Respect `retry_after` and the task budget; an indexing delay is not an empty search. |
| `uncertain_write` (exit 1) | Do not automatically resend. Inspect bounded recent history in the same destination to reconcile; report uncertainty if the evidence is inconclusive. |
| `network_failure`, `cancelled`, `invalid_upstream_data` (exit 1) | Report the failure; discard unsuccessful output. Retry reads only within the task budget. |

`auth set-token` changes the saved override using hidden terminal input or stdin;
`auth clear-token` removes only that override. Neither is required for normal
desktop use. If an override is requested, keep token input out of command arguments,
reports, and committed files; never dump environment or configuration secrets to
diagnose a source-selection problem.

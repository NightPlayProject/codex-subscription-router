# Architecture

The independently built desktop uses bundle identifier `app.cdxmux.multi`; its
Computer Use helper uses `com.cdxmux.sky.CUAService`. Neither identifier is used
by the official ChatGPT installation. These identifiers and the `.codex-mux`
state directory remain stable across the product rename so existing macOS
privacy grants, connected accounts, and sticky thread ownership continue to
work.

Codex Subscription Router replaces the copied app's bundled `codex` executable
with a small Go multiplexer and keeps the original binary beside it as
`codex.real` on macOS or `codex.real.exe` on Windows.

## Request routing

The desktop app opens one JSON-RPC app-server connection to the multiplexer.
The multiplexer starts one real app-server child for every enabled account,
each with its own `CODEX_HOME` and `CODEX_SQLITE_HOME`.

New threads are assigned using a quota-urgency score: weekly percentage
remaining divided by the hours until that account resets. Banked usage resets
add a capped bonus, while short-window usage, existing pinned-thread count, and
stable account order break close results. Reset-credit metadata is fetched in
parallel, cached for five minutes, and treated as neutral when unavailable.
Once a thread ID is known, `state.json` persists its owner. Requests, responses,
approvals, and notifications are rewritten only as needed to preserve one
coherent desktop session.

On Windows, an explicit subscription selection is also honored for existing
threads at the next `turn/start`. The multiplexer reads the thread from its
current owner, resumes the same history on the selected account, persists the
new owner only after resume succeeds, and then forwards the turn. `Automatic`
leaves existing sticky ownership unchanged. Passive thread reads and lists do
not trigger migration. Explicit selection also schedules preparation of every
known idle, open chat. Cold chats prepare on `thread/resume`; active chats defer
until a subsequent turn. Progress is exposed by the routing-preference endpoint.
Per-thread routing locks serialize preparation with desktop requests. Internal
migration lifecycle notifications are suppressed, and passive discovery only
learns unknown owners so retained source rollouts cannot undo a migration.

If the owner is depleted, the multiplexer resumes the rollout on an account
with capacity and updates ownership. Threads do not migrate for ordinary load
balancing.

## Account isolation

The Primary account uses `~/.codex`. Added accounts use
`~/.codex-mux/accounts/<id>/codex-home`. Managed configuration is copied from
the Primary account, excluding credential-store settings and project trust.
Each isolated account forces file-backed CLI and MCP OAuth credentials.

## Desktop integration

### macOS

The patcher extracts `app.asar`, verifies exact upstream anchors, inserts the
account UI, disables self-update, and repacks the archive with an updated
integrity hash. The app receives a separate Chromium profile and URL scheme.

The copied Computer Use service, Node runtime, and callers are re-signed under
one Apple team. The helper uses a separate bundle identity and socket, avoiding
the official app's privacy grants and app-group container.

### Windows

The Windows installer copies the supported `OpenAI.Codex` Microsoft Store
package to `%LOCALAPPDATA%\Programs\Codex Subscription Router` and patches only
that writable copy. The patcher verifies the exact official AppX version and
ASAR hash, extracts `app.asar`, checks unique bootstrap/renderer anchors,
disables the staged copy's updater, injects the account panel and plugin routing
scope, and repacks the archive while preserving unpacked native modules.

The launcher sets `CODEX_ELECTRON_USER_DATA_PATH` to a separate profile and also
passes Chromium's explicit `--user-data-dir` switch before starting the staged
`ChatGPT.exe`. The command-line switch makes the profile effective early enough
for Chromium services such as crashpad, while the environment variable is kept
for the app bootstrap. The staged Windows bootstrap has a fail-closed patch that
uses `%LOCALAPPDATA%\Codex Subscription Router\User Data` when that variable is
absent after an internal Electron relaunch. The existing Windows Computer
Use/runtime files are copied unchanged; the Windows port does not apply the
macOS bundle ID, Mach-O, entitlement, or code-signing patches.

## Plugin behavior

Plugin definitions and managed MCP configuration are shared. The Plugins page
adds an account selector and marks Apps, MCP status, and MCP OAuth requests with
the selected account ID. The multiplexer removes that private routing marker
before forwarding the strict RPC request to the chosen child.

## Control API

The renderer talks to a loopback-only HTTP service on port 48123. All private
routes require a random 256-bit token. CORS is limited to the copied app's
`app://-` origin. The service exposes account metadata, aggregated usage and
profile data, thread ownership, login/logout actions, and an authenticated SSE
event stream; it never returns OAuth tokens.

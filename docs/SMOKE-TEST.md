# Signed-app smoke test

Complete this checklist on the exact official build recorded in
`docs/COMPATIBILITY.md` before publishing a release draft. Use a team-backed
signature and reuse the same Apple team as the previous installed build.

## Build and identity

- Confirm the patcher reports the expected version, build, and ASAR SHA-256.
- Verify the official `/Applications/ChatGPT.app` is unchanged.
- Verify the app and every nested Computer Use application with
  `codesign --verify --deep --strict`.
- Confirm the installed app and helper report the intended bundle IDs and the
  same `TeamIdentifier`.

## Accounts and routing

- Connect at least two subscriptions and confirm photos, plans, masked emails,
  pooled usage, and loading states.
- Start chats until each account has received one; confirm every follow-up stays
  on its original account.
- Spoof one depleted account and confirm the thread continues on an account with
  quota. Spoof all accounts depleted and confirm the combined alert.
- Open a quota-triggered reset sheet, switch subscriptions, consume a reset, and
  confirm only the selected account changes.

## Settings and plugins

- Confirm Profile opens in the combined state, uses 20 px avatar overlap, and
  toggles between combined and per-account statistics.
- In Settings → Plugins, select each subscription and verify Apps, MCP status,
  and MCP OAuth login reflect that account while installed definitions remain
  shared.

## Appshots and Computer Use

- In System Settings, grant Accessibility to Codex Subscription Router and
  Screen & System Audio Recording to Codex Subscription Router Computer Use.
  Quit and reopen when macOS asks.
- Capture an Appshot from the attachment menu and with the Command-key shortcut.
- Run a Computer Use task and confirm the native helper performs the action
  without falling back to `osascript`.
- Rebuild once with the same signing team and confirm existing permissions still
  work without adding duplicate permission rows.

Record the tested commit, macOS version, signing team ID, and any deviations in
the release draft before publishing it.

## Windows x64 staging smoke test

Run these checks only on the exact Windows package recorded in
`docs/COMPATIBILITY.md`.

- Confirm `install.ps1` reports the expected `OpenAI.Codex` package version and
  exact source `app.asar` SHA-256 before staging.
- Hash the official `WindowsApps` `app.asar` before and after staging and confirm
  it is unchanged.
- Run the staged `resources\codex.exe --version` and confirm it passes through
  to `resources\codex.real.exe`.
- Start an isolated staged mux `app-server`, authenticate to `/v1/health` with
  the generated control token, and confirm the health response is `ok: true`.
- With the official Store app still running, launch only the staged app through
  `Launch-CodexSubscriptionRouter.ps1`. Verify any new `ChatGPT.exe` process is
  loaded from the staged path and uses the isolated user-data directory.
- Confirm the subscription panel loads and the control API remains loopback
  only. Exercise account add/enable/disable and sticky routing with test
  subscriptions before calling routing behavior production-ready.
- With an existing chat owned by Primary, select Subscription 2 and send one
  new turn. Confirm the chat resumes on Subscription 2, its history remains
  intact, and subsequent turns stay on Subscription 2. Switch back to
  Automatic and confirm no migration occurs on the next turn.
- Test Windows Computer Use independently. Do not infer Computer Use support
  from ASAR staging, mux health, or ordinary desktop launch alone.
- Select a subscription with several existing chats. Verify preparation progress,
  an idle chat, a reopened cold chat, and a chat with a running reply. Refresh the
  list repeatedly and confirm owners do not revert or chats appear twice. Test
  both directions before claiming live migration success.
- Rename an account inline, save it, refresh, and verify persistence. Verify
  Cancel and an empty name, and confirm failed preparation is visible in the
  subscription panel without claiming the affected chat is ready.

Record the tested commit, Windows version, AppX package version, source ASAR
hash, staged ASAR hash, GUI result, and Computer Use result for each release.

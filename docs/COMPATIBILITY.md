# Compatibility

The patcher is intentionally tied to known ChatGPT desktop bundle structures.
It verifies every modified renderer, main-process, and native binary anchor and
stops instead of applying a partial patch.

## Release 0.1.0

| Component | Tested value |
| --- | --- |
| Official ChatGPT version | `26.803.61601` |
| Official bundle build | `6396` |
| `app.asar` SHA-256 | `d5a44ed9e2f1db5f81dbbe85408aed256f3203c5b16f00817bb9d7cd941343cf` |
| Architecture | Apple silicon (`arm64`) |

A different official version may work when all anchors remain identical, but
it is unverified. The patcher rejects a version, build, or ASAR hash mismatch by
default; `--allow-untested-source` is an explicit diagnostic override. Never
weaken an anchor-count or binary-constant check merely to make a new build
complete. Review the upstream change and update the patch deliberately.

## Windows x64 staging target

| Component | Tested value |
| --- | --- |
| Package identity | `OpenAI.Codex` |
| Microsoft Store package version | `26.908.9136.0` |
| Electron app package version | `26.908.70816` |
| Codex build number | `9275` |
| `app.asar` SHA-256 | `7a46bd6fe162050afbac27d7d5271d19524e887fa0cdd06c0f2d3fa9b606a31d` |
| Architecture | Windows x64 |

`install.ps1` and `scripts/patch_app_windows.py` require the exact AppX identity,
package version, ASAR hash, and unique renderer/bootstrap anchors above. There is
no Windows compatibility override: a changed official build stops before the
staged copy is patched. The Microsoft Store package is read-only input; the
patcher operates only on a per-user copy.

Validation for this build covers the full Windows checks (Go tests/vet, account
panel, patcher, launcher and endpoint tests), applying all three patches to the
actual official ASAR and checking the resulting JavaScript syntax, and native
app-server goal migration between disposable homes. The migration check moves a
goal in both directions, including a changed rollout path, and verifies preserved
usage counters. No authenticated model turn was submitted.

The Windows patch now preserves both official updater startup calls, allowing
Codex update checks and notifications to run. Router updates remain available
through the separate Subscriptions panel. An official app update may require a
new compatible router build; this patch does not automatically approve unknown
ASAR hashes. Live update notifications and quota exhaustion in the desktop UI
have not been verified on this build.

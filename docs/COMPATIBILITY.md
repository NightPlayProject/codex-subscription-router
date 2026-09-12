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
| Microsoft Store package version | `26.908.4834.0` |
| Electron app package version | `26.908.40834` |
| Codex build number | `8881` |
| `app.asar` SHA-256 | `2bd5b96a48232f3ccf3df6be50965920699ea3a1b4512dcdd770e209fd1f009e` |
| Architecture | Windows x64 |

`install.ps1` and `scripts/patch_app_windows.py` require the exact AppX identity,
package version, ASAR hash, and unique renderer/bootstrap anchors above. There is
no Windows compatibility override: a changed official build stops before the
staged copy is patched. The Microsoft Store package is read-only input; the
patcher operates only on a per-user copy.

Current Windows validation covers the Go test/vet suite, Windows patcher unit
tests, a staged ASAR/build with the exact hash above, router passthrough to the
bundled `codex.real.exe`, and an authenticated mux control-API smoke test. On the
test machine, the staged Electron process exits immediately while the official
Store app remains open, so the desktop GUI and Windows Computer Use integration
are not yet claimed as validated.

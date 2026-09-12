from __future__ import annotations

import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
MODULE_PATH = ROOT / "scripts" / "patch_app_windows.py"
SPEC = importlib.util.spec_from_file_location("patch_app_windows", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
patcher = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(patcher)


class WindowsPatcherTests(unittest.TestCase):
    def test_powershell_single_quoted_escapes_apostrophes_without_backslash_rewrites(self) -> None:
        value = r"C:\Users\O'Brien\Codex Router"
        self.assertEqual(
            patcher.powershell_single_quoted(value),
            r"'C:\Users\O''Brien\Codex Router'",
        )

    def test_bootstrap_patch_requires_exact_anchors(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            extracted = Path(temporary)
            build = extracted / ".vite" / "build"
            build.mkdir(parents=True)
            bootstrap = build / "bootstrap-test.js"
            bootstrap.write_text(
                "function w({appDataPath:e,buildFlavor:n,env:r}){let i=r.CODEX_ELECTRON_USER_DATA_PATH?.trim();"
                "if(i)return(0,o.resolve)(i);let a=t.ko(n),s=(0,o.join)(e,a==null?`Codex`:`Codex (${a})`),"
                "c=r.CODEX_ELECTRON_AGENT_RUN_ID?.trim()||null;return s};"
                '"CODEX_ELECTRON_USER_DATA_PATH";'
                "async function x(){await i.initialize();await i.startUpdaterAfterStartupFailure()}\n",
                encoding="utf-8",
            )

            patched = patcher.patch_bootstrap(extracted)
            data = patched.read_text(encoding="utf-8")
            self.assertIn("process.platform===`win32`", data)
            self.assertIn("r.LOCALAPPDATA?.trim()", data)
            self.assertIn("`Codex Subscription Router`,`User Data`", data)
            self.assertNotIn("await i.initialize()", data)
            self.assertNotIn("await i.startUpdaterAfterStartupFailure()", data)
            self.assertEqual(data.count("await Promise.resolve()"), 2)

            bootstrap.write_text(
                "function w({appDataPath:e,buildFlavor:n,env:r}){let i=r.CODEX_ELECTRON_USER_DATA_PATH?.trim();"
                "if(i)return(0,o.resolve)(i);let a=t.ko(n),s=(0,o.join)(e,a==null?`Codex`:`Codex (${a})`),"
                "c=r.CODEX_ELECTRON_AGENT_RUN_ID?.trim()||null;return s};"
                "async function x(){await i.initialize();"
                "await i.startUpdaterAfterStartupFailure()}\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "isolated-profile support"):
                patcher.patch_bootstrap(extracted)

            bootstrap.write_text(
                '"CODEX_ELECTRON_USER_DATA_PATH";"CODEX_ELECTRON_USER_DATA_PATH";'
                "async function x(){await i.initialize();await i.startUpdaterAfterStartupFailure()}\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "Windows isolated-profile anchor"):
                patcher.patch_bootstrap(extracted)

    def test_renderer_patch_adds_loopback_and_scoped_bridge_once(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            extracted = Path(temporary)
            assets = extracted / "webview" / "assets"
            assets.mkdir(parents=True)
            index = extracted / "webview" / "index.html"
            index.write_text(
                '<meta http-equiv="Content-Security-Policy" content="connect-src &#39;self&#39;">',
                encoding="utf-8",
            )
            renderer = assets / "app-initial-test.js"
            renderer.write_text(
                "class X{async sendRequest(e,t,n){return this.assertActive(),this.requestClient.sendRequest(e,t,n)}}",
                encoding="utf-8",
            )

            patched = patcher.patch_renderer(extracted, "a" * 64)
            html = index.read_text(encoding="utf-8")
            data = patched.read_text(encoding="utf-8")
            self.assertEqual(html.count("http://127.0.0.1:48123"), 1)
            self.assertIn("globalThis.__codexMuxPluginAccountId", data)
            self.assertIn("codex-mux-windows-root", data)
            self.assertNotIn("__CODEX_MUX_", data)

            with self.assertRaisesRegex(RuntimeError, "already patched"):
                patcher.patch_renderer(extracted, "a" * 64)

    def test_verify_source_rejects_hash_or_staging_drift(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            destination = root / "destination"
            for package_root in (source, destination):
                (package_root / "app" / "resources").mkdir(parents=True)
                (package_root / "AppxManifest.xml").write_text(
                    '<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10">'
                    f'<Identity Name="{patcher.SUPPORTED_PACKAGE_NAME}" Version="{patcher.SUPPORTED_PACKAGE_VERSION}" />'
                    "</Package>",
                    encoding="utf-8",
                )
                (package_root / "app" / "resources" / "app.asar").write_bytes(b"known-asar")

            expected = hashlib.sha256(b"known-asar").hexdigest()
            original = patcher.SUPPORTED_ASAR_SHA256
            patcher.SUPPORTED_ASAR_SHA256 = expected
            try:
                patcher.verify_source(source, destination)
                (destination / "app" / "resources" / "app.asar").write_bytes(b"changed")
                with self.assertRaisesRegex(RuntimeError, "does not byte-match"):
                    patcher.verify_source(source, destination)
            finally:
                patcher.SUPPORTED_ASAR_SHA256 = original


if __name__ == "__main__":
    unittest.main()

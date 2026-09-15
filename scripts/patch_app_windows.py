#!/usr/bin/env python3
"""Patch an isolated copy of the official Windows Codex app for local routing."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parent.parent
CONTROL_PORT = 48123
SUPPORTED_PACKAGE_VERSION = "26.908.9136.0"
SUPPORTED_ASAR_SHA256 = "7a46bd6fe162050afbac27d7d5271d19524e887fa0cdd06c0f2d3fa9b606a31d"
SUPPORTED_PACKAGE_NAME = "OpenAI.Codex"
DEFAULT_STATE_ROOT = Path.home() / ".codex-mux"
ASAR_UNPACK_DIRECTORIES = "node_modules/{@worklouder,better-sqlite3,node-mac-permissions,node-pty,objc-js}"
RPC_METHODS = (
    "app/installed",
    "app/list",
    "app/read",
    "mcpServer/oauth/login",
    "mcpServerStatus/list",
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="Official OpenAI.Codex package root (read-only input).")
    parser.add_argument("--destination", type=Path, required=True, help="Already copied writable package root to patch.")
    parser.add_argument("--mux-exe", type=Path, required=True, help="Built codex-mux.exe to install in the staged copy.")
    return parser.parse_args()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def powershell_single_quoted(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def launcher_script(user_data: Path) -> str:
    return (
        "$ErrorActionPreference = 'Stop'\n"
        f"$userData = {powershell_single_quoted(str(user_data))}\n"
        "$env:CODEX_ELECTRON_USER_DATA_PATH = $userData\n"
        f"$env:CODEX_MUX_HOME = {powershell_single_quoted(str(DEFAULT_STATE_ROOT))}\n"
        "$exe = Join-Path $PSScriptRoot 'app\\ChatGPT.exe'\n"
        "$userDataArg = '--user-data-dir=\"' + $userData + '\"'\n"
        "$env:CODEX_ROUTER_INSTALL_ROOT = $PSScriptRoot\n"
        "if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'Wallpapers.ps1')) {\n"
        "  & (Join-Path $PSScriptRoot 'Wallpapers.ps1') -Executable $exe -UserDataArgument $userDataArg\n"
        "} else {\n"
        "  Start-Process -FilePath $exe -ArgumentList $userDataArg -WorkingDirectory (Split-Path $exe -Parent)\n"
        "}\n"
    )


def run(command: list[str], cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(command, cwd=cwd, check=True, text=True, capture_output=True)


def manifest_identity(package_root: Path) -> tuple[str, str]:
    manifest = package_root / "AppxManifest.xml"
    if not manifest.is_file():
        raise RuntimeError(f"missing AppxManifest.xml: {manifest}")
    root = ET.parse(manifest).getroot()
    identity = next((node for node in root.iter() if node.tag.endswith("Identity")), None)
    if identity is None:
        raise RuntimeError("AppxManifest.xml has no package Identity")
    return identity.attrib.get("Name", ""), identity.attrib.get("Version", "")


def load_or_create_token() -> str:
    DEFAULT_STATE_ROOT.mkdir(parents=True, exist_ok=True)
    token_path = DEFAULT_STATE_ROOT / "control-token"
    if token_path.exists():
        token = token_path.read_text(encoding="utf-8").strip()
        if re.fullmatch(r"[0-9a-f]{64}", token) is None:
            raise RuntimeError(f"invalid control token at {token_path}")
        return token
    token = secrets.token_hex(32)
    token_path.write_text(token, encoding="utf-8")
    return token


def ensure_asar_tool() -> Path:
    package_manifest = PROJECT_ROOT / "node_modules" / "@electron" / "asar" / "package.json"
    tool = PROJECT_ROOT / "node_modules" / "@electron" / "asar" / "bin" / "asar.mjs"
    if not package_manifest.is_file() or not tool.is_file():
        raise RuntimeError("run `npm ci --ignore-scripts` before patching")
    expected = json.loads((PROJECT_ROOT / "package.json").read_text(encoding="utf-8"))["devDependencies"]["@electron/asar"]
    actual = json.loads(package_manifest.read_text(encoding="utf-8")).get("version")
    if actual != expected:
        raise RuntimeError(f"installed @electron/asar is {actual!r}, expected {expected!r}")
    return tool


def verify_source(source: Path, destination: Path) -> tuple[Path, Path]:
    source = source.resolve()
    destination = destination.resolve()
    if source == destination:
        raise RuntimeError("source and destination must differ; the Store package is never patched in place")
    name, version = manifest_identity(source)
    if name != SUPPORTED_PACKAGE_NAME or version != SUPPORTED_PACKAGE_VERSION:
        raise RuntimeError(f"unsupported Windows package {name!r} {version!r}; expected {SUPPORTED_PACKAGE_NAME} {SUPPORTED_PACKAGE_VERSION}")
    source_asar = source / "app" / "resources" / "app.asar"
    destination_asar = destination / "app" / "resources" / "app.asar"
    if not source_asar.is_file() or not destination_asar.is_file():
        raise RuntimeError("source or staged copy is missing app/resources/app.asar")
    source_hash = sha256(source_asar)
    destination_hash = sha256(destination_asar)
    if source_hash != SUPPORTED_ASAR_SHA256:
        raise RuntimeError(f"unsupported official app.asar hash {source_hash}; expected {SUPPORTED_ASAR_SHA256}")
    if destination_hash != source_hash:
        raise RuntimeError("staged app.asar does not byte-match the official source before patching")
    return source_asar, destination_asar


def patch_bootstrap(extracted: Path) -> Path:
    bundles = list((extracted / ".vite" / "build").glob("bootstrap-*.js"))
    if len(bundles) != 1:
        raise RuntimeError(f"expected exactly one bootstrap bundle, found {len(bundles)}")
    path = bundles[0]
    data = path.read_text(encoding="utf-8")
    if data.count("CODEX_ELECTRON_USER_DATA_PATH") != 2:
        raise RuntimeError("could not verify isolated-profile support in bootstrap")
    profile_anchor = (
        "function w({appDataPath:e,buildFlavor:n,env:r}){let i=r.CODEX_ELECTRON_USER_DATA_PATH?.trim();"
        "if(i)return(0,o.resolve)(i);let a=t.ko(n),s=(0,o.join)(e,a==null?`Codex`:`Codex (${a})`)"
    )
    if data.count(profile_anchor) != 1:
        raise RuntimeError("could not verify exact Windows isolated-profile anchor")
    profile_patch = (
        "function w({appDataPath:e,buildFlavor:n,env:r}){let i=r.CODEX_ELECTRON_USER_DATA_PATH?.trim();"
        "if(i)return(0,o.resolve)(i);if(process.platform===`win32`){let i=r.LOCALAPPDATA?.trim();"
        "if(i)return(0,o.join)(i,`Codex Subscription Router`,`User Data`)}"
        "let a=t.ko(n),s=(0,o.join)(e,a==null?`Codex`:`Codex (${a})`)"
    )
    data = data.replace(profile_anchor, profile_patch, 1)
    initialize = "await i.initialize()"
    failure = "await i.startUpdaterAfterStartupFailure()"
    if data.count(initialize) != 1 or data.count(failure) != 1:
        raise RuntimeError("could not verify exact updater startup anchors")
    # Preserve the official updater so Codex update checks and notifications run.
    path.write_text(data, encoding="utf-8")
    return path


def patch_window_icons(extracted: Path) -> list[Path]:
    """Ensure Electron windows inherit the staged Codex icon where possible.

    The official executable remains untouched. This only adjusts the staged
    Electron bundle used by the router installation.
    """
    build = extracted / ".vite" / "build"
    bundles = list(build.glob("main-*.js"))
    if not bundles:
        raise RuntimeError("could not find Electron main bundle for icon patch")

    if len(bundles) != 1:
        raise RuntimeError("expected exactly one Electron main bundle")
    path = bundles[0]
    data = path.read_text(encoding="utf-8")
    anchor = "...this.options.windowIconPath==null?{}:{icon:this.options.windowIconPath}"
    if data.count(anchor) != 1:
        raise RuntimeError("could not find a safe Electron BrowserWindow icon anchor")
    replacement = (
        "icon:this.options.windowIconPath??require('node:path').join("
        "process.resourcesPath,'chatgpt-app-dark.ico')"
        "/* CODEX_SUBSCRIPTION_ROUTER_WINDOW_ICON */"
    )
    path.write_text(data.replace(anchor, replacement, 1), encoding="utf-8")
    return [path]


def patch_renderer(extracted: Path, token: str) -> Path:
    index_path = extracted / "webview" / "index.html"
    index = index_path.read_text(encoding="utf-8")
    loopback = f"http://127.0.0.1:{CONTROL_PORT}"
    if loopback not in index:
        connect_anchor = "connect-src &#39;self&#39;"
        if index.count(connect_anchor) != 1:
            raise RuntimeError("could not verify the renderer CSP connect-src anchor")
        index = index.replace(connect_anchor, f"{connect_anchor} {loopback}", 1)
        index_path.write_text(index, encoding="utf-8")

    bundles = list((extracted / "webview" / "assets").glob("app-initial-*.js"))
    if len(bundles) != 1:
        raise RuntimeError(f"expected exactly one initial renderer bundle, found {len(bundles)}")
    path = bundles[0]
    data = path.read_text(encoding="utf-8")
    if "codex-mux-windows-root" in data:
        raise RuntimeError("staged renderer is already patched")

    bridge = "async sendRequest(e,t,n){return this.assertActive(),this.requestClient.sendRequest(e,t,n)}"
    if data.count(bridge) != 1:
        raise RuntimeError("could not verify the unique app-facing sendRequest bridge")
    methods = json.dumps(list(RPC_METHODS), separators=(",", ":"))
    scoped_bridge = (
        f"async sendRequest(e,t,n){{this.assertActive();let r=t,a=globalThis.__codexMuxPluginAccountId;"
        f"if(a&&{methods}.includes(e)&&(r==null||typeof r===\"object\"&&!Array.isArray(r)))"
        "r={...(r||{}),codexMuxAccountId:a};return this.requestClient.sendRequest(e,r,n)}"
    )
    data = data.replace(bridge, scoped_bridge, 1)

    panel = (PROJECT_ROOT / "ui" / "windows-account-panel.js").read_text(encoding="utf-8")
    panel = panel.replace("__CODEX_MUX_CONTROL_PORT__", str(CONTROL_PORT)).replace("__CODEX_MUX_CONTROL_TOKEN__", token)
    if "__CODEX_MUX_" in panel:
        raise RuntimeError("unresolved Windows panel placeholder")
    data += "\n/* Codex Subscription Router Windows panel */\n" + panel + "\n"
    path.write_text(data, encoding="utf-8")
    return path


def patch_destination(source: Path, destination: Path, mux_exe: Path) -> None:
    source_asar, destination_asar = verify_source(source, destination)
    mux_exe = mux_exe.resolve()
    if not mux_exe.is_file():
        raise RuntimeError(f"missing built multiplexer: {mux_exe}")
    resources = destination.resolve() / "app" / "resources"
    bundled = resources / "codex.exe"
    real = resources / "codex.real.exe"
    if not bundled.is_file():
        raise RuntimeError(f"staged copy is missing bundled Codex: {bundled}")
    if real.exists():
        raise RuntimeError(f"staged copy already contains {real.name}")

    asar_tool = ensure_asar_tool()
    token = load_or_create_token()
    with tempfile.TemporaryDirectory(prefix="codex-router-windows-") as temp:
        temp_path = Path(temp)
        extracted = temp_path / "asar"
        repacked = temp_path / "app.asar"
        run([sys.executable, "-c", "import sys; print(sys.version)"])
        run(["node", str(asar_tool), "extract", str(destination_asar), str(extracted)])
        bootstrap = patch_bootstrap(extracted)
        icons = patch_window_icons(extracted)
        renderer = patch_renderer(extracted, token)
        run(["node", str(asar_tool), "pack", "--unpack-dir", ASAR_UNPACK_DIRECTORIES, str(extracted), str(repacked)])
        listing = run(["node", str(asar_tool), "list", "--is-pack", str(repacked)]).stdout
        if "better_sqlite3.node" not in listing or "unpack :" not in listing:
            raise RuntimeError("native ASAR modules were not kept unpacked")
        shutil.copy2(repacked, destination_asar)
        unpacked = temp_path / "app.asar.unpacked"
        if unpacked.is_dir():
            shutil.copytree(unpacked, resources / "app.asar.unpacked", dirs_exist_ok=True)
        print(f"Patched bootstrap: {bootstrap.name}")
        print(f"Patched window icons: {', '.join(path.name for path in icons)}")
        print(f"Patched renderer: {renderer.name}")

    bundled.rename(real)
    shutil.copy2(mux_exe, bundled)

    launcher = destination.resolve() / "Launch-CodexSubscriptionRouter.ps1"
    user_data = Path(os.environ.get("LOCALAPPDATA", str(Path.home() / "AppData" / "Local"))) / "Codex Subscription Router" / "User Data"
    launcher.write_text(launcher_script(user_data), encoding="utf-8")

    if sha256(source_asar) != SUPPORTED_ASAR_SHA256:
        raise RuntimeError("official source app.asar changed during patching")
    if sha256(destination_asar) == SUPPORTED_ASAR_SHA256:
        raise RuntimeError("patched staged app.asar unexpectedly matches the official source hash")
    if not real.is_file() or not bundled.is_file():
        raise RuntimeError("Codex executable swap did not complete")


def main() -> int:
    args = parse_args()
    patch_destination(args.source, args.destination, args.mux_exe)
    print(f"Windows staged copy patched successfully: {args.destination.resolve()}")
    print("The official Microsoft Store package was used only as read-only input.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

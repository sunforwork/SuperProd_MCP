import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = (("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64"))

def run(*args, env=None, capture=False):
    return subprocess.run(args, cwd=ROOT, env=env, check=True, text=True,
                          encoding="utf-8", stdout=subprocess.PIPE if capture else None).stdout

def version(tag):
    current = json.loads((ROOT / "plugin/plugin.json").read_text(encoding="utf-8"))["version"]
    if tag:
        if not re.fullmatch(r"v[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[0-9A-Za-z.-]+)?", tag):
            raise SystemExit("Invalid release tag")
        selected = tag[1:]
        if re.fullmatch(r"[0-9]+\.[0-9]+", selected):
            selected += ".0"
        if selected != current:
            raise SystemExit("Release tag does not match plugin version")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", current):
        raise SystemExit("Invalid plugin version")
    return current

def add(bundle, name, body, executable=False):
    item = zipfile.ZipInfo(name, (2020, 1, 1, 0, 0, 0))
    item.create_system = 3
    item.external_attr = ((0o100755 if executable else 0o100644) << 16)
    item.compress_type = zipfile.ZIP_DEFLATED
    bundle.writestr(item, body)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", default="")
    options = parser.parse_args()
    selected = version(options.tag)
    run("go", "vet", "./...")
    output = ROOT / "dist"
    output.mkdir(exist_ok=True)
    names = [f"super-productivity-mcp-{system}-{arch}.zip" for system, arch in TARGETS]
    names += ["super-productivity-mcp-plugin-source.zip", "SHA256SUMS.txt"]
    collisions = [name for name in names if (output / name).exists()]
    if collisions:
        raise SystemExit("Output already exists; use an empty dist directory: " + ", ".join(collisions))
    legal_files = [ROOT / n for n in ("LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "README.md", "upstream.lock.json")]
    resources = sorted(p for p in (ROOT / "licenses").rglob("*") if p.is_file())
    host_os = run("go", "env", "GOHOSTOS", capture=True).strip()
    host_arch = run("go", "env", "GOHOSTARCH", capture=True).strip()
    with tempfile.TemporaryDirectory(prefix="spmcp-package-") as temporary:
        binaries = Path(temporary)
        native = None
        for system, arch in TARGETS:
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            binary = binaries / (system + "-" + arch + (".exe" if system == "windows" else ""))
            run("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", str(binary), "./cmd/spmcp", env=env)
            if (system, arch) == (host_os, host_arch):
                native = binary
            destination = output / f"super-productivity-mcp-{system}-{arch}.zip"
            with zipfile.ZipFile(destination, "w") as bundle:
                add(bundle, "spmcp.exe" if system == "windows" else "spmcp", binary.read_bytes(), True)
                for file in legal_files + resources:
                    add(bundle, file.relative_to(ROOT).as_posix(), file.read_bytes())
            print("Built " + destination.name)
        if native is None:
            native = binaries / ("native.exe" if host_os == "windows" else "native")
            run("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", str(native), "./cmd/spmcp",
                env=dict(os.environ, GOOS=host_os, GOARCH=host_arch, CGO_ENABLED="0"))
        run(str(native), "licenses", capture=True)
        plugin = binaries / "plugin.zip"
        run(str(native), "plugin-source", "--output", str(plugin), capture=True)
        with zipfile.ZipFile(plugin) as source, zipfile.ZipFile(output / names[-2], "w") as destination:
            for item in sorted(source.infolist(), key=lambda p: p.filename):
                add(destination, item.filename, source.read(item))
        sums = "".join(hashlib.sha256((output / name).read_bytes()).hexdigest() + "  " + name + "\n" for name in names[:-1])
        (output / "SHA256SUMS.txt").write_text(sums, encoding="utf-8")
    print("Packaged version " + selected)

if __name__ == "__main__":
    main()

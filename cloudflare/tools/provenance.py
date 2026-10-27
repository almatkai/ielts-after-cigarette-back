#!/usr/bin/env python3
"""Reproduce selected DEV image executables locally; never executes the binaries."""

import hashlib, json, os, pathlib, subprocess

CF = pathlib.Path(__file__).resolve().parents[1]
SHA = "40ed0dc5fca0b9df989a29d421a81476e7df0f50"
EXPECTED = {
    "api": "2f2f9f3a076369b888a402a2d926afe7827beab33bdab81e506d44d5be314b76",
    "worker": "39e9db0064f37800958bc50a69a2c45f0e8ab487f9246c443ffca590f488ae6f",
}
source = CF / ".artifacts" / SHA / "source"
if not source.exists():
    raise SystemExit("Run the pinned source builder first")
env = dict(
    os.environ, GOTOOLCHAIN="go1.26.10", GOOS="linux", GOARCH="amd64", CGO_ENABLED="0"
)
for name, expected in EXPECTED.items():
    output = CF / ".artifacts" / ("provenance-" + name)
    subprocess.run(
        [
            "go",
            "build",
            "-trimpath",
            "-buildvcs=false",
            "-ldflags=-s -w",
            "-o",
            str(output),
            "./cmd/" + name,
        ],
        cwd=source,
        env=env,
        check=True,
    )
    actual = hashlib.sha256(output.read_bytes()).hexdigest()
    if actual != expected:
        raise SystemExit("DEV source/binary provenance mismatch: " + name)
    print(name + ": SHA-256 matches the read-only live image observation")

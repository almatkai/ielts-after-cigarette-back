#!/usr/bin/env python3
"""Build a pinned snapshot using Go's overlay; never modify the source archive."""

import subprocess, pathlib, json, hashlib, shutil, os, tarfile, io

ROOT = pathlib.Path(__file__).resolve().parents[2]
CF = ROOT / "cloudflare"
SHA = "40ed0dc5fca0b9df989a29d421a81476e7df0f50"
artifact = CF / ".artifacts" / SHA
source = artifact / "source"
overlaydir = artifact / "overlay"
overlaydir.mkdir(parents=True, exist_ok=True)
archive = subprocess.check_output(["git", "archive", SHA], cwd=ROOT)
if not source.exists():
    source.mkdir()
    tarfile.open(fileobj=io.BytesIO(archive)).extractall(source)
manifest = {
    "revision": SHA,
    "archive_sha256": hashlib.sha256(archive).hexdigest(),
    "files": {
        str(p.relative_to(source)): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in source.rglob("*")
        if p.is_file()
    },
}
expected = artifact / "source-manifest.json"
if expected.exists() and json.loads(expected.read_text()) != manifest:
    raise SystemExit("Snapshot bytes changed")
expected.write_text(json.dumps(manifest, indent=2) + "\n")
replacements = {}
for f in source.rglob("*.go"):
    if f.name.endswith("_test.go"):
        continue
    b = f.read_text()
    a = b.replace(
        '"github.com/jackc/pgx/v5/pgxpool"',
        '"github.com/almatkai/ielts-after-cigarette-back/internal/cf/pgxpool"',
    ).replace(
        '"github.com/redis/go-redis/v9"',
        '"github.com/almatkai/ielts-after-cigarette-back/internal/cf/redis"',
    )
    if f.name == "recordings.go" and f.parent.name == "attempts":
        a = a.replace(
            "data, err := io.ReadAll(io.LimitReader(file, limit+1))",
            "if source, ok := file.(interface{ CloudflareKey() string }); ok { return &SpeakingAudio{CloudflareKey: source.CloudflareKey(), Format: audioFormat(recording.MimeType, recording.StorageKey)}, nil }; data, err := io.ReadAll(io.LimitReader(file, limit+1))",
        )
    if f.name == "openrouter.go" and f.parent.name == "attempts":
        a = a.replace(
            "type SpeakingAudio struct {",
            "type SpeakingAudio struct { CloudflareKey string;",
        )
        a = a.replace('Data   string `json:"data"`', 'Data   any `json:"data"`')
        a = a.replace(
            "part.Audio == nil || len(part.Audio.Data) == 0",
            'part.Audio == nil || (len(part.Audio.Data) == 0 && part.Audio.CloudflareKey == "")',
        )
        a = a.replace(
            "base64.StdEncoding.EncodeToString(part.Audio.Data)",
            "cfhttp.AudioData(part.Audio.CloudflareKey, base64.StdEncoding.EncodeToString(part.Audio.Data))",
        )
        a = a.replace(
            "import (",
            'import (\n cfhttp "github.com/almatkai/ielts-after-cigarette-back/internal/cf/http"',
            1,
        )
    if f.name == "service.go" and f.parent.name == "blog":
        a = a.replace(
            "return s.repository.GetCertificate(ctx, application.CertificateMediaID, false)",
            "return cloudflareCertificate(ctx, s.repository, application.ID, application.CertificateMediaID)",
        )
    if f.name == "stt.go" and f.parent.name == "listening":
        start = a.index(
            "\text := strings.ToLower", a.index("func (s *STTService) Transcribe(")
        )
        end = a.index('\tif s.sttKey != ""', start)
        original = a[start:end].replace(
            "req, err := http.NewRequestWithContext",
            "req, err = http.NewRequestWithContext",
        )
        a = (
            a[:start]
            + "\tvar req *http.Request; var err error; if source, ok := audioStream.(interface{ CloudflareKey() string }); ok { req, err = cfhttp.TranscriptionRequest(ctx,s.sttURL,filename,source.CloudflareKey()); if err != nil {return STTResult{},err} } else {\n"
            + original
            + "\n}\n"
            + a[end:]
        )
        a = a.replace(
            "import (",
            'import (\n cfhttp "github.com/almatkai/ielts-after-cigarette-back/internal/cf/http"',
            1,
        )
    if f.name == "network.go" and f.parent.name == "aiproviders":
        start = a.index(
            "\ttransport := http.DefaultTransport.(*http.Transport).Clone()"
        )
        end = a.index("\n}", start)
        a = (
            a[:start]
            + "\treturn &http.Client{Transport: cfhttp.Transport{Protected: protected}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}"
            + a[end:]
        )
        a = a.replace(
            "import (",
            'import (\n cfhttp "github.com/almatkai/ielts-after-cigarette-back/internal/cf/http"',
            1,
        )
    if "r.ParseMultipartForm(" in a or "r.FormFile(" in a or "http.ServeContent(" in a:
        a = (
            a.replace("r.ParseMultipartForm(", "cfhttp.ParseMultipartForm(r,")
            .replace("r.FormFile(", "cfhttp.FormFile(r,")
            .replace("http.ServeContent(", "cfhttp.ServeContent(")
        )
        a = a.replace(
            "import (",
            'import (\n cfhttp "github.com/almatkai/ielts-after-cigarette-back/internal/cf/http"',
            1,
        )
    if b != a:
        dest = overlaydir / f.relative_to(source)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(a)
        replacements[str(f)] = str(dest)
for f in (CF / "go").rglob("*.go"):
    dest = source / (
        "cmd/cloudflare/main.go"
        if f.name == "main.go"
        else "internal/writingpipeline/cloudflare.go"
        if f.parent.name == "hooks" and f.name == "writing.go"
        else "internal/blog/cloudflare.go"
        if f.parent.name == "hooks" and f.name == "blog.go"
        else str(pathlib.Path("internal/cf") / f.relative_to(CF / "go"))
    )
    replacements[str(dest)] = str(f)
overlay = artifact / "overlay.json"
overlay.write_text(json.dumps({"Replace": replacements}, indent=2))
dist = CF / "dist"
dist.mkdir(exist_ok=True)
env = dict(
    os.environ,
    GOTOOLCHAIN="go1.26.10",
    GOOS="js",
    GOARCH="wasm",
    CGO_ENABLED="0",
    GOMEMLIMIT="80MiB",
)
subprocess.run(
    [
        "go",
        "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags=-s -w",
        "-overlay",
        str(overlay),
        "-o",
        str(dist / "api.wasm"),
        "./cmd/cloudflare",
    ],
    cwd=source,
    env=env,
    check=True,
)
goroot = subprocess.check_output(["go", "env", "GOROOT"], env=env, text=True).strip()
shutil.copyfile(pathlib.Path(goroot) / "lib/wasm/wasm_exec.js", dist / "wasm_exec.js")
(dist / "wasm_exec.js").write_text(
    (dist / "wasm_exec.js").read_text() + "\nexport const Go = globalThis.Go;\n"
)
after = {
    str(p.relative_to(source)): hashlib.sha256(p.read_bytes()).hexdigest()
    for p in source.rglob("*")
    if p.is_file()
}
if after != manifest["files"]:
    raise SystemExit("Archived source bytes changed during build")
manifest["go_version"] = subprocess.check_output(
    ["go", "version"], env=env, text=True
).strip()
manifest["adapter_files"] = {
    str(p.relative_to(CF)): hashlib.sha256(p.read_bytes()).hexdigest()
    for p in (CF / "go").rglob("*")
    if p.is_file()
}
release_paths = subprocess.check_output(
    ["git", "ls-files", "-c", "-o", "--exclude-standard", "cloudflare"],
    cwd=ROOT,
    text=True,
).splitlines()
manifest["release_inputs"] = {
    path: hashlib.sha256((ROOT / path).read_bytes()).hexdigest()
    for path in sorted(set(release_paths))
    if (ROOT / path).is_file()
}
manifest["adapter_git_head"] = subprocess.check_output(
    ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
).strip()
manifest["wasm_sha256"] = hashlib.sha256((dist / "api.wasm").read_bytes()).hexdigest()
manifest["wasm_bytes"] = (dist / "api.wasm").stat().st_size
(dist / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
print("Wasm bytes:", manifest["wasm_bytes"])

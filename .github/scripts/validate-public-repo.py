#!/usr/bin/env python3
"""Validate the filtered Everstack Community Edition tree before publishing."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote, urlparse

REQUIRED_PATHS = (
    "README.md",
    "LICENSE",
    "LICENSING.md",
    "NOTICE",
    "CODE_OF_CONDUCT.md",
    "CONTRIBUTING.md",
    "EDITIONS.md",
    "GOVERNANCE.md",
    "ROADMAP.md",
    "SECURITY.md",
    "SUPPORT.md",
    ".dockerignore",
    ".gitignore",
    "Makefile",
    "go.mod",
    "build/Dockerfile",
    "build/install.sh",
    "internal/enterprise/wire_ce.go",
    "pkg/grpc/everstack/gateway/v1/gateway.pb.go",
    "openapi/v1/everstack/gateway/v1/gateway.swagger.json",
    "packages/proto/.gitignore",
    "packages/proto/es/everstack/gateway/v1/gateway_pb.js",
    ".github/ISSUE_TEMPLATE/config.yml",
    ".github/PUBLIC_PROJECTION",
    ".github/dependabot.yml",
    ".github/workflows/ci.yml",
    ".github/workflows/release.yml",
    "examples/quickstart/README.md",
    "examples/quickstart/compose.yaml",
    "model-catalog/manifest.yaml",
    "model-catalog/README.md",
    "pkg/plans/plans.json",
)

FORBIDDEN_COMPONENTS = {
    "__pycache__",
    ".vscode",
    ".idea",
    "node_modules",
    # Build caches. The generation step runs pnpm install inside the work
    # directory, and postinstall hooks recreate these after the copy.
    ".next",
    ".source",
    ".turbo",
    ".wrangler",
    "coverage",
}
FORBIDDEN_BASENAMES = {".DS_Store"}
# Agent and editor instruction files hold private working notes and describe
# the private repository's layout. Compared case-insensitively, at any depth,
# the repository root included.
FORBIDDEN_AGENT_FILES = {
    "claude.md",
    "claude.local.md",
    "agents.md",
    "gemini.md",
    ".cursorrules",
    ".windsurfrules",
    ".clinerules",
    ".envrc",
    ".mcp.json",
}
# Directories that are agent or editor state wherever they appear.
FORBIDDEN_ANY_DEPTH_DIRS = {".claude", ".cursor", ".agents", ".worktrees", ".windsurf", ".codex", ".gemini"}

# The public repository is the Community Edition only. These are the
# commercial paths of the private repository: the hosted cloud services, the
# cloud dashboard, the marketing site, the multi-tenant control plane and the
# commercial licence text. None of them is published. A trailing slash marks a
# directory. Build variants (enterprise and dev) are caught by name and by
# build tag wherever they sit, so they are not listed one by one.
COMMERCIAL_PATHS = (
    "LICENSE-COMMERCIAL",
    "apps/cloud/",
    "apps/landing/",
    "internal/api/grpc/controlplane/",
    "internal/controlplane/",
    "internal/instance/",
    "services/README.md",
    "services/auth/",
    "services/billing/",
    "services/cloud/",
    "services/cmd/",
    "services/config/",
    "services/email/",
    "services/identity/",
    "services/license/",
    "services/organization/",
)

# API contracts that nothing in the Community Edition consumes: the staff
# operations console and the hosted identity service. Neither the definitions
# nor the clients generated from them are published.
UNPUBLISHED_APIS = ("operations", "identity")

# What else stays private: deployment data, internal documents, production
# operations and tooling state.
FORBIDDEN_PREFIXES = (
    ".agents",
    ".claude",
    ".cursor",
    ".github/public",
    ".github/scripts/sync-public-repo.sh",
    ".github/scripts/private-markers.tsv",
    ".github/workflows/oss-readiness.yml",
    ".worktrees",
    ".air.toml",
    "CHANGELOG.md",
    "TODO.md",
    "apps/ops",
    "build/tests/deploy-prod-rollback-boundary.test.sh",
    "build/tests/runner-pool-policy.test.sh",
    "build/tests/wait-for-image-publications.test.sh",
    "build/wait-for-image-publications.sh",
    "compare",
    "docs",
    "examples/certs",
    "infra",
    "infrastructure",
    # Founder outreach email, sent only from the staff operations console.
    "internal/email/outreach.go",
    "internal/email/service/outreach.go",
    "internal/email/service/outreach_test.go",
    "internal/email/templates/outreach.go",
    "internal/operations",
    "local_scripts",
    "model-catalog/research",
    "sandbox-agent",
    "scripts",
    "services/operations",
    "tasks",
    "test-evals",
) + tuple(entry.rstrip("/") for entry in COMMERCIAL_PATHS) + tuple(
    f"{root}/everstack/{api}"
    for api in UNPUBLISHED_APIS
    for root in (
        "proto",
        "pkg/grpc",
        "openapi/v1",
        "packages/proto/cjs",
        "packages/proto/es",
        "packages/proto/esquery",
        "packages/proto/types",
    )
)

# Exact files allowed under a forbidden prefix, each for a stated reason.
FORBIDDEN_PREFIX_EXCEPTIONS = frozenset(
    {
        # Reproducible image recipe referenced by the public OpenAI Agents
        # guide. The rest of infra/ is private deployment data.
        "infra/images/Dockerfile.openai-codex-executor",
        "infra/images/Dockerfile.openai-codex-firecracker-rootfs",
        "infra/images/prepare-openai-codex-firecracker-rootfs.sh",
        "infra/images/expand-gzip-sparse.sh",
    }
)

# Public workflows run on hosted runners with no deploy credentials. Any other
# workflow file is private until it is reviewed and added here.
PUBLIC_WORKFLOWS = frozenset(
    {
        ".github/workflows/ci.yml",
        ".github/workflows/release.yml",
        ".github/workflows/sdk-go.yml",
    }
)

# Licensing. Every published file is Apache-2.0, so the public tree carries
# no commercial licence text, no commercial licence header and no list of
# commercial paths.
LICENSING_FILE = "LICENSING.md"
COMMERCIAL_LICENCE_NAME = "Everstack Commercial License"
COMMERCIAL_LICENCE_FILE = "LICENSE-COMMERCIAL"
# The private LICENSING.md delimits its path lists with these markers. The
# public copy is a different file and must not carry them.
PRIVATE_LICENSING_MARKERS = ("commercial-paths:start", "reserved-paths:start")
# Documents that state the licence of this repository. None may point a reader
# at the commercial licence file, which is not published.
LICENCE_DOCS = ("README.md", "LICENSING.md", "NOTICE", "CONTRIBUTING.md", "EDITIONS.md", "GOVERNANCE.md")
# The only service and app directories that are published. Anything else
# under services/ or apps/ fails validation.
APACHE_TOP_LEVEL_DIRS = frozenset(
    {
        "apps/admin/",
        "apps/docs/",
        "services/fc-supervisor/",
        "services/firecracker-agent/",
        "services/sandbox-rootfs/",
    }
)
# Binaries distributed under Apache-2.0. None of them may compile a file from
# a commercial path, with no build tag or with `ce`.
APACHE_BINARIES = (".", "./services/firecracker-agent", "./services/fc-supervisor")
APACHE_BINARY_TAGS = ("", "ce", "ui_embed,ce")
APACHE_BINARY_OSES = ("linux", "darwin", "windows")
COMMUNITY_DOCS = (
    "README.md",
    "LICENSING.md",
    "CODE_OF_CONDUCT.md",
    "CONTRIBUTING.md",
    "EDITIONS.md",
    "GOVERNANCE.md",
    "ROADMAP.md",
    "SECURITY.md",
    "SUPPORT.md",
    "examples/quickstart/README.md",
)

SENSITIVE_SUFFIXES = (".key", ".pem", ".p12", ".pfx")
SENSITIVE_BASENAMES = (".env", "credentials.json", "service-account.json")
SECRET_PATTERNS = (
    ("private key", re.compile(r"-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----")),
    ("GitHub token", re.compile(r"\bgh[pousr]_[A-Za-z0-9]{30,}\b")),
    ("AWS access key", re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b")),
    ("Stripe live key", re.compile(r"\b[rs]k_live_[A-Za-z0-9]{16,}\b")),
    ("OpenAI key", re.compile(r"\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}\b")),
    ("Anthropic key", re.compile(r"\bsk-ant-[A-Za-z0-9_-]{32,}\b")),
)
# Files allowed to name a marker, each for one stated reason. Keep this list
# short and specific; a new entry is a policy decision, not a convenience.
PRIVATE_MARKER_EXEMPTIONS = frozenset(
    {
        # The OpenAI Codex Firecracker rootfs is published only as a release
        # asset in the commercial binary repository. The guide's download
        # commands must name the real URL or they do not work. Revisit when
        # EVS-141 settles where public binaries live.
        (
            "apps/docs/content/docs/getting-started/agents/openai-agents-api.mdx",
            "legacy binary repository",
        ),
    }
)
# Markers that are safe to publish: names that are already public. Markers
# that would themselves disclose something (private repository names, real
# addresses, internal tenants) are NOT listed here. The private sync passes
# them at run time with --private-markers, from a file that is never published.
PUBLIC_MARKERS = (
    ("legacy binary repository", "everstacklabs/" + "releases"),
    ("retired GitHub organization", "midfu" + "sionlabs"),
    ("retired API hostname", "api." + "midfu" + "sion.dev"),
    ("retired local hostname", "." + "midfu" + "sion.local"),
    # Any surviving mention of the pre-rename brand. The markers above are
    # written as split literals so this scan does not match this file.
    ("retired brand name", "midfu" + "sion"),
)
IPV4 = re.compile(r"^\d{1,3}(?:\.\d{1,3}){3}$")


def load_private_markers(path: Path | None, errors: list[str]) -> list[tuple[str, str]]:
    """Read "label<TAB>marker" lines; add the reverse-DNS form of any IPv4."""
    if path is None:
        return []
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        errors.append(f"could not read private markers: {exc}")
        return []
    markers: list[tuple[str, str]] = []
    for number, line in enumerate(lines, 1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if line.count("\t") != 1:
            errors.append(f"private markers line {number} must be exactly label<TAB>marker")
            continue
        label, _, marker = line.partition("\t")
        marker = marker.strip().lower()
        if not label.strip() or not marker:
            errors.append(f"private markers line {number} is not label<TAB>marker")
            continue
        markers.append((label.strip(), marker))
        if IPV4.match(marker):
            reversed_form = ".".join(reversed(marker.split("."))) + ".in-addr.arpa"
            markers.append((label.strip() + " (reverse DNS)", reversed_form))
    if not markers:
        errors.append("private markers file holds no markers; refusing to run without them")
    return markers


# Set in main(): the public markers plus any private ones for this run.
PRIVATE_MARKERS: list[tuple[str, str]] = list(PUBLIC_MARKERS)

# Shapes rather than fixed strings, for addresses that change. Each shape has
# a short list of values that are obviously synthetic and so may appear in
# tests and range checks; anything else of that shape must be replaced with one
# of them before it is published.
# Matches a tailnet address with an optional /prefix. A range constant such as
# 100.64.0.0/10 (host bits all zero) is allowed; a host written with any
# prefix, at the end of a sentence or inside a URL path is not.
TAILNET_ADDRESS = re.compile(
    r"(?<![\d.])(100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.\d{1,3}\.\d{1,3})"
    r"(?!\d|\.\d)(?:/(\d{1,2})(?!\d))?"
)


def is_network_constant(address: str, prefix: str | None) -> bool:
    if prefix is None or int(prefix) >= 32:
        return False
    value = 0
    for octet in address.split("."):
        value = (value << 8) | int(octet)
    host_bits = 32 - int(prefix)
    return value & ((1 << host_bits) - 1) == 0
TAILNET_REVERSE_DNS = re.compile(
    r"(?<![\d.])(\d{1,3})\.(\d{1,3})\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.100\.in-addr\.arpa",
    re.IGNORECASE,
)
SYNTHETIC_TAILNET_ADDRESSES = frozenset(
    {
        "100.64.0.0",  # network address of 100.64.0.0/10
        "100.64.0.1",
        "100.100.100.100",  # Tailscale's documented MagicDNS address
        "100.127.255.254",
        "100.127.255.255",  # last address of 100.64.0.0/10
    }
)
TAILNET_HOSTNAME = re.compile(r"\b[a-z0-9-]+\.ts" r"\.net\b", re.IGNORECASE)
# A tenant gateway host: slug, six hex characters, region host.
TENANT_HOSTNAME = re.compile(
    r"\b[a-z0-9-]+-([0-9a-f]{6})\.(?:dev\.)?(?:[a-z]{2}-[a-z]+-\d{1,2}|[a-z]{2})\.everstack\.ai\b",
    re.IGNORECASE,
)
# A password in a connection URL. Short local development passwords (for
# example the quickstart's) are allowed; a long one looks generated.
URL_CREDENTIAL = re.compile(
    r"\b(?:postgres(?:ql)?|redis|rediss|mysql|mariadb|mongodb|amqps?|clickhouse|nats)"
    r"(?:\+[a-z0-9]+)?://[^:/\s@'\"]*:([^@\s/'\"]{16,})@",
    re.IGNORECASE,
)
# The whole password must be a placeholder: a variable reference, a template,
# or an obvious stand-in word.
URL_CREDENTIAL_PLACEHOLDER = re.compile(
    r"^(?:\$\{?[A-Za-z0-9_]+\}?|\$\([^)]*\)|<[^>]*>|\{\{[^}]*\}\}"
    r"|.*(?:your|example|changeme|change-me|placeholder|xxxxxx).*)$",
    re.IGNORECASE,
)
SYNTHETIC_TENANT_SUFFIXES = frozenset({"a1b2c3", "abc123", "d4e5f6", "123456", "abcdef"})


def private_shape_findings(text: str) -> list[tuple[str, int]]:
    """Return (label, offset) for each address or host that looks real."""
    findings: list[tuple[str, int]] = []
    for match in TAILNET_ADDRESS.finditer(text):
        address, prefix = match.group(1), match.group(2)
        if address in SYNTHETIC_TAILNET_ADDRESSES or is_network_constant(address, prefix):
            continue
        findings.append(("tailnet address", match.start()))
    for match in TAILNET_REVERSE_DNS.finditer(text):
        forward = f"100.{match.group(3)}.{match.group(2)}.{match.group(1)}"
        if forward not in SYNTHETIC_TAILNET_ADDRESSES:
            findings.append(("tailnet address", match.start()))
    for match in TAILNET_HOSTNAME.finditer(text):
        findings.append(("tailnet hostname", match.start()))
    for match in TENANT_HOSTNAME.finditer(text):
        suffix = match.group(1).lower()
        if suffix in SYNTHETIC_TENANT_SUFFIXES or len(set(suffix)) == 1:
            continue
        findings.append(("tenant hostname", match.start()))
    for match in URL_CREDENTIAL.finditer(text):
        if not URL_CREDENTIAL_PLACEHOLDER.search(match.group(1)):
            findings.append(("credential in connection URL", match.start()))
    return findings


MARKDOWN_LINK = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
HTML_LINK = re.compile(r"(?:href|src)=[\"']([^\"']+)[\"']", re.IGNORECASE)
POSITIVE_PRIVATE_BUILD_TAG = re.compile(r"(?<![!A-Za-z0-9_])(enterprise|dev)\b")
RAW_MDX_PATH_PLACEHOLDER = re.compile(
    r"<code\b[^>]*>(?!\{)[^<\n]*/[^<\n]*"
    r"\{[A-Za-z_][A-Za-z0-9_]*\}[^<\n]*</code>"
)
PROJECTION_MARKER = "everstack-public-projection-v1\n"
MINIMUM_SAFE_RELEASE = (0, 1, 25)


def relative_files(root: Path) -> list[Path]:
    # In a checkout, inspect tracked files plus non-ignored untracked files so
    # dependency directories and build output do not make local validation
    # unusable. A force-added ignored artifact still appears via --cached. The
    # projection staging directory has no .git metadata, so it is scanned in
    # full and remains fail-closed.
    if (root / ".git").exists():
        try:
            git_files = subprocess.run(
                [
                    "git",
                    "-C",
                    str(root),
                    "ls-files",
                    "-z",
                    "--cached",
                    "--others",
                    "--exclude-standard",
                ],
                check=False,
                capture_output=True,
            )
        except OSError:
            git_files = None
        if git_files is not None and git_files.returncode == 0:
            return sorted(
                Path(value.decode("utf-8", errors="surrogateescape"))
                for value in git_files.stdout.split(b"\0")
                if value
            )

    return sorted(
        path.relative_to(root)
        for path in root.rglob("*")
        if path.is_file() or path.is_symlink()
    )


def validate_required(root: Path, errors: list[str]) -> None:
    for relative in REQUIRED_PATHS:
        if not (root / relative).exists():
            errors.append(f"required public path is missing: {relative}")


def validate_projection_marker(root: Path, errors: list[str]) -> None:
    path = root / ".github/PUBLIC_PROJECTION"
    if not path.is_file():
        return
    try:
        marker = path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"could not read public projection marker: {exc}")
        return
    if marker != PROJECTION_MARKER:
        errors.append("public projection marker is invalid")


def validate_forbidden(paths: list[Path], errors: list[str]) -> None:
    names = [path.as_posix() for path in paths]
    for prefix in FORBIDDEN_PREFIXES:
        leaked = [
            name
            for name in names
            if (name == prefix or name.startswith(prefix + "/"))
            and name not in FORBIDDEN_PREFIX_EXCEPTIONS
        ]
        if leaked:
            errors.append(f"private path leaked into public tree: {prefix} ({leaked[0]})")
    for name in names:
        if name.startswith(".github/workflows/") and name not in PUBLIC_WORKFLOWS:
            errors.append(f"unreviewed workflow in public tree: {name}")
    for path in paths:
        components = set(path.parts)
        if components & FORBIDDEN_COMPONENTS or path.name in FORBIDDEN_BASENAMES:
            errors.append(f"cache or editor artifact leaked into public tree: {path}")
        if {part.lower() for part in path.parts[:-1]} & FORBIDDEN_ANY_DEPTH_DIRS:
            errors.append(f"agent or editor state leaked into public tree: {path}")
        if path.name.lower() in FORBIDDEN_AGENT_FILES:
            errors.append(f"agent or editor instructions leaked into public tree: {path}")
        # Only the root .github directory is published; a nested one would
        # carry workflows nobody reviewed.
        if ".github" in path.parts[1:-1]:
            errors.append(f"nested .github directory in public tree: {path}")


def under_any(name: str, entries) -> bool:
    for entry in entries:
        if entry.endswith("/"):
            if name.startswith(entry):
                return True
        elif name == entry:
            return True
    return False


def file_head(path: Path, max_lines: int = 40) -> str:
    try:
        with path.open(encoding="utf-8", errors="ignore") as handle:
            return "".join(line for _, line in zip(range(max_lines), handle))
    except OSError:
        return ""


def validate_community_edition(root: Path, paths: list[Path], errors: list[str]) -> None:
    """The tree is Apache-2.0 throughout: no commercial text, code or variant."""
    # LICENSE stays the plain Apache-2.0 text so that licence detection, and
    # anyone skimming the repository, sees Apache-2.0.
    licence = file_head(root / "LICENSE", 10)
    if "Apache License" not in licence or "Version 2.0" not in licence:
        errors.append("LICENSE must be the plain Apache License 2.0 text")
    if COMMERCIAL_LICENCE_NAME in file_head(root / "LICENSE", 100000):
        errors.append("LICENSE must not carry the commercial licence")

    licensing = file_head(root / LICENSING_FILE, 100000)
    for marker in PRIVATE_LICENSING_MARKERS:
        if marker in licensing:
            errors.append(
                f"{LICENSING_FILE} is the private copy (it lists {marker.split(':')[0]}); "
                "publish the Community Edition copy"
            )
    for relative in LICENCE_DOCS:
        if COMMERCIAL_LICENCE_FILE in file_head(root / relative, 100000):
            errors.append(f"{relative} refers to {COMMERCIAL_LICENCE_FILE}, which is not published")

    for relative in paths:
        name = relative.as_posix()
        path = root / relative
        # A LICENSE file inside a directory is how the private repository
        # marks a commercial or reserved directory.
        if relative.name == "LICENSE" and len(relative.parts) > 1:
            if COMMERCIAL_LICENCE_NAME in file_head(path, 20):
                errors.append(f"commercial licence file in public tree: {name}")
        if relative.suffix != ".go":
            continue
        # Build variants that exist only for the commercial or development
        # builds are commercial, wherever they sit.
        if relative.name.endswith(("_ee.go", "_dev.go", "_ee_test.go", "_dev_test.go")):
            errors.append(f"enterprise or dev Go build variant in public tree: {name}")
            continue
        if path.is_symlink() or not path.is_file():
            continue
        try:
            lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
        except OSError:
            continue
        # A build constraint is valid anywhere before the package clause, so a
        # licence header of any length can sit above it. Read up to the clause
        # rather than a fixed number of lines.
        for line in lines:
            if line.startswith("package "):
                break
            if line.startswith("//") and COMMERCIAL_LICENCE_NAME in line:
                errors.append(f"commercial licence header in public tree: {name}")
                break
            if line.startswith(("//go:build", "// +build")) and POSITIVE_PRIVATE_BUILD_TAG.search(line):
                errors.append(f"enterprise or dev build tag in public tree: {name}")
                break

    # Only the named Apache-2.0 service and app directories are published. A
    # new directory, or a loose file directly under services/ or apps/, stays
    # private until it is reviewed and added to the list.
    for relative in paths:
        if relative.parts[0] not in ("services", "apps"):
            continue
        if len(relative.parts) < 3:
            errors.append(f"unreviewed file directly under {relative.parts[0]}/: {relative.as_posix()}")
    for top in sorted({"/".join(path.parts[:2]) + "/" for path in paths
                       if len(path.parts) > 2 and path.parts[0] in ("services", "apps")}):
        if top not in APACHE_TOP_LEVEL_DIRS:
            errors.append(f"service or app directory is not on the Community Edition list: {top}")


def validate_go_dependencies(root: Path, commercial, errors: list[str]) -> None:
    """No Apache binary may compile a file from a commercial path, on any
    target OS or tag set it is released with. In the public tree those paths
    are absent, so this also proves each binary resolves every package it
    needs without them."""
    try:
        module = re.search(
            r"^module\s+(\S+)", (root / "go.mod").read_text(encoding="utf-8"), re.MULTILINE
        ).group(1)
    except (OSError, AttributeError):
        errors.append("could not read the module path from go.mod")
        return
    template = (
        "{{if .Error}}ERROR\t{{.ImportPath}}\t{{.Error}}\n{{end}}"
        "{{if .Module}}{{if .Module.Main}}{{.ImportPath}}\t"
        "{{join .GoFiles \" \"}} {{join .CgoFiles \" \"}} {{join .EmbedFiles \" \"}}\n{{end}}{{end}}"
    )
    targets = [
        (goos, binary, tags)
        for goos in APACHE_BINARY_OSES
        for binary in APACHE_BINARIES
        for tags in APACHE_BINARY_TAGS
    ]
    for goos, binary, tags in targets:
        command = ["go", "list", "-e", "-deps", "-f", template]
        if tags:
            command.append(f"-tags={tags}")
        command.append(binary)
        env = dict(os.environ, GOOS=goos, CGO_ENABLED="0")
        result = subprocess.run(command, cwd=root, capture_output=True, text=True, env=env)
        label = f"{binary} ({goos}, tags: {tags or 'none'})"
        if result.returncode != 0:
            errors.append(f"go list failed for {label}: {result.stderr.strip()[:300]}")
            continue
        for line in result.stdout.splitlines():
            if line.startswith("ERROR\t"):
                _, package, message = line.split("\t", 2)
                # The embedded admin UI is built later in the release; only its
                # missing dist directory is tolerated.
                if "pattern all:dist: no matching files found" not in message:
                    errors.append(f"go list error for {label} in {package}: {message[:200]}")
                continue
            if "\t" not in line:
                continue
            import_path, files = line.split("\t", 1)
            if import_path.startswith(module):
                directory = import_path[len(module) :].lstrip("/")
            else:
                directory = import_path
            for file_name in files.split():
                relative = f"{directory}/{file_name}" if directory else file_name
                if under_any(relative, commercial):
                    errors.append(f"Apache binary {label} compiles commercial file {relative}")


def validate_symlinks(root: Path, paths: list[Path], errors: list[str]) -> None:
    root_resolved = root.resolve()
    for relative in paths:
        path = root / relative
        if not path.is_symlink():
            continue
        try:
            target = path.resolve(strict=True)
        except FileNotFoundError:
            errors.append(f"broken symlink in public tree: {relative}")
            continue
        try:
            target.relative_to(root_resolved)
        except ValueError:
            errors.append(f"symlink escapes public tree: {relative} -> {os.readlink(path)}")


def validate_secrets(root: Path, paths: list[Path], errors: list[str]) -> None:
    for relative in paths:
        # A marker in a file or directory name leaks as surely as one in the
        # contents, and applies to symlinks and unreadable files too.
        normalized_name = relative.as_posix().lower()
        for label, marker in PRIVATE_MARKERS:
            if marker in normalized_name:
                errors.append(f"{label} leaked into public path: {relative}")

        path = root / relative
        if path.is_symlink() or not path.is_file():
            continue

        lower_name = path.name.lower()
        if lower_name in SENSITIVE_BASENAMES or path.suffix.lower() in SENSITIVE_SUFFIXES:
            errors.append(f"sensitive file type present in public tree: {relative}")
            continue

        try:
            data = path.read_bytes()
        except OSError as exc:
            errors.append(f"could not read {relative}: {exc}")
            continue
        if b"\x00" in data:
            # Binary or UTF-16. The secret regexes would misfire on compressed
            # bytes, but the markers are fixed strings: search the raw bytes,
            # and again with NULs dropped so UTF-16 text is covered.
            lowered = data.lower()
            squeezed = lowered.replace(b"\x00", b"")
            for label, marker in PRIVATE_MARKERS:
                needle = marker.encode("ascii")
                if (needle in lowered or needle in squeezed) and (
                    relative.as_posix(),
                    label,
                ) not in PRIVATE_MARKER_EXEMPTIONS:
                    errors.append(f"{label} leaked into public file: {relative}")
            stripped = data.replace(b"\x00", b"")
            printable = sum(1 for byte in stripped if 32 <= byte < 127 or byte in (9, 10, 13))
            if stripped and printable / len(stripped) >= 0.85:
                text = stripped.decode("latin-1")
                for label, pattern in SECRET_PATTERNS:
                    if pattern.search(text):
                        errors.append(f"possible {label} in public file: {relative}")
                for label, _ in private_shape_findings(text):
                    if (relative.as_posix(), label) not in PRIVATE_MARKER_EXEMPTIONS:
                        errors.append(f"{label} leaked into public file: {relative}")
            continue
        text = data.decode("utf-8", errors="ignore")
        for label, pattern in SECRET_PATTERNS:
            if pattern.search(text):
                errors.append(f"possible {label} in public file: {relative}")
        normalized = text.lower()
        for label, marker in PRIVATE_MARKERS:
            if marker in normalized and (relative.as_posix(), label) not in PRIVATE_MARKER_EXEMPTIONS:
                errors.append(f"{label} leaked into public file: {relative}")
        for label, offset in private_shape_findings(text):
            if (relative.as_posix(), label) in PRIVATE_MARKER_EXEMPTIONS:
                continue
            line = text.count("\n", 0, offset) + 1
            errors.append(f"{label} leaked into public file: {relative}:{line}")


def validate_public_plans(root: Path, errors: list[str]) -> None:
    path = root / "pkg/plans/plans.json"
    if not path.is_file():
        return
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"could not parse public plan metadata: {exc}")
        return

    # Plan names and prices are published as they are; they match the pricing
    # page. The published token allowance is the real entitlement, and
    # ce_defaults_test.go keeps it in step with enterprise.CEUsageLimits.

    # The margin applied to platform-key inference is not published. Community
    # Edition is BYOK and never meters that path, so the public tree carries a
    # neutral multiplier. Fail closed: a renamed or moved key must not let the
    # real value through unchecked.
    credit = data.get("credit_pricing")
    markup = credit.get("inference_markup") if isinstance(credit, dict) else None
    if isinstance(markup, bool) or markup != 1.0:
        errors.append(
            "public plan metadata must carry credit_pricing.inference_markup = 1.0 "
            f"(found {markup!r})"
        )


def validate_gitignore(root: Path, errors: list[str]) -> None:
    checks = {
        ".gitignore": ("**.pb.go", "**.pb.*.go", "openapi/**", "packages/proto/**", "logs/"),
        "packages/proto/.gitignore": ("google", "protoc-gen-openapiv2", "validate", "everstack"),
    }
    for relative, forbidden_patterns in checks.items():
        path = root / relative
        if not path.is_file():
            continue
        lines = {
            line.strip()
            for line in path.read_text(encoding="utf-8").splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        }
        for forbidden in forbidden_patterns:
            if forbidden in lines:
                errors.append(
                    f"{relative} hides required generated API artifacts: {forbidden}"
                )


def validate_installer_and_release(root: Path, errors: list[str]) -> None:
    installer_path = root / "build/install.sh"
    workflow_path = root / ".github/workflows/release.yml"
    try:
        installer = installer_path.read_text(encoding="utf-8")
        workflow = workflow_path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"could not read public release files: {exc}")
        return

    version_match = re.search(
        r'^MIN_SAFE_VERSION="v(\d+)\.(\d+)\.(\d+)"$', installer, re.MULTILINE
    )
    if version_match is None or tuple(map(int, version_match.groups())) < MINIMUM_SAFE_RELEASE:
        errors.append("installer minimum safe release must be v0.1.25 or newer")
    if 'validate_release_version "$VERSION"' not in installer:
        errors.append("installer does not enforce its minimum safe release")
    # The projection rewrites RELEASES_REPO for the public tree (see
    # retarget_installer_for_ce in sync-public-repo.sh). The private file names
    # the commercial releases repository so the hosted installer works; the
    # projected file must say everstacklabs/everstack so CE does not depend on
    # that binary channel. PRIVATE_MARKERS enforces the same policy from the
    # other side by forbidding the string outright, so these two guards agree.
    # (This comment cannot spell the repository out: this file is itself part
    # of the projection and would trip its own marker scan.)
    if 'RELEASES_REPO="everstacklabs/everstack"' not in installer:
        errors.append("installer must download releases from the public source repository")

    required_fragments = (
        'tags: ["v*"]',
        "runs-on: ubuntu-latest",
        "build/install.sh --check-version",
        "ui_embed,ce",
        "actions/attest-build-provenance@v2",
    )
    for fragment in required_fragments:
        if fragment not in workflow:
            errors.append(f"public release workflow is missing required setting: {fragment}")
    for forbidden in ("self-hosted", "enterprise", "secrets."):
        if forbidden in workflow:
            errors.append(f"public release workflow contains private setting: {forbidden}")
    # No public workflow may use self-hosted runners or repository secrets
    # beyond the automatic GITHUB_TOKEN.
    for name in sorted(PUBLIC_WORKFLOWS):
        path = root / name
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8", errors="ignore")
        for runner in re.findall(r"runs-on:\s*(.+)", text):
            value = runner.strip().strip("'\"").lower()
            if not re.fullmatch(r"(ubuntu|macos|windows)-[a-z0-9.\-]+", value):
                errors.append(f"public workflow uses a non-hosted runner ({value}): {name}")
        if re.search(
            r"secrets\s*(?:\.\s*(?!GITHUB_TOKEN\b)\w|\[)|secrets:\s*inherit|tojson\(\s*secrets",
            text,
            re.IGNORECASE,
        ):
            errors.append(f"public workflow uses a repository secret: {name}")
        if re.search(
            r"\b(pull_request_target|workflow_run|issue_comment|issues|discussion|"
            r"discussion_comment|pull_request_review_comment)\s*:",
            text,
            re.IGNORECASE,
        ):
            errors.append(f"public workflow uses a privileged trigger: {name}")


def validate_quickstart(root: Path, errors: list[str]) -> None:
    compose_path = root / "examples/quickstart/compose.yaml"
    dockerfile_path = root / "build/Dockerfile"
    try:
        compose = compose_path.read_text(encoding="utf-8")
        dockerfile = dockerfile_path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"could not read quickstart build files: {exc}")
        return

    for fragment in (
        "build:",
        "dockerfile: build/Dockerfile",
        "pgvector/pgvector:pg16",
        "/debug/healthz",
    ):
        if fragment not in compose:
            errors.append(f"quickstart Compose file is missing required setting: {fragment}")
    for fragment in ("@everstack/admin... build", 'go build -tags="ui_embed,ce"'):
        if fragment not in dockerfile:
            errors.append(f"public Dockerfile is missing required build step: {fragment}")


def extract_link_target(raw: str) -> str:
    value = raw.strip()
    if value.startswith("<") and ">" in value:
        return value[1 : value.index(">")]
    # Markdown permits an optional title after a whitespace-delimited URL.
    return value.split(maxsplit=1)[0]


def validate_mdx_placeholders(
    root: Path, paths: list[Path], errors: list[str]
) -> None:
    for relative in paths:
        if relative.suffix != ".mdx" or not relative.as_posix().startswith(
            "apps/docs/content/"
        ):
            continue
        path = root / relative
        try:
            text = path.read_text(encoding="utf-8")
        except OSError as exc:
            errors.append(f"could not read {relative}: {exc}")
            continue
        if RAW_MDX_PATH_PLACEHOLDER.search(text):
            errors.append(f"unescaped MDX path placeholder: {relative}")


def validate_local_links(root: Path, errors: list[str]) -> None:
    for relative_name in COMMUNITY_DOCS:
        document = root / relative_name
        if not document.is_file():
            continue
        text = document.read_text(encoding="utf-8")
        targets = [match.group(1) for match in MARKDOWN_LINK.finditer(text)]
        targets.extend(match.group(1) for match in HTML_LINK.finditer(text))

        for raw_target in targets:
            target = unquote(extract_link_target(raw_target))
            parsed = urlparse(target)
            if (
                not target
                or target.startswith("#")
                or target.startswith("/")
                or parsed.scheme
                or parsed.netloc
            ):
                continue
            path_part = parsed.path
            if not path_part:
                continue
            destination = (document.parent / path_part).resolve()
            try:
                destination.relative_to(root.resolve())
            except ValueError:
                errors.append(f"link escapes public tree: {relative_name} -> {target}")
                continue
            if not destination.exists():
                errors.append(f"broken local link: {relative_name} -> {target}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", nargs="?", default=".", help="projected public tree")
    parser.add_argument(
        "--private-markers",
        type=Path,
        help="file of label<TAB>marker lines that must not appear; never published",
    )
    parser.add_argument(
        "--go-deps-only",
        action="store_true",
        help="only check that Apache binaries compile no commercial file (needs go and generated code)",
    )
    args = parser.parse_args()

    root = Path(args.root).resolve()
    if not root.is_dir():
        print(f"error: public tree does not exist: {root}", file=sys.stderr)
        return 2

    errors: list[str] = []
    paths = relative_files(root)
    PRIVATE_MARKERS[:] = list(PUBLIC_MARKERS) + load_private_markers(args.private_markers, errors)

    if args.go_deps_only:
        if not errors:
            validate_go_dependencies(root, COMMERCIAL_PATHS, errors)
        if errors:
            print(f"Apache binary dependency check failed with {len(errors)} error(s):", file=sys.stderr)
            for error in errors:
                print(f"  - {error}", file=sys.stderr)
            return 1
        print("Apache binaries compile no commercial file")
        return 0

    validate_required(root, errors)
    validate_projection_marker(root, errors)
    validate_forbidden(paths, errors)
    validate_community_edition(root, paths, errors)
    validate_symlinks(root, paths, errors)
    validate_secrets(root, paths, errors)
    validate_public_plans(root, errors)
    validate_gitignore(root, errors)
    validate_installer_and_release(root, errors)
    validate_quickstart(root, errors)
    validate_mdx_placeholders(root, paths, errors)
    validate_local_links(root, errors)

    if errors:
        print(f"public tree validation failed with {len(errors)} error(s):", file=sys.stderr)
        for error in errors:
            print(f"  - {error}", file=sys.stderr)
        return 1

    print(f"public tree validation passed ({len(paths)} files checked)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

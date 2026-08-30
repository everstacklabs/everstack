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

FORBIDDEN_PREFIXES = (
    ".github/public",
    ".github/scripts/sync-public-repo.sh",
    ".github/workflows/oss-readiness.yml",
    "apps/cloud",
    "apps/landing",
    "apps/ops",
    "internal/controlplane",
    "internal/instance",
    "internal/operations",
    "internal/enterprise/wire_dev.go",
    "internal/enterprise/wire_ee.go",
    "internal/enterprise/context_ee.go",
    "internal/enterprise/instance_manager_ee.go",
    "internal/enterprise/license_enforcer_ee.go",
    "internal/enterprise/license_monitor_ee.go",
    "examples/certs",
    "services/auth",
    "services/billing",
    "services/cloud",
    "services/cmd",
    "services/email",
    "services/identity",
    "services/operations",
    "services/organization",
    "services/license/internal",
    "services/license/serve",
)

COMMUNITY_DOCS = (
    "README.md",
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
PRIVATE_MARKERS = (
    ("private source repository", "everstacklabs/" + "es-core"),
    ("legacy binary repository", "everstacklabs/" + "releases"),
    ("retired GitHub organization", "midfu" + "sionlabs"),
    ("retired API hostname", "api." + "midfu" + "sion.dev"),
    ("retired local hostname", "." + "midfu" + "sion.local"),
    ("developer Tailnet hostname", "tailb2" + "d4eb" + ".ts" + ".net"),
    ("developer private address", "100.69." + "220.19"),
    # Production single-node address. It reached a firecracker test fixture as
    # "real `ip -4 -o addr show` output from the node"; RFC 5737 documentation
    # addresses exercise the same parser branch without naming our host.
    ("production node address", "37.59." + "98.187"),
    # Any surviving mention of the pre-rename brand. The markers above are
    # written as split literals so this scan does not match this file.
    ("retired brand name", "midfu" + "sion"),
)

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
        if any(name == prefix or name.startswith(prefix + "/") for name in names):
            errors.append(f"private path leaked into public tree: {prefix}")
    for path in paths:
        components = set(path.parts)
        if components & FORBIDDEN_COMPONENTS or path.name in FORBIDDEN_BASENAMES:
            errors.append(f"cache or editor artifact leaked into public tree: {path}")


def validate_private_go_variants(
    root: Path, paths: list[Path], errors: list[str]
) -> None:
    for relative in paths:
        if relative.suffix != ".go":
            continue
        if relative.name.endswith(("_ee.go", "_dev.go")):
            errors.append(f"private Go build variant leaked into public tree: {relative}")
            continue
        path = root / relative
        if path.is_symlink() or not path.is_file():
            continue
        try:
            header = "\n".join(path.read_text(encoding="utf-8").splitlines()[:5])
        except (OSError, UnicodeDecodeError):
            continue
        for line in header.splitlines():
            if line.startswith("//go:build") and POSITIVE_PRIVATE_BUILD_TAG.search(line):
                errors.append(f"private Go build tag leaked into public tree: {relative}")
                break


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
            continue
        text = data.decode("utf-8", errors="ignore")
        for label, pattern in SECRET_PATTERNS:
            if pattern.search(text):
                errors.append(f"possible {label} in public file: {relative}")
        normalized = text.lower()
        for label, marker in PRIVATE_MARKERS:
            if marker in normalized:
                errors.append(f"{label} leaked into public file: {relative}")


def validate_public_plans(root: Path, errors: list[str]) -> None:
    path = root / "pkg/plans/plans.json"
    if not path.is_file():
        return
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"could not parse public plan metadata: {exc}")
        return

    plans = data.get("plans", {})
    for tier, plan in plans.items():
        pricing = plan.get("pricing", {})
        if tier != "free" and any(
            pricing.get(period) != "Custom" for period in ("monthly", "yearly")
        ):
            errors.append(f"public paid plan must use custom pricing: {tier}")
        # The published token allowance is the real entitlement, not a
        # marketing figure: Community Edition ships the same number that
        # enterprise.CEUsageLimits declares, and ce_defaults_test.go keeps the
        # two in step. It is deliberately NOT rewritten to unlimited.

    # The margin applied to platform-key inference is not published. Community
    # Edition is BYOK and never meters that path, so the public tree carries a
    # neutral multiplier.
    credit = data.get("credit_pricing")
    if isinstance(credit, dict):
        markup = credit.get("inference_markup")
        if markup is not None and markup != 1.0:
            errors.append(
                f"public plan metadata must not carry an inference margin: "
                f"inference_markup={markup}"
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
    args = parser.parse_args()

    root = Path(args.root).resolve()
    if not root.is_dir():
        print(f"error: public tree does not exist: {root}", file=sys.stderr)
        return 2

    errors: list[str] = []
    paths = relative_files(root)
    validate_required(root, errors)
    validate_projection_marker(root, errors)
    validate_forbidden(paths, errors)
    validate_private_go_variants(root, paths, errors)
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

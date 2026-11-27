#!/usr/bin/env python3

from __future__ import annotations

import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("validate-public-repo.py")
REQUIRED_FILES = (
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


# The private repository's LICENSING.md, which must never be the published one.
PRIVATE_LICENSING = """# Licensing

<!-- commercial-paths:start -->
- `internal/enterprise/wire_ee.go`
- `services/billing/`
<!-- commercial-paths:end -->

<!-- reserved-paths:start -->
- `apps/landing/`
<!-- reserved-paths:end -->
"""


class PublicRepoValidatorTest(unittest.TestCase):
    def make_tree(self) -> Path:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        for relative in REQUIRED_FILES:
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("# Test\n", encoding="utf-8")
        (root / "README.md").write_text(
            "# Test\n\n[License](./LICENSE)\n", encoding="utf-8"
        )
        (root / ".github/PUBLIC_PROJECTION").write_text(
            "everstack-public-projection-v1\n", encoding="utf-8"
        )
        (root / "LICENSE").write_text(
            "                                 Apache License\n"
            "                           Version 2.0, January 2004\n",
            encoding="utf-8",
        )
        (root / "LICENSING.md").write_text(
            "# Licensing\n\nEverything here is under the [Apache License 2.0](./LICENSE).\n",
            encoding="utf-8",
        )
        (root / "pkg/plans/plans.json").write_text(
            '{"plans": {}, "credit_pricing": {"inference_markup": 1.0}}\n', encoding="utf-8"
        )
        (root / "examples/quickstart/compose.yaml").write_text(
            "build:\n  dockerfile: build/Dockerfile\n"
            "image: pgvector/pgvector:pg16\nhealth: /debug/healthz\n",
            encoding="utf-8",
        )
        (root / "build/Dockerfile").write_text(
            'RUN pnpm --filter @everstack/admin... build\n'
            'RUN go build -tags="ui_embed,ce" .\n',
            encoding="utf-8",
        )
        (root / "build/install.sh").write_text(
            'MIN_SAFE_VERSION="v0.1.25"\n'
            'validate_release_version "$VERSION"\n'
            'RELEASES_REPO="everstacklabs/everstack"\n',
            encoding="utf-8",
        )
        (root / ".github/workflows/release.yml").write_text(
            'on:\n  push:\n    tags: ["v*"]\n'
            'runs-on: ubuntu-latest\n'
            'run: bash build/install.sh --check-version "$GITHUB_REF_NAME"\n'
            'run: go build -tags="ui_embed,ce" .\n'
            'uses: actions/attest-build-provenance@v2\n',
            encoding="utf-8",
        )
        return root

    def private_markers(self, root: Path, *lines: str) -> str:
        path = root.parent / (root.name + ".markers")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.addCleanup(path.unlink)
        return str(path)

    def validate(self, root: Path, *flags: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python3", str(SCRIPT), *flags, str(root)],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_accepts_minimal_public_tree(self) -> None:
        result = self.validate(self.make_tree())
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_modified_projection_marker(self) -> None:
        root = self.make_tree()
        (root / ".github/PUBLIC_PROJECTION").write_text(
            "not-a-managed-projection\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("public projection marker is invalid", result.stderr)

    def test_rejects_private_path(self) -> None:
        root = self.make_tree()
        leaked = root / "infra/helm/values-prod.yaml"
        leaked.parent.mkdir(parents=True)
        leaked.write_text("replicas: 1\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private path leaked", result.stderr)

    def test_rejects_new_private_go_build_variant(self) -> None:
        root = self.make_tree()
        leaked = root / "internal/example/adapter.go"
        leaked.parent.mkdir(parents=True)
        leaked.write_text(
            "//go:build enterprise\n\npackage example\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("enterprise or dev build tag in public tree", result.stderr)

    def test_rejects_private_go_build_tag_below_long_header(self) -> None:
        root = self.make_tree()
        leaked = root / "internal/example/adapter.go"
        leaked.parent.mkdir(parents=True)
        header = "".join(f"// licence line {n}\n" for n in range(12))
        leaked.write_text(
            header + "\n//go:build enterprise\n\npackage example\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("enterprise or dev build tag in public tree", result.stderr)

    def test_ignores_build_tag_text_after_package_clause(self) -> None:
        root = self.make_tree()
        source = root / "internal/example/doc.go"
        source.parent.mkdir(parents=True)
        source.write_text(
            'package example\n\nconst hint = `\n//go:build enterprise\n`\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_private_marker_in_binary_file(self) -> None:
        root = self.make_tree()
        blob = root / "assets/blob.bin"
        blob.parent.mkdir(parents=True, exist_ok=True)
        blob.write_bytes(b"\x00\x01" + ("examplecorp/" + "secret-repo").encode() + b"\x00")
        markers = self.private_markers(root, "private source repository\texamplecorp/" + "secret-repo")

        result = self.validate(root, "--private-markers", markers)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private source repository leaked into public file", result.stderr)

    def test_rejects_private_marker_in_utf16_file(self) -> None:
        root = self.make_tree()
        note = root / "assets/note.txt"
        note.parent.mkdir(parents=True, exist_ok=True)
        note.write_bytes(("see examplecorp/" + "secret-repo").encode("utf-16"))
        markers = self.private_markers(root, "private source repository\texamplecorp/" + "secret-repo")

        result = self.validate(root, "--private-markers", markers)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private source repository leaked into public file", result.stderr)

    def test_rejects_private_marker_in_path(self) -> None:
        root = self.make_tree()
        leaked = root / ("assets/" + "midfu" + "sion-logo.svg")
        leaked.parent.mkdir(parents=True, exist_ok=True)
        leaked.write_text("<svg/>\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("retired brand name leaked into public path", result.stderr)

    def test_rejects_cache_artifact(self) -> None:
        root = self.make_tree()
        artifact = root / "sdk/__pycache__/client.pyc"
        artifact.parent.mkdir(parents=True)
        artifact.write_bytes(b"compiled")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cache or editor artifact", result.stderr)

    def test_ignores_untracked_gitignored_dependency_directory(self) -> None:
        root = self.make_tree()
        (root / ".gitignore").write_text("node_modules/\n", encoding="utf-8")
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        subprocess.run(["git", "-C", str(root), "add", "."], check=True)
        dependency = root / "node_modules/example/index.js"
        dependency.parent.mkdir(parents=True)
        dependency.write_text("local dependency cache\n", encoding="utf-8")

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_force_added_dependency_directory(self) -> None:
        root = self.make_tree()
        (root / ".gitignore").write_text("node_modules/\n", encoding="utf-8")
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        subprocess.run(["git", "-C", str(root), "add", "."], check=True)
        dependency = root / "node_modules/example/index.js"
        dependency.parent.mkdir(parents=True)
        dependency.write_text("tracked dependency cache\n", encoding="utf-8")
        subprocess.run(
            ["git", "-C", str(root), "add", "-f", "node_modules/example/index.js"],
            check=True,
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cache or editor artifact", result.stderr)

    def test_rejects_secret_material(self) -> None:
        root = self.make_tree()
        marker = "-----BEGIN " + "PRIVATE KEY-----"
        (root / "accidental.txt").write_text(
            marker + "\nnot-a-real-key\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("possible private key", result.stderr)

    def test_rejects_broken_local_link(self) -> None:
        root = self.make_tree()
        (root / "README.md").write_text(
            "# Test\n\n[Missing](./does-not-exist.md)\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("broken local link", result.stderr)

    def test_rejects_private_repository_reference(self) -> None:
        root = self.make_tree()
        private_repo = "examplecorp/" + "secret-repo"
        (root / "leak.txt").write_text(private_repo, encoding="utf-8")
        markers = self.private_markers(root, "private source repository\t" + private_repo)

        result = self.validate(root, "--private-markers", markers)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private source repository", result.stderr)

    def test_rejects_legacy_binary_repository_reference(self) -> None:
        root = self.make_tree()
        legacy_repo = "everstacklabs/" + "releases"
        (root / "leak.txt").write_text(legacy_repo, encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("legacy binary repository", result.stderr)

    def test_rejects_ignored_generated_api(self) -> None:
        root = self.make_tree()
        (root / ".gitignore").write_text("**.pb.go\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("hides required generated API artifacts", result.stderr)

    def test_rejects_unsafe_installer_release_floor(self) -> None:
        root = self.make_tree()
        installer = root / "build/install.sh"
        installer.write_text(
            installer.read_text(encoding="utf-8").replace("v0.1.25", "v0.1.24"),
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("installer minimum safe release", result.stderr)

    def test_rejects_non_ce_public_release(self) -> None:
        root = self.make_tree()
        workflow = root / ".github/workflows/release.yml"
        workflow.write_text(
            workflow.read_text(encoding="utf-8").replace(
                "ui_embed,ce", "ui_embed,enterprise"
            ),
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("public release workflow", result.stderr)

    def test_accepts_escaped_mdx_path_placeholder(self) -> None:
        root = self.make_tree()
        page = root / "apps/docs/content/docs/api-reference/example.mdx"
        page.parent.mkdir(parents=True)
        page.write_text(
            '<code className="path">{"/v1/resources/{id}"}</code>\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_unescaped_mdx_path_placeholder(self) -> None:
        root = self.make_tree()
        page = root / "apps/docs/content/docs/api-reference/example.mdx"
        page.parent.mkdir(parents=True)
        page.write_text(
            '<code className="path">/v1/resources/{id}</code>\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unescaped MDX path placeholder", result.stderr)

    def test_rejects_non_reproducible_quickstart(self) -> None:
        root = self.make_tree()
        (root / "examples/quickstart/compose.yaml").write_text(
            "image: private.example/everstack:latest\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("quickstart Compose file", result.stderr)

    def test_accepts_published_paid_plan_prices(self) -> None:
        # Paid prices are the ones on the pricing page; only the inference
        # margin is held back.
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans":{"pro":{"pricing":{"monthly":"$49","yearly":"$499"},'
            '"usage_limits":[{"type":"TOKENS","value":1000000}]}},'
            '"credit_pricing":{"inference_markup":1.0}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_allows_the_real_token_allowance(self) -> None:
        # The published allowance mirrors enterprise.CEUsageLimits rather than
        # being rewritten to unlimited, so a concrete figure must not error.
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans":{"free":{"pricing":{"monthly":"$0","yearly":"$0"},'
            '"usage_limits":[{"type":"TOKENS","value":1000000}]}}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotIn("token", result.stderr.lower())

    def test_rejects_published_inference_margin(self) -> None:
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans":{},"credit_pricing":{"currency":"USD",'
            '"inference_markup":2.0}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("inference_markup = 1.0", result.stderr)

    def test_accepts_neutral_inference_markup(self) -> None:
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans":{},"credit_pricing":{"currency":"USD",'
            '"inference_markup":1.0}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotIn("inference_markup", result.stderr)

    # --- Licensing by path ---------------------------------------------------

    def test_rejects_enterprise_variant_anywhere(self) -> None:
        for relative in ("services/firecracker-agent/meter.go", "internal/example/meter.go"):
            root = self.make_tree()
            source = root / relative
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_text("//go:build enterprise\n\npackage meter\n", encoding="utf-8")

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, relative)
            self.assertIn("enterprise or dev build tag in public tree", result.stderr)

    def test_rejects_ee_and_dev_files_by_name(self) -> None:
        for name in ("feature_ee.go", "feature_dev.go", "feature_ee_test.go"):
            root = self.make_tree()
            source = root / "internal/example" / name
            source.parent.mkdir(parents=True)
            source.write_text("package example\n", encoding="utf-8")

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, name)
            self.assertIn("enterprise or dev Go build variant in public tree", result.stderr)

    def test_rejects_commercial_licence_file(self) -> None:
        root = self.make_tree()
        (root / "LICENSE-COMMERCIAL").write_text("Everstack Commercial License\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private path leaked into public tree: LICENSE-COMMERCIAL", result.stderr)

    def test_rejects_every_commercial_path(self) -> None:
        for relative in (
            "apps/cloud/src/main.tsx",
            "apps/landing/src/page.tsx",
            "internal/api/grpc/controlplane/server.go",
            "internal/controlplane/tenant.go",
            "internal/instance/manager.go",
            "services/README.md",
            "services/auth/server.go",
            "services/billing/meter.go",
            "services/cloud/internal/x.go",
            "services/cmd/main.go",
            "services/config/config.yaml",
            "services/email/email.go",
            "services/identity/package.json",
            "services/license/serve/serve.go",
        ):
            root = self.make_tree()
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("package x\n", encoding="utf-8")

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, relative)
            self.assertIn("private path leaked into public tree", result.stderr, relative)

    def test_rejects_staff_console_and_identity_api_contracts(self) -> None:
        for relative in (
            "proto/everstack/operations/v1/operations.proto",
            "proto/everstack/identity/v1/identity.proto",
            "pkg/grpc/everstack/operations/v1/operations.pb.go",
            "openapi/v1/everstack/identity/v1/identity.swagger.json",
            "packages/proto/es/everstack/operations/v1/operations_pb.js",
            "packages/proto/types/everstack/identity/v1/identity_pb.d.ts",
        ):
            root = self.make_tree()
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("x\n", encoding="utf-8")

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, relative)
            self.assertIn("private path leaked into public tree", result.stderr, relative)

    def test_rejects_founder_outreach_email_files(self) -> None:
        for relative in (
            "internal/email/outreach.go",
            "internal/email/service/outreach.go",
            "internal/email/service/outreach_test.go",
            "internal/email/templates/outreach.go",
        ):
            root = self.make_tree()
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("package email\n", encoding="utf-8")

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, relative)
            self.assertIn("private path leaked into public tree", result.stderr, relative)

    def test_rejects_commercial_directory_licence_file(self) -> None:
        root = self.make_tree()
        stub = root / "internal/example/LICENSE"
        stub.parent.mkdir(parents=True)
        stub.write_text("This directory is under the Everstack Commercial License.\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("commercial licence file in public tree: internal/example/LICENSE", result.stderr)

    def test_rejects_commercial_licence_header_in_go_file(self) -> None:
        root = self.make_tree()
        source = root / "internal/example/feature.go"
        source.parent.mkdir(parents=True)
        source.write_text(
            "// Licensed under the Everstack Commercial License.\n\npackage example\n",
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("commercial licence header in public tree", result.stderr)

    def test_rejects_private_licensing_file(self) -> None:
        root = self.make_tree()
        (root / "LICENSING.md").write_text(PRIVATE_LICENSING, encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("LICENSING.md is the private copy", result.stderr)

    def test_rejects_document_pointing_at_the_commercial_licence(self) -> None:
        for relative in ("README.md", "NOTICE", "CONTRIBUTING.md", "EDITIONS.md"):
            root = self.make_tree()
            (root / relative).write_text(
                "# Test\n\nSee LICENSE-COMMERCIAL for the commercial paths.\n", encoding="utf-8"
            )

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, relative)
            self.assertIn(f"{relative} refers to LICENSE-COMMERCIAL", result.stderr)

    def test_rejects_licence_preamble_in_root_licence(self) -> None:
        root = self.make_tree()
        licence = root / "LICENSE"
        licence.write_text(
            "Portions are under the Everstack Commercial License.\n" + licence.read_text(),
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("LICENSE must", result.stderr)

    def test_draft_licence_flag_is_gone(self) -> None:
        # A dry run and a publish must invoke the validator identically.
        result = self.validate(self.make_tree(), "--allow-draft-licence")

        self.assertEqual(result.returncode, 2)

    def test_allows_named_exception_under_forbidden_prefix(self) -> None:
        root = self.make_tree()
        recipe = root / "infra/images/expand-gzip-sparse.sh"
        recipe.parent.mkdir(parents=True)
        recipe.write_text("#!/bin/sh\n", encoding="utf-8")

        allowed = self.validate(root)
        (root / "infra/images/deploy-prod.sh").write_text("#!/bin/sh\n", encoding="utf-8")
        refused = self.validate(root)

        self.assertEqual(allowed.returncode, 0, allowed.stderr)
        self.assertNotEqual(refused.returncode, 0)
        self.assertIn("private path leaked into public tree: infra", refused.stderr)

    def test_rejects_everything_under_scripts(self) -> None:
        root = self.make_tree()
        generator = root / "scripts/generate-features.go"
        generator.parent.mkdir(parents=True)
        generator.write_text("package main\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("private path leaked into public tree: scripts", result.stderr)

    def test_rejects_unreviewed_workflow(self) -> None:
        root = self.make_tree()
        (root / ".github/workflows/deploy-prod.yml").write_text("on: push\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unreviewed workflow in public tree", result.stderr)

    # --- Address and hostname shapes ---------------------------------------

    def test_rejects_tailnet_address(self) -> None:
        root = self.make_tree()
        (root / "notes.txt").write_text("ssh root@100." + "101.2.3\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("tailnet address leaked into public file: notes.txt:1", result.stderr)

    def test_ignores_address_outside_tailnet_range(self) -> None:
        root = self.make_tree()
        (root / "notes.txt").write_text("version 100.1.2.3 and 192.0.2.10\n", encoding="utf-8")

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_allows_synthetic_tailnet_values_and_ranges(self) -> None:
        root = self.make_tree()
        (root / "guard.go").write_text(
            'var blocked = "100.64.0.0/10"\nconst dns = "100.100.100.100"\nconst probe = "100.64.0.1"\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_allows_synthetic_tenant_hostnames(self) -> None:
        root = self.make_tree()
        (root / "notes.txt").write_text(
            "acme-a1b2c3.eu-gra-1.everstack.ai nobody-000000.dev.eu-gra-1.everstack.ai\n",
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_tailnet_hostname(self) -> None:
        root = self.make_tree()
        (root / "notes.txt").write_text("host: box.tail" + "0000.ts" + ".net\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("tailnet hostname leaked", result.stderr)

    def test_rejects_tenant_hostname(self) -> None:
        root = self.make_tree()
        (root / "notes.txt").write_text(
            "https://acme-prod-" + "7f3e91.dev.eu-gra-1.everstack.ai/v1\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("tenant hostname leaked", result.stderr)

    # --- Apache binaries compile no commercial file -------------------------

    def make_go_module(self, main_imports_commercial: bool) -> Path:
        root = self.make_tree()
        (root / "go.mod").write_text("module example.com/m\n\ngo 1.22\n", encoding="utf-8")
        (root / "services/billing").mkdir(parents=True)
        (root / "services/billing/meter.go").write_text(
            "package billing\n\nfunc Meter() {}\n", encoding="utf-8"
        )
        for binary in ("services/firecracker-agent", "services/fc-supervisor"):
            (root / binary).mkdir(parents=True, exist_ok=True)
            (root / binary / "main.go").write_text("package main\n\nfunc main() {}\n", encoding="utf-8")
        body = (
            'package main\n\nimport "example.com/m/services/billing"\n\nfunc main() { billing.Meter() }\n'
            if main_imports_commercial
            else "package main\n\nfunc main() {}\n"
        )
        (root / "main.go").write_text(body, encoding="utf-8")
        # The fixture's placeholder pb.go is not Go; keep it out of the build.
        (root / "pkg/grpc/everstack/gateway/v1/gateway.pb.go").write_text(
            "package gatewayv1\n", encoding="utf-8"
        )
        (root / "internal/enterprise/wire_ce.go").write_text(
            "//go:build !enterprise\n\npackage enterprise\n", encoding="utf-8"
        )
        return root

    @unittest.skipUnless(shutil.which("go"), "go toolchain not installed")
    def test_rejects_apache_binary_compiling_commercial_package(self) -> None:
        root = self.make_go_module(main_imports_commercial=True)

        result = self.validate(root, "--go-deps-only")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("compiles commercial file services/billing/meter.go", result.stderr)

    @unittest.skipUnless(shutil.which("go"), "go toolchain not installed")
    def test_accepts_apache_binary_without_commercial_imports(self) -> None:
        root = self.make_go_module(main_imports_commercial=False)

        result = self.validate(root, "--go-deps-only")

        self.assertEqual(result.returncode, 0, result.stderr)

    # --- Second review: gaps closed ------------------------------------------

    def test_private_marker_matches_reverse_dns_form(self) -> None:
        root = self.make_tree()
        (root / "zones.go").write_text('var z = "4.3.2.198.in-addr.arpa"\n', encoding="utf-8")
        markers = self.private_markers(root, "production node address\t198.2.3.4")

        result = self.validate(root, "--private-markers", markers)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("production node address (reverse DNS) leaked", result.stderr)

    def test_published_validator_holds_no_private_markers(self) -> None:
        # The validator itself is published; sensitive markers must come from
        # the private file, not from this source.
        source = SCRIPT.read_text(encoding="utf-8")
        start = source.index("PUBLIC_MARKERS = (")
        block = source[start : source.index(")\n", start)]
        for label in ("private source repository", "tailnet", "production node", "test tenant"):
            self.assertNotIn(label, block)

    def test_rejects_agent_state_at_any_depth(self) -> None:
        for relative in (
            "apps/cloud/.claude/settings.local.json",
            "packages/x/CLAUDE.md",
            "apps/admin/.cursorrules",
            "services/cloud/.github/workflows/deploy.yml",
        ):
            with self.subTest(relative=relative):
                root = self.make_tree()
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("x\n", encoding="utf-8")

                result = self.validate(root)

                self.assertNotEqual(result.returncode, 0, relative)

    def test_rejects_tailnet_host_route_and_sentence_end(self) -> None:
        for text in (
            "route 100." + "101.2.3/32\n",
            "reach it at 100." + "101.2.3.\n",
            "zone 3.2.101." + "100.in-addr.arpa\n",
        ):
            with self.subTest(text=text):
                root = self.make_tree()
                (root / "notes.txt").write_text(text, encoding="utf-8")

                result = self.validate(root)

                self.assertNotEqual(result.returncode, 0, text)
                self.assertIn("tailnet address leaked", result.stderr)

    def test_rejects_uppercase_and_other_region_tenant_hosts(self) -> None:
        for host in (
            "ACME-PROD-" + "7F3E91.EU-GRA-1.EVERSTACK.AI",
            "acme-" + "7f3e91.us-pdx-1.everstack.ai",
            "acme-" + "7f3e91.eu.everstack.ai",
        ):
            with self.subTest(host=host):
                root = self.make_tree()
                (root / "notes.txt").write_text(host + "\n", encoding="utf-8")

                result = self.validate(root)

                self.assertNotEqual(result.returncode, 0, host)

    def test_scans_shapes_in_files_with_a_nul_byte(self) -> None:
        root = self.make_tree()
        (root / "notes.txt").write_bytes(b"ssh root@100." + b"101.2.3\x00 trailing text\n")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("tailnet address leaked", result.stderr)

    def test_rejects_long_password_in_connection_url(self) -> None:
        root = self.make_tree()
        (root / "values.yaml").write_text(
            "dsn: postgres://app:" + "Qm9w3hT7xK2pL8vZ" + "@db.internal:5432/app\n",
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("credential in connection URL", result.stderr)

    def test_allows_local_development_connection_url(self) -> None:
        root = self.make_tree()
        (root / "compose.yaml").write_text(
            "dsn: postgres://everstack:everstack-local@postgres:5432/everstack\n"
            "dsn2: postgres://user:${POSTGRES_PASSWORD_VALUE}@db:5432/app\n",
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_missing_inference_markup(self) -> None:
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans": {}, "credit": {"markup": 2.0}}\n', encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("inference_markup = 1.0", result.stderr)

    def test_rejects_unclassified_service_directory(self) -> None:
        root = self.make_tree()
        source = root / "services/newthing/main.go"
        source.parent.mkdir(parents=True)
        source.write_text("package main\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not on the Community Edition list: services/newthing/", result.stderr)

    def test_rejects_loose_file_directly_under_services_or_apps(self) -> None:
        for relative in ("services/README.md", "apps/notes.txt", "services/deploy.sh"):
            root = self.make_tree()
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("# Notes\n", encoding="utf-8")

            result = self.validate(root)

            self.assertNotEqual(result.returncode, 0, relative)
            self.assertIn(f"unreviewed file directly under {relative.split('/')[0]}/", result.stderr)

    def test_rejects_old_style_enterprise_build_tag(self) -> None:
        root = self.make_tree()
        source = root / "internal/example/adapter.go"
        source.parent.mkdir(parents=True)
        source.write_text("// +build enterprise\n\npackage example\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("enterprise or dev build tag in public tree", result.stderr)

    def test_rejects_secrets_in_any_public_workflow(self) -> None:
        root = self.make_tree()
        (root / ".github/workflows/ci.yml").write_text(
            "env:\n  TOKEN: ${{ secrets.DEPLOY_TOKEN }}\n", encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("public workflow uses a repository secret", result.stderr)


    # --- Delta review: gaps closed -------------------------------------------

    def test_rejects_empty_private_markers_file(self) -> None:
        root = self.make_tree()
        markers = self.private_markers(root, "# only a comment")

        result = self.validate(root, "--private-markers", markers)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("holds no markers", result.stderr)

    def test_rejects_private_marker_line_with_extra_column(self) -> None:
        root = self.make_tree()
        markers = self.private_markers(root, "label\tvalue\textra", "ok\tmarker")

        result = self.validate(root, "--private-markers", markers)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exactly label<TAB>marker", result.stderr)

    def test_tailnet_host_with_prefix_is_rejected_but_network_is_allowed(self) -> None:
        root = self.make_tree()
        (root / "net.txt").write_text("range 100.64.0.0/10 and 100.96.0.0/11\n", encoding="utf-8")
        allowed = self.validate(root)
        (root / "host.txt").write_text("host 100." + "101.2.3/24\n", encoding="utf-8")
        refused = self.validate(root)

        self.assertEqual(allowed.returncode, 0, allowed.stderr)
        self.assertNotEqual(refused.returncode, 0)
        self.assertIn("tailnet address leaked into public file: host.txt", refused.stderr)

    def test_rejects_connection_url_forms(self) -> None:
        # A generated-looking value assembled at run time, so this source holds
        # no literal a secret scanner would flag.
        sample = "".join(chr(c) for c in (81, 109, 57, 119, 51, 104, 84, 55, 120, 75, 50, 112, 76, 56, 118, 90)) + "secret"
        for url in (
            "redis://:" + sample + "@cache:6379/0",
            "postgresql+psycopg://app:" + sample + "@db:5432/app",
        ):
            with self.subTest(url=url):
                root = self.make_tree()
                (root / "values.yaml").write_text("dsn: " + url + "\n", encoding="utf-8")

                result = self.validate(root)

                self.assertNotEqual(result.returncode, 0, url)
                self.assertIn("credential in connection URL", result.stderr)

    def test_rejects_workflow_secret_and_runner_forms(self) -> None:
        for text in (
            "env:\n  T: ${{ secrets['DEPLOY'] }}\n",
            "jobs:\n  x:\n    secrets: inherit\n",
            "jobs:\n  x:\n    runs-on: [build-box]\n",
            "on:\n  issues:\n    types: [opened]\n",
        ):
            with self.subTest(text=text):
                root = self.make_tree()
                (root / ".github/workflows/ci.yml").write_text(text, encoding="utf-8")

                result = self.validate(root)

                self.assertNotEqual(result.returncode, 0, text)

    def test_rejects_boolean_markup(self) -> None:
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans": {}, "credit_pricing": {"inference_markup": true}}\n', encoding="utf-8"
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("inference_markup = 1.0", result.stderr)

    def test_rejects_nested_agent_instruction_files(self) -> None:
        for relative in ("packages/x/AGENTS.md", "apps/cloud/claude.local.md", "services/billing/.mcp.json", "apps/admin/.codex/config.toml"):
            with self.subTest(relative=relative):
                root = self.make_tree()
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("x\n", encoding="utf-8")

                result = self.validate(root)

                self.assertNotEqual(result.returncode, 0, relative)

    def test_rejects_root_agents_file(self) -> None:
        # The root AGENTS.md describes the private repository's layout.
        root = self.make_tree()
        (root / "AGENTS.md").write_text("# Contributing with agents\n", encoding="utf-8")

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("agent or editor instructions leaked into public tree: AGENTS.md", result.stderr)


if __name__ == "__main__":
    unittest.main()

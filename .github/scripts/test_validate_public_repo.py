#!/usr/bin/env python3

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("validate-public-repo.py")
REQUIRED_FILES = (
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
        (root / "pkg/plans/plans.json").write_text(
            '{"plans": {}}\n', encoding="utf-8"
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

    def validate(self, root: Path) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["python3", str(SCRIPT), str(root)],
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
        leaked = root / "services/billing/private.go"
        leaked.parent.mkdir(parents=True)
        leaked.write_text("package billing\n", encoding="utf-8")

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
        self.assertIn("private Go build tag leaked", result.stderr)

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
        private_repo = "everstacklabs/" + "es-core"
        (root / "leak.txt").write_text(private_repo, encoding="utf-8")

        result = self.validate(root)

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

    def test_rejects_paid_plan_list_pricing(self) -> None:
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans":{"pro":{"pricing":{"monthly":"$49","yearly":"$499"},'
            '"usage_limits":[{"type":"TOKENS","value":1000000}]}}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("custom pricing", result.stderr)

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
            '"inference_markup":1.4}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("inference margin", result.stderr)

    def test_accepts_neutral_inference_markup(self) -> None:
        root = self.make_tree()
        (root / "pkg/plans/plans.json").write_text(
            '{"plans":{},"credit_pricing":{"currency":"USD",'
            '"inference_markup":1.0}}\n',
            encoding="utf-8",
        )

        result = self.validate(root)

        self.assertNotIn("inference margin", result.stderr)


if __name__ == "__main__":
    unittest.main()

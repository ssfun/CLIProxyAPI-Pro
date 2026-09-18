import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github" / "workflows"


class ReleaseWorkflowArtifactTests(unittest.TestCase):
    def run_publish_guard(
        self,
        mode: str,
        *,
        env_overrides=None,
        response_overrides=None,
    ) -> subprocess.CompletedProcess[str]:
        responses = {
            "api repos/ssfun/CLIProxyAPI-Pro/commits/main --jq .sha": "custom-new",
            "release view --repo ssfun/CLIProxyAPI-Pro --json tagName --jq .tagName": "v7.3.7-pro",
            "release view --repo router-for-me/Cli-Proxy-API-Management-Center --json tagName --jq .tagName": "v2.1.0",
            "api repos/router-for-me/Cli-Proxy-API-Management-Center/commits/v2.1.0 --jq .sha": "management-new",
            "release view --repo router-for-me/CLIProxyAPI --json tagName --jq .tagName": "v7.3.7",
            "api repos/router-for-me/CLIProxyAPI/commits/v7.3.7 --jq .sha": "core-new",
            "api repos/router-for-me/models/commits/main --jq .sha": "models-new",
            "release view v7.3.7-pro --repo ssfun/CLIProxyAPI-Pro --json body --jq .body // \"\"": (
                "- Core upstream release: v7.3.7\n"
                "- Management upstream release: v2.0.0\n"
                "- Management upstream commit: management-old\n"
                "- Models data commit: models-old\n"
                "- Customization commit: custom-old\n"
            ),
            "api repos/ssfun/CLIProxyAPI-Pro/compare/custom-old...custom-new --jq .status": "ahead",
            "api repos/router-for-me/Cli-Proxy-API-Management-Center/compare/management-old...management-new --jq .status": "ahead",
            "api repos/router-for-me/models/compare/models-old...models-new --jq .status": "ahead",
        }
        if response_overrides:
            responses.update(response_overrides)

        env = os.environ.copy()
        env.update(
            {
                "RELEASE_PUBLISH_MODE": mode,
                "ALLOW_STALE_PUBLISH": "false",
                "CURRENT_REPO": "ssfun/CLIProxyAPI-Pro",
                "CUSTOMIZATION_SHA": "custom-new",
                "MANAGEMENT_UPSTREAM_REPO": "router-for-me/Cli-Proxy-API-Management-Center",
                "TARGET_RELEASE_TAG": "v7.3.7-pro",
                "TARGET_MANAGEMENT_TAG": "v2.1.0",
                "TARGET_MANAGEMENT_SHA": "management-new",
                "CORE_UPSTREAM_REPO": "router-for-me/CLIProxyAPI",
                "MODELS_REPO": "router-for-me/models",
                "TARGET_CORE_TAG": "v7.3.7",
                "TARGET_CORE_SHA": "core-new",
                "TARGET_MODELS_SHA": "models-new",
                "MOCK_GH_RESPONSES": json.dumps(responses),
            }
        )
        if env_overrides:
            env.update(env_overrides)

        with tempfile.TemporaryDirectory() as temporary_directory:
            mock_gh = Path(temporary_directory) / "gh"
            mock_gh.write_text(
                "#!/usr/bin/env python3\n"
                "import json, os, sys\n"
                "responses = json.loads(os.environ['MOCK_GH_RESPONSES'])\n"
                "key = ' '.join(sys.argv[1:])\n"
                "if key not in responses:\n"
                "    print(f'unexpected gh invocation: {key}', file=sys.stderr)\n"
                "    raise SystemExit(1)\n"
                "print(responses[key])\n"
            )
            mock_gh.chmod(0o755)
            env["PATH"] = f"{temporary_directory}:{env['PATH']}"
            return subprocess.run(
                ["bash", str(ROOT / "scripts" / "validation" / "release_publish_guard.sh")],
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )

    def test_core_release_validates_the_pinned_release_models_data(self) -> None:
        workflow = (WORKFLOWS / "release-core.yml").read_text()
        core_validation = (ROOT / "scripts" / "validation" / "core.sh").read_text()

        validate_job_start = workflow.index("  validate-core:\n")
        validate_job_end = workflow.index("  validate-management:\n")
        validate_job = workflow[validate_job_start:validate_job_end]
        build_job_start = workflow.index("  build-core-other:\n")
        build_job_end = workflow.index("  assemble-core-assets:\n")
        build_job = workflow[build_job_start:build_job_end]

        self.assertIn("repository: router-for-me/models", validate_job)
        self.assertIn(
            "ref: ${{ needs.check-version.outputs.models_sha }}", validate_job
        )
        self.assertIn("path: upstream-models", validate_job)
        self.assertIn(
            'test "$(git -C upstream-models rev-parse HEAD)" = "${MODELS_SHA}"',
            validate_job,
        )
        self.assertIn("upstream-models/models.json", validate_job)

        validation_models_install = core_validation.index(
            'cp "${release_models_file}" '
            '"${upstream_root}/internal/registry/models/models.json"'
        )
        validation_customization_apply = core_validation.index(
            'python3 "${repo_root}/cliproxyapi-pro-core/patches/'
            'apply_upstream_patches.py"'
        )
        self.assertLess(validation_models_install, validation_customization_apply)

        models_install = build_job.index(
            "git -C upstream-core show FETCH_HEAD:models.json > "
            "upstream-core/internal/registry/models/models.json"
        )
        customization_apply = build_job.index(
            "python customizations-repo/cliproxyapi-pro-core/patches/"
            "apply_upstream_patches.py"
        )
        self.assertLess(models_install, customization_apply)

    def test_core_release_parallelizes_validation_builds_and_gates_publication(self) -> None:
        workflow = (WORKFLOWS / "release-core.yml").read_text()

        other_start = workflow.index("  build-core-other:\n")
        linux_start = workflow.index("  build-core-linux:\n")
        assemble_start = workflow.index("  assemble-core-assets:\n")
        gate_start = workflow.index("  release-gate:\n")
        publish_start = workflow.index("  publish-release:\n")

        other_job = workflow[other_start:linux_start]
        linux_job = workflow[linux_start:assemble_start]
        assemble_job = workflow[assemble_start:gate_start]
        gate_job = workflow[gate_start:publish_start]
        publish_job = workflow[publish_start:]

        for job in (other_job, linux_job):
            self.assertIn("- validate-repository", job)
            self.assertNotIn("- validate-core", job)
        self.assertIn("runner: ubuntu-latest\n            goos: windows\n            goarch: arm64", other_job)
        self.assertIn("Cache manylinux Go toolchain archive", linux_job)
        self.assertIn("GOMODCACHE=/go/pkg/mod", linux_job)
        self.assertIn("GOCACHE=/root/.cache/go-build", linux_job)

        self.assertIn("- build-core-linux", assemble_job)
        self.assertIn("- build-core-other", assemble_job)
        self.assertIn('Expected %s core archives, found %s', assemble_job)
        self.assertIn('"CLIProxyAPI_${version}_freebsd_aarch64_no-plugin.tar.gz"', assemble_job)

        self.assertIn("- validate-core", gate_job)
        self.assertIn("- build-image-candidate", gate_job)
        self.assertIn("IMAGE_DIGEST:", gate_job)
        self.assertIn("needs.release-gate.result == 'success'", publish_job)
        self.assertIn("docker buildx imagetools create", publish_job)
        self.assertIn("@${IMAGE_DIGEST}", publish_job)
        self.assertIn(
            "awk '$1 == \"Digest:\" && digest == \"\" { digest=$2 } END { print digest }'",
            publish_job,
        )
        self.assertNotIn('awk \'$1 == "Digest:" { print $2; exit }\'', publish_job)

    def test_runtime_image_is_built_early_without_publishing_official_tags(self) -> None:
        workflow = (WORKFLOWS / "release-core.yml").read_text()
        candidate_start = workflow.index("  build-image-candidate:\n")
        other_start = workflow.index("  build-core-other:\n")
        candidate_job = workflow[candidate_start:other_start]

        self.assertIn("- build-core-linux", candidate_job)
        self.assertNotIn("- build-core-other", candidate_job)
        self.assertIn("candidate-${{ github.run_id }}-${{ github.run_attempt }}", candidate_job)
        self.assertIn("cache-from: type=gha,scope=release-runtime", candidate_job)
        self.assertIn("cache-to: type=gha,scope=release-runtime,mode=min", candidate_job)
        self.assertNotIn("cliproxyapi-pro:latest", candidate_job)
        self.assertNotIn("needs.check-version.outputs.release_tag }}", candidate_job.split("tags:", 1)[1])

    def test_publish_jobs_share_a_mutex_and_stale_run_guard(self) -> None:
        core_workflow = (WORKFLOWS / "release-core.yml").read_text()
        management_workflow = (WORKFLOWS / "release-management.yml").read_text()

        core_publish = core_workflow[core_workflow.index("  publish-release:\n") :]
        management_publish = management_workflow[
            management_workflow.index("  publish-management-asset:\n") :
        ]

        for workflow, publish_job, mode in (
            (core_workflow, core_publish, "core"),
            (management_workflow, management_publish, "management"),
        ):
            self.assertIn("allow_stale_publish:", workflow)
            self.assertIn("group: cliproxyapi-pro-release-publish", publish_job)
            self.assertIn("cancel-in-progress: false", publish_job)
            self.assertIn("Reject stale", publish_job)
            self.assertIn(f"RELEASE_PUBLISH_MODE: {mode}", publish_job)
            self.assertIn(
                "bash customizations-repo/scripts/validation/release_publish_guard.sh",
                publish_job,
            )
            self.assertNotIn("github.workflow", publish_job.split("permissions:", 1)[0])

        guard = (
            ROOT / "scripts" / "validation" / "release_publish_guard.sh"
        ).read_text()
        self.assertIn("commits/main", guard)
        self.assertIn("compare/${published_sha}...${target_sha}", guard)
        self.assertIn("Published release ${latest_release_tag} is newer", guard)
        self.assertIn("Target release ${target_release_tag} is no longer latest", guard)

    def test_publish_guard_accepts_fresh_core_and_management_snapshots(self) -> None:
        for mode in ("core", "management"):
            result = self.run_publish_guard(mode)
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertIn("freshness checks passed", result.stdout)

    def test_publish_guard_rejects_stale_customization_and_release(self) -> None:
        stale_customization = self.run_publish_guard(
            "core",
            response_overrides={
                "api repos/ssfun/CLIProxyAPI-Pro/commits/main --jq .sha": "custom-newer"
            },
        )
        self.assertNotEqual(0, stale_customization.returncode)
        self.assertIn("is no longer main", stale_customization.stderr)

        stale_release = self.run_publish_guard(
            "management",
            response_overrides={
                "release view --repo ssfun/CLIProxyAPI-Pro --json tagName --jq .tagName": "v7.3.8-pro"
            },
        )
        self.assertNotEqual(0, stale_release.returncode)
        self.assertIn("is no longer latest", stale_release.stderr)

    def test_publish_guard_rejects_diverged_snapshot_and_allows_override(self) -> None:
        diverged = self.run_publish_guard(
            "core",
            response_overrides={
                "api repos/ssfun/CLIProxyAPI-Pro/compare/custom-old...custom-new --jq .status": "diverged"
            },
        )
        self.assertNotEqual(0, diverged.returncode)
        self.assertIn("compare status 'diverged'", diverged.stderr)

        bypassed = self.run_publish_guard(
            "core",
            env_overrides={"ALLOW_STALE_PUBLISH": "true"},
            response_overrides={},
        )
        self.assertEqual(0, bypassed.returncode, bypassed.stderr)
        self.assertIn("explicitly bypassed", bypassed.stdout)

    def test_publish_guard_rejects_management_rollback_or_retag(self) -> None:
        newer_management = self.run_publish_guard(
            "management",
            response_overrides={
                "release view v7.3.7-pro --repo ssfun/CLIProxyAPI-Pro --json body --jq .body // \"\"": (
                    "- Management upstream release: v2.2.0\n"
                    "- Management upstream commit: management-future\n"
                    "- Customization commit: custom-old\n"
                )
            },
        )
        self.assertNotEqual(0, newer_management.returncode)
        self.assertIn("is newer than target", newer_management.stderr)

        retagged_management = self.run_publish_guard(
            "management",
            response_overrides={
                "release view v7.3.7-pro --repo ssfun/CLIProxyAPI-Pro --json body --jq .body // \"\"": (
                    "- Management upstream release: v2.1.0\n"
                    "- Management upstream commit: management-old\n"
                    "- Customization commit: custom-old\n"
                ),
                "api repos/router-for-me/Cli-Proxy-API-Management-Center/compare/management-old...management-new --jq .status": "diverged",
            },
        )
        self.assertNotEqual(0, retagged_management.returncode)
        self.assertIn("Management snapshot", retagged_management.stderr)

    def test_release_tail_runs_optional_work_in_parallel_and_moves_cleanup_out(self) -> None:
        core_workflow = (WORKFLOWS / "release-core.yml").read_text()
        management_workflow = (WORKFLOWS / "release-management.yml").read_text()
        cleanup_workflow = (WORKFLOWS / "cleanup-runs.yml").read_text()

        deploy_start = core_workflow.index("  trigger-render-deployment:\n")
        deploy_job = core_workflow[deploy_start:]

        self.assertIn("- backup-pro-data", deploy_job.split("    steps:\n", 1)[0])
        self.assertIn("- name: Trigger Render Deployment", deploy_job)
        self.assertIn("- name: Send Telegram notification", deploy_job)
        self.assertNotIn("  send-telegram-notification:\n", core_workflow)
        self.assertNotIn("  cleanup-runs:\n", core_workflow)
        self.assertNotIn("  cleanup-runs:\n", management_workflow)
        self.assertIn("  workflow_dispatch:\n", cleanup_workflow)
        self.assertIn("  schedule:\n", cleanup_workflow)
        self.assertIn("  cleanup-runs:\n", cleanup_workflow)
        self.assertIn("Mattraks/delete-workflow-runs@", cleanup_workflow)

    def test_core_release_reuses_validated_management_asset(self) -> None:
        workflow = (WORKFLOWS / "release-core.yml").read_text()

        self.assertNotIn("  build-management-html:\n", workflow)
        self.assertNotIn("run: bun run build", workflow)
        self.assertEqual(
            1,
            workflow.count(
                "run: bash customizations-repo/scripts/validation/management.sh "
                "upstream-management"
            ),
        )
        self.assertIn("- name: Upload validated management asset", workflow)
        self.assertIn("path: upstream-management/dist/management.html", workflow)
        self.assertEqual(3, workflow.count("name: management-release-asset"))

    def test_management_release_reuses_validated_management_asset(self) -> None:
        workflow = (WORKFLOWS / "release-management.yml").read_text()

        self.assertNotIn("run: bun run build", workflow)
        self.assertEqual(
            1,
            workflow.count(
                "run: bash customizations-repo/scripts/validation/management.sh upstream"
            ),
        )
        self.assertIn("- name: Upload validated management asset", workflow)
        self.assertIn("- name: Download validated management asset", workflow)
        self.assertIn("  publish-management-asset:\n", workflow)
        self.assertNotIn("  build-and-release:\n", workflow)
        self.assertIn("path: upstream/dist/management.html", workflow)
        self.assertIn(
            "release-assets/management/management.html", workflow
        )
        self.assertEqual(2, workflow.count("name: management-release-asset"))

    def test_release_backup_uses_the_tracked_data_management_endpoint(self) -> None:
        workflow = (WORKFLOWS / "release-core.yml").read_text()
        core_patcher = (
            ROOT / "cliproxyapi-pro-core" / "patches" / "apply_upstream_patches.py"
        ).read_text()
        backup_job_start = workflow.index("  backup-pro-data:\n")
        backup_job_end = workflow.index("  trigger-render-deployment:\n")
        backup_job = workflow[backup_job_start:backup_job_end]

        self.assertIn(
            "/v0/management/data/backups/now", backup_job
        )
        self.assertIn(
            'embeddedusage.RegisterDataManagementGinRoutes(mgmt.Group("/data"))',
            core_patcher,
        )
        self.assertNotIn("/v0/management/usage/data/", backup_job)
        self.assertIn('elif [ "$http_status" = "404" ]; then', backup_job)
        self.assertIn("/v0/management/usage/export", backup_job)
        self.assertIn("Legacy backup fallback", backup_job)
        self.assertIn("PROPFIND", backup_job)
        self.assertIn("webdav_url", backup_job)
        self.assertIn("webdav_password", backup_job)


if __name__ == "__main__":
    unittest.main()

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


class ValidateWorkflowTests(unittest.TestCase):
    def test_core_candidate_runs_before_conditional_clean_baseline(self) -> None:
        script = (ROOT / "scripts" / "validation" / "core.sh").read_text()
        candidate = script.index('run_timed "candidate tests"')
        condition = script.index('if [[ "${candidate_status}" -eq 0 ]]')
        baseline = script.index('run_timed "clean upstream baseline tests"')
        self.assertLess(candidate, condition)
        self.assertLess(condition, baseline)
        self.assertIn("worktree add --detach", script)
        self.assertIn("Clean upstream baseline could not be executed reliably", script)

    def test_validate_routes_cumulative_changes_and_records_state(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "ci.yml").read_text()
        self.assertIn("fetch-depth: 0", workflow)
        self.assertIn('${BASE_SHA}...${HEAD_SHA}', workflow)
        self.assertIn('${previous_head_sha}..${HEAD_SHA}', workflow)
        self.assertIn("validation-summary:", workflow)
        self.assertIn("record-validation-state:", workflow)
        self.assertIn("name: validation-state", workflow)
        self.assertIn(
            "cancel-in-progress: ${{ github.event_name != 'workflow_dispatch' }}",
            workflow,
        )

    def test_validate_uses_separate_rotating_go_caches(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "ci.yml").read_text()
        self.assertIn("cache: false", workflow)
        self.assertIn("Restore Go module cache", workflow)
        self.assertIn("Restore rotating Go build cache", workflow)
        self.assertIn("Restore Staticcheck analysis cache", workflow)
        self.assertIn("actions/cache/restore@", workflow)
        self.assertIn("Save Go build cache after validation", workflow)
        self.assertIn("Save Staticcheck analysis cache after validation", workflow)
        self.assertIn("actions/cache/save@", workflow)
        self.assertIn("github.event_name == 'push'", workflow)
        self.assertIn("github.ref == 'refs/heads/main'", workflow)
        self.assertIn("steps.validate_core.outcome == 'failure'", workflow)
        self.assertIn("steps.cache_go_build.outputs.cache-primary-key", workflow)
        self.assertIn("steps.cache_staticcheck.outputs.cache-primary-key", workflow)
        self.assertIn("GOMODCACHE=${RUNNER_TEMP}/go-mod-cache", workflow)
        self.assertIn("GOCACHE=${RUNNER_TEMP}/go-build-cache", workflow)
        self.assertIn("STATICCHECK_CACHE=${RUNNER_TEMP}/staticcheck-cache", workflow)
        self.assertIn("${{ runner.temp }}/go-mod-cache", workflow)
        self.assertIn("${{ runner.temp }}/go-build-cache", workflow)
        self.assertIn("${{ runner.temp }}/staticcheck-cache", workflow)

    def test_core_validation_propagates_staticcheck_failures(self) -> None:
        script = (ROOT / "scripts" / "validation" / "core.sh").read_text()
        self.assertIn(
            "staticcheck -checks=SA4011 ./internal/api/handlers/management || exit",
            script,
        )
        self.assertIn("staticcheck -checks=U1000 ./internal/pro/... || exit", script)
        self.assertIn('run_timed "staticcheck SA4011"', script)
        self.assertIn('run_timed "staticcheck U1000"', script)
        self.assertIn('run_timed "go vet"', script)

    def test_core_validation_reduces_groups_without_merging_race_sensitive_packages(self) -> None:
        script = (ROOT / "scripts" / "validation" / "core.sh").read_text()
        self.assertIn("packages+=(./internal/pro/... ./internal/embeddedusage/...)", script)
        self.assertIn("patch-and-pro-packages", script)
        self.assertIn("runtime-executor ./internal/runtime/executor", script)
        self.assertIn("sdk-cliproxy ./sdk/cliproxy", script)

    def test_core_validation_applies_timeout_fixture_to_every_test_root(self) -> None:
        script = (ROOT / "scripts" / "validation" / "core.sh").read_text()
        fixture = (
            ROOT
            / "scripts"
            / "validation"
            / "fixtures"
            / "antigravity_models_timeout_cleanup.patch"
        ).read_text()
        function_start = script.index("run_upstream_test_groups()")
        function_end = script.index("prepare_baseline_worktree()")
        self.assertIn(
            'apply_validation_fixture_adjustments "${source_root}"',
            script[function_start:function_end],
        )
        self.assertIn("stopSlowHandler", fixture)
        self.assertEqual(2, fixture.count("close(stopSlowHandler)"))
        self.assertEqual(4, fixture.count("resetAntigravityCapabilityCache"))
        self.assertIn("antigravity_models_timeout_cleanup codex_live_media_loopback", script)
        media_fixture = (ROOT / "scripts/validation/fixtures/codex_live_media_loopback.patch").read_text()
        self.assertIn("newTestLoopbackWebRTCAPI", media_fixture)
        self.assertIn("offerCandidatesAreLoopback(t, sdp)", media_fixture)
        # Setup is isolated; the real audio/data transfer assertions are not removed.
        removed = [line for line in media_fixture.splitlines() if line.startswith("-") and not line.startswith("---")]
        self.assertTrue(all("newTestWebRTCAPI(t)" in line for line in removed))


    def test_source_image_uses_buildx_cache_and_pinned_management_asset(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "ci.yml").read_text()
        dockerfile = (ROOT / "cliproxyapi-pro-core" / "Dockerfile").read_text()
        self.assertIn("docker/setup-buildx-action@", workflow)
        self.assertIn("docker/build-push-action@", workflow)
        self.assertIn("cache-from: type=gha,scope=validate-source-image", workflow)
        self.assertIn("load: false", workflow)
        self.assertIn(
            "cache-to: type=gha,scope=validate-source-image,mode=max,timeout=60s,ignore-error=true",
            workflow,
        )
        self.assertIn("PRO_MANAGEMENT_VERSION=", workflow)
        self.assertIn("PRO_MANAGEMENT_DIGEST=", workflow)
        self.assertIn("releases/${management_release_endpoint}", dockerfile)
        self.assertIn("sha256sum -c -", dockerfile)
        self.assertIn("AS management", dockerfile)
        self.assertIn("COPY --from=management /tmp/pro-management.html", dockerfile)
        self.assertLess(dockerfile.index("AS management"), dockerfile.index("AS builder"))
        self.assertGreater(
            dockerfile.index('ARG SOURCE_DATE_EPOCH=""'),
            dockerfile.index("RUN python3 /tmp/patches/apply_upstream_patches.py"),
        )

    def test_core_diagnostics_are_uploaded_even_on_failure(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "ci.yml").read_text()
        release_workflow = (
            ROOT / ".github" / "workflows" / "release-core.yml"
        ).read_text()
        self.assertIn("VALIDATION_ARTIFACT_DIR:", workflow)
        self.assertIn("name: core-validation-diagnostics", workflow)
        self.assertIn("if: always()", workflow)
        self.assertIn("name: core-release-validation-diagnostics", release_workflow)


if __name__ == "__main__":
    unittest.main()

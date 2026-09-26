from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]
OPENAPI = (
    ROOT / "scripts/validation/contracts/api-key-policy-openapi.yaml"
).read_text()
GO_TYPES = (
    ROOT
    / "cliproxyapi-pro-core/patches/sources/internal/pro/apikeypolicy/types.go"
).read_text()
GO_HANDLER = (
    ROOT
    / "cliproxyapi-pro-core/patches/sources/internal/api/handlers/management/api_key_policy.go"
).read_text()
TS_CLIENT = (
    ROOT
    / "cliproxyapi-pro-management/overlay/src/pro/modules/apiKeyPolicy/apiKeyPolicy.ts"
).read_text()
CORE_PATCHER = (
    ROOT / "cliproxyapi-pro-core/patches/apply_upstream_patches.py"
).read_text()


class APIKeyPolicyContractTest(unittest.TestCase):
    def test_policy_catalog_exposes_provider_model_relationships(self) -> None:
        self.assertIn("/v0/management/api-key-policy-catalog:", OPENAPI)
        self.assertRegex(
            OPENAPI,
            r"PolicyCatalog:\n(?:.|\n)*?required: \[providers, models, modelProviders\]",
        )
        self.assertIn("ModelProviders map[string][]string `json:\"modelProviders\"`", GO_TYPES)
        self.assertIn('"provider_model_linkage"', GO_HANDLER)

    def test_runtime_smoke_helpers_are_syntax_valid(self) -> None:
        for relative in (
            "scripts/validation/api_key_policy_runtime_smoke.py",
            "scripts/validation/run_api_key_policy_binary_smoke.py",
        ):
            compile((ROOT / relative).read_text(), relative, "exec")

    def test_management_states_match_go_typescript_and_openapi(self) -> None:
        expected = {"unconfigured", "configured", "orphaned", "unavailable"}
        go_states = set(re.findall(r'State\w+\s+=\s+"([^"]+)"', GO_TYPES))
        ts_match = re.search(
            r"export type APIKeyPolicyState = ([^;]+);", TS_CLIENT
        )
        self.assertIsNotNone(ts_match)
        ts_states = set(re.findall(r"'([^']+)'", ts_match.group(1)))
        openapi_match = re.search(
            r"PolicyState:\n\s+type: string\n\s+enum: \[([^\]]+)\]", OPENAPI
        )
        self.assertIsNotNone(openapi_match)
        openapi_states = {
            value.strip() for value in openapi_match.group(1).split(",")
        }
        self.assertEqual(expected, go_states)
        self.assertEqual(expected, ts_states)
        self.assertEqual(expected, openapi_states)

    def test_binding_generation_and_delete_preview_exist_across_contracts(self) -> None:
        for source in (GO_HANDLER, TS_CLIENT, OPENAPI):
            self.assertIn("configGeneration", source)
            self.assertIn("delete-preview", source)
        self.assertIn("api_key_policy_config_changed", GO_HANDLER)
        self.assertIn("api_key_policy_config_changed", TS_CLIENT)
        for source in (GO_HANDLER, TS_CLIENT, OPENAPI):
            self.assertIn("orphaned_purge_guard", source)
        self.assertIn("configGeneration },", TS_CLIENT)
        self.assertRegex(
            OPENAPI,
            r"permanentlyDeleteOrphanedApiKeyPolicy(?:.|\n)*?required: \[version, configGeneration\]",
        )
        self.assertRegex(
            OPENAPI,
            r"BindingPage:\n(?:.|\n)*?required: \[items, orphaned, nextCursor, configGeneration\]",
        )

    def test_usage_target_is_explicit_and_feature_gated_across_contracts(self) -> None:
        for source in (GO_HANDLER, TS_CLIENT, OPENAPI):
            self.assertIn("api-key-policy-usage-target", source)
            self.assertIn("usage_key_target", source)
            self.assertIn("apiKeyHash", source)
        self.assertNotIn('json:"apiKeyHash"', GO_HANDLER.split("type apiKeyPolicyBinding struct", 1)[1].split("}", 1)[0])

    def test_lightweight_profile_catalog_is_generation_bound_across_contracts(self) -> None:
        for source in (GO_HANDLER, TS_CLIENT, OPENAPI):
            self.assertIn("api-key-policy-profile-catalog", source)
            self.assertIn("policyGeneration", source)
        self.assertIn("ListProfileCatalog", GO_HANDLER)
        self.assertIn("ProfileCatalogSnapshot", GO_TYPES)
        self.assertRegex(
            OPENAPI,
            r"ProfileCatalogItem:\n(?:.|\n)*?additionalProperties: false(?:.|\n)*?required: \[id, name, updatedAtMs\]",
        )

    def test_quota_overview_endpoint_and_states_match_across_contracts(self) -> None:
        for source in (GO_HANDLER, TS_CLIENT, OPENAPI):
            self.assertIn("api-key-policy-quota-summaries", source)
            self.assertIn("key_quota_overview", source)
        self.assertIn("ListQuotaSummaries", GO_HANDLER)
        self.assertIn("APIKeyQuotaSummaryResponse", TS_CLIENT)
        self.assertRegex(
            OPENAPI,
            r"QuotaSummaryResponse:\n(?:.|\n)*?required: \[items, snapshotAtMs\]",
        )
        self.assertRegex(
            OPENAPI,
            r"QuotaSummary:\n(?:.|\n)*?required: \[policyId, policyVersion, admissionState\]",
        )

        expected_states = {"available", "disabled", "exhausted", "blocked"}
        go_states = set(re.findall(r'QuotaAdmission\w+\s+=\s+"([^"]+)"', GO_TYPES))
        ts_state = re.search(r"export type APIKeyQuotaAdmissionState = ([^;]+);", TS_CLIENT)
        self.assertIsNotNone(ts_state)
        ts_states = set(re.findall(r"'([^']+)'", ts_state.group(1)))
        openapi_state = re.search(
            r"QuotaAdmissionState:\n\s+type: string\n\s+enum: \[([^\]]+)\]",
            OPENAPI,
        )
        self.assertIsNotNone(openapi_state)
        openapi_states = {value.strip() for value in openapi_state.group(1).split(",")}
        self.assertEqual(expected_states, go_states)
        self.assertEqual(expected_states, ts_states)
        self.assertEqual(expected_states, openapi_states)

        expected_reasons = {"pricing_store_unavailable", "settlement_store_unavailable"}
        go_reasons = set(re.findall(r'QuotaBlock\w+\s+=\s+"([^"]+)"', GO_TYPES))
        ts_summary = TS_CLIENT.split("export interface APIKeyQuotaSummary", 1)[1].split("}", 1)[0]
        ts_reasons = set(re.findall(r"'([^']+_store_unavailable)'", ts_summary))
        openapi_reason = re.search(
            r"QuotaBlockReason:\n\s+type: string\n\s+enum: \[([^\]]+)\]",
            OPENAPI,
        )
        self.assertIsNotNone(openapi_reason)
        openapi_reasons = {value.strip() for value in openapi_reason.group(1).split(",")}
        self.assertEqual(expected_reasons, go_reasons)
        self.assertEqual(expected_reasons, ts_reasons)
        self.assertEqual(expected_reasons, openapi_reasons)

    def test_profile_response_does_not_inherit_write_version(self) -> None:
        profile_schema = OPENAPI.split("    Profile:\n", 1)[1].split(
            "    Policy:\n", 1
        )[0]
        self.assertIn('$ref: "#/components/schemas/ProfileInput"', profile_schema)
        self.assertNotIn("ProfileWrite", profile_schema)
        self.assertIn("policyId", profile_schema)

    def test_runtime_reload_publishes_restored_keys_before_access_auth(self) -> None:
        replacement = re.search(
            r"server_reload_source = ROOT / 'internal/api/server_reload\.go'"
            r"(?:.|\n)*?replace_once\(\s*server_reload_source,"
            r"(?:.|\n)*?'s\.apiKeyPolicy\.SetConfiguredAPIKeys\(cfg\.APIKeys\)',\s*\)",
            CORE_PATCHER,
        )
        self.assertIsNotNone(replacement)
        source = replacement.group(0)
        configured = source.index(
            "s.apiKeyPolicy.SetConfiguredAPIKeys(cfg.APIKeys)"
        )
        access = source.index(
            "accessConfigApplied := s.applyAccessConfig(oldCfg, cfg)", configured
        )
        self.assertLess(configured, access)


if __name__ == "__main__":
    unittest.main()

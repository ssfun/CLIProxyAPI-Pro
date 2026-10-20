# Native auth mutation convergence: failures defined before implementation

The integration checks drive the real Management adapter, native Manager and a file-backed Store. Native persistence is gated to force the previously unsafe read/edit/write ordering. Run the focused Go tests below against a clean generated upstream and retain JSON test output in `/tmp/pro-native-convergence-mutation`.

1. Native proxy/prefix/metadata edit held in persistence while Pro disables an auth: Pro must start from the native committed auth and preserve unrelated fields on disk and in runtime.
2. Native refresh held in persistence while Pro disables an auth: refreshed access/refresh tokens and refresh timestamp must survive in runtime and persisted JSON.
3. A recorded quota-cache cleanup for an auth removed/recreated with identical ID, file and tokens must reject the old registration epoch and preserve the replacement's cache.
4. A recorded inspection recovery for an auth removed/recreated with identical credentials must preserve the replacement's error.
5. Cancellation while waiting on the native mutation gate must return cancellation without invoking the edit or saving.
6. Callback identity changes or an uncommitted mutation must return an error, never success, and must not mutate a different auth.
7. A nil callback must perform no write. Ordinary store errors retain upstream's nonfatal behavior: runtime changes commit, disk does not change, and upstream logs the failure.
8. Shared plugin virtual source disable must still update all identities, preserve source tokens/project lists/unmanaged metadata, and persist disabled once through the existing source adapter.
9. Inspection recovery must recheck the error source and observed credentials within the native mutation gate so newer native errors or refreshed credentials are not cleared by an older probe.

Focused command: `go test -race -count=1 -json ./internal/api/handlers/management -run 'TestProNativeMutation|TestExecuteActionDisablesGeminiCLIPluginVirtualSourceFile|TestPluginVirtualInspectionErrorDoesNotChangeNativeState|TestUpdateProAuthSerializesAdministrativeEnable'`.

The production adapter intentionally retains native nonfatal ordinary Store.Save errors; changing that contract is outside this migration.

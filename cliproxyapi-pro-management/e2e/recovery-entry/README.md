# Inspection recovery entry E2E

Mounts the actual AccountInspectionPage with controlled service APIs and no inspection history or auth files. No direct recovery-dialog mounting or synthetic open action is used. Cases: on-demand read, unsupported openai-compat API-key account with only upstream cooldown, safe labels, account search, independent overlapping-source credential, exact targeted release identity/revision preserving the other source, refresh failure without stale rows, and empty state.

Copy this directory into a clean generated Management checkout after lint/build; start Vite there. Run:

```sh
TASK_SPACE_ID=4 KEEP_TASK_SPACE=1 bash e2e/recovery-entry/run.sh http://127.0.0.1:4173/e2e/recovery-entry/ /private/tmp/recovery-entry-evidence
```

Requires running ego-lite. The script writes result.json and entry-empty.png. Run the existing recovery-dialog-race harness separately for delayed responses and close/reopen behavior. The retirement README records failure cases before implementation.

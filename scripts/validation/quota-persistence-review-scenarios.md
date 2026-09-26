# Quota persistence review

Failure scenarios fixed before implementation (2026-09-26):

- A failed GET must preserve hydrated quota and allow retry at the same backend generation.
- A live quota refresh during GET must remain visible and be persisted; hydration must not suppress it.
- An older persisted observation must not overwrite a newer successful local observation.
- File invalidation or logout during GET must prevent the old response from restoring invalidated data.
- Invalidation while GET is pending must survive completion and trigger the next reload.
- Stop/restart must reject pending hydration, cancel retries, and permit reading a lower generation from another backend.
- Equal timestamps with different payloads must not cause a successful update to be skipped.
- Backend deletion should remove unchanged hydrated state, but preserve locally refreshed state.
- Hydration must not write restored data back; Gemini Core snapshots remain authoritative.

Repeatable focused check after applying the Management overlay:

```sh
bun test tests/quotaPersistence.test.ts
```

This uses the real Zustand store, middleware, normalization and API adapter with controlled API responses. It is an isolated integration regression, not browser or real SQLite E2E coverage.

## Verification result

- Before implementation: 1 passed, 10 failed (`/private/tmp/quota-persistence-before.log`).
- After implementation: all 11 passed (`/private/tmp/quota-persistence-after.log`).
- Fresh upstream checkout with the overlay: `bun run verify` passed all 1,051 tests, ESLint, TypeScript and the production build (`/private/tmp/quota-persistence-final-validation.log`).
- Management customization checks: all 114 passed (`/private/tmp/quota-persistence-customization.log`).
- Overlay surface check passed; reapplication produced identical source/test contents (`/private/tmp/quota-persistence-replay.txt`).
- Backend persistence was reviewed without modification. Real SQLite and browser E2E were not executed for this change.

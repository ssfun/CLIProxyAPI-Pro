# v8.0.44 repair scenarios (written before implementation)

Inputs: Actions 40395244909; Core 1f917e77c4562623aa49e9804d999524151a2f96; models 6e82c3999c895ef83ca9087e6944008658fabe78; Pro base 63a0dfa.

1. The complete generator rejects the removed `httpClient.Do(httpReq)` model-observation anchor before flushing any writes. Reproduce on the exact clean upstream checkout and retain the failure log, exit status, and before/after source hashes.
2. Updating the anchor could miss either ordinary Execute, compact Execute, or streaming. Require exactly two execute matches and one stream match, inserting complete-body model observation immediately before each existing `doCodexRequest` call.
3. Falling back to HTTP-body observation could lose models after a large conversation input or fail on compressed bodies. Preserve explicit observation of the complete `upstreamBody`, using existing final runtime coverage for large-body/model accounting.
4. Replacing the new helper with direct transport could remove upstream zstd rejection retry, response decompression, header preparation, or the streaming response-ready callback. Preserve each helper call and all its arguments verbatim; compare the helper against the clean upstream source.
5. A narrow repair may reveal later stale anchors. Replay the full generator on the exact upstream checkout, retaining logs and inspecting additional failures before further changes. Keep strict replacement counts and late request guards unchanged.
6. Final integration must run existing Core validation and HTTP E2E, retain repeatable logs/artifacts, and check clean-source replay and reapplication rejection. No new unit tests will be written after production changes; full E2E runs are deferred to final integration.

# Account inspection receipt browser fixture

This entrypoint mounts the real `AccountInspectionPage` with local module-level
API stubs and a visible scenario selector. See `scenarios.md` for the frozen
failure matrix.

## Prepare a generated Management checkout

After applying the Pro overlay to a clean upstream Management checkout, copy:

```sh
cp scripts/validation/fixtures/inspection-receipt/main.tsx /path/to/management/review.tsx
cp scripts/validation/fixtures/inspection-receipt/index.html /path/to/management/review.html
```

Start Vite from that checkout:

```sh
bun run dev --host 127.0.0.1 --port 5197
```

Run the smoke through Ego Browser's documented Node.js stdin mode, reusing the
task's existing TaskSpace:

```sh
(printf '%s\n' \
  "globalThis.inspectionReceiptConfig={spaceId:3,url:'http://127.0.0.1:5189/review.html',artifact:'/tmp/inspection-receipt-results.json'};"; \
  cat scripts/validation/inspection_receipt_smoke.mjs) | ego-browser nodejs
```

The runner defaults to TaskSpace `3`, port `5189`, and the artifact path shown
above, so `ego-browser nodejs < scripts/validation/inspection_receipt_smoke.mjs`
is sufficient when those defaults match the validation session. A JavaScript
prefix is used for overrides because the Ego Node.js subprocess does not
inherit arbitrary shell environment variables.

If the browser's screenshot protocol is unavailable, set
`captureScreenshots:false` in the JavaScript prefix. The semantic smoke still
runs and the JSON records both screenshots as `not-captured`; screenshot
timeouts are otherwise recorded as `failed` without discarding DOM evidence.

The runner writes the JSON artifact and adjacent `.desktop.png` and
`.mobile.png` screenshots. It deliberately leaves the TaskSpace open so the
main validation task can inspect the final page and screenshots.

## Batch error localization

The same fixture also provides a batch-error selector and language selector.
`inspection_batch_errors_smoke.mjs` clicks the real recovery/retry buttons,
checks rejected-account details and live language switching, and writes a JSON
report. The APIs are local stubs; this does not send recovery requests to Core.

Prepend a configuration to the runner's stdin (Ego does not inherit shell
variables into its Node process):

```js
globalThis.inspectionBatchErrorConfig = {
  spaceId: 4, // Use the TaskSpace created for your validation task.
  url: 'http://127.0.0.1:5189/review.html',
  artifact: '/tmp/inspection-batch-i18n-browser.json',
};
```

Run that prelude followed by `scripts/validation/inspection_batch_errors_smoke.mjs`
through `ego-browser nodejs`. Finish the TaskSpace after inspecting the report.

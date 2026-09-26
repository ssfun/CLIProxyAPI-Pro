# API Key Policy browser regression

This fixture mounts the real `APIKeyPolicyPage`, hooks and clipboard helper in
Ego Browser. Management responses and clipboard outcomes are controlled locally;
this checks browser behavior, not live Core enforcement. It uses only fake keys.

Failure cases were reproduced before changing the implementation:

- A rejected key read or failed clipboard write must not display success.
- Refresh must clear feedback from the previous snapshot.
- Clipboard completion after disconnect must not notify the new session.
- A failed retry must not retain a previous successful copy indicator.

The smoke also checks successful copying and automatic feedback expiry.

## Replay

1. Apply the Management overlay to a clean upstream checkout using
   `bash cliproxyapi-pro-management/apply.sh /path/to/management` and install its
   dependencies with `bun install --frozen-lockfile`.
2. Copy `api_key_policy_page.tsx` from this directory to the checkout's
   `review.tsx`. Create `review.html` in that checkout:

   ```html
   <!doctype html><html><body><div id="root"></div><script type="module" src="/review.tsx"></script></body></html>
   ```

3. Run `bun run dev --host 127.0.0.1 --port 5197` in the checkout.
4. Reuse the current Ego TaskSpace (create one only if this task has none) and
   substitute its ID for `SPACE_ID` below. Run from this repository:

   ```sh
   ego-browser nodejs -e "globalThis.akpReviewConfig = {spaceId:SPACE_ID,url:'http://127.0.0.1:5197/review.html',artifact:'/tmp/api-key-policy-page-results.json'}; $(cat scripts/validation/api_key_policy_page_smoke.mjs)"
   ```

All seven results must have `passed: true`. The script exits with an error on
failure and writes a JSON artifact with observed notifications and copy state.
Keep the fixture outside production `src`; it is a separate browser entrypoint.

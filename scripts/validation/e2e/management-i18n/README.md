# Management localization acceptance

Validated against Management `v1.26.5` (`fe2963330f32a933f562282165f62ae52cf57145`).

Apply the Pro customizations to a clean upstream checkout, install the pinned dependencies,
and run `bun run verify`. The `proLocaleCoverage` suite compares generated languages with
upstream `SUPPORTED_LANGUAGES` and checks directly owned resources and interpolation variables
for all six languages. `selfUsageLocale` checks the public entry's rendered translations and
locale-specific number formatting without mutating the application's shared i18n instance.

Browser scenarios use a production build and a disposable HTTP backend with synthetic
usage, quota, and data inventory. Open the normal `#/usage` entry, select each language,
submit a synthetic key, and verify quota/statistics/logs and document title. Verify an
invalid key produces a translated error. Refresh and verify the language persists.

Use the normal management login with a disposable backend, open Data Management, and
check Overview, Backup & Restore, Retention & Cleanup, Data Domains, and Operation Log in
each language. Do not execute backup, restore, cleanup, or reset against real data.

On 2026-10-18, all six public query scenarios (query, invalid key, reload with saved language) and all 30 data-management tab scenarios passed through the production build and normal management login. No backend mutations were performed.

The fixtures verify UI language and formatting; they do not validate real backup or quota
accounting. Vietnamese and Korean resources were machine-assisted and reviewed for
placeholders, technical identifiers, navigation labels, and destructive-action wording;
full native-speaker copy review remains outside this verification.

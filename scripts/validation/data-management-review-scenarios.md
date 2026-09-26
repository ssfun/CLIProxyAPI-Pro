# Data management review regression scenarios

Before implementation:
- Two backups in the same second must have different names and remain recognizable to WebDAV listing/retention. Go treats `_000` as literal text, so current names collide.
- Select local file A, then B while A is reading or previewing: only B may populate the restore dialog. A's late error must not notify or clear B's busy state.
- File read failure must be reported without leaving the page busy or retaining an executable old preview.
- A renamed, pretty-printed encrypted backup must open the passphrase dialog; detection must not depend on the filename or exact JSON whitespace.
- JSON `null`, malformed JSON and empty input must not crash manifest detection.
- Backup history refresh must keep only the most recent response; operation history remains sourced from `/data/operations`.

Validation: focused Go backup-name regression and existing observability integration tests; browser fixture renders the real DataManagementPage with controlled API responses. Browser results are saved as JSON, with a page screenshot. Run existing Management tests, type check and production build after implementation.

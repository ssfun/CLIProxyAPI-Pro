# Claude/Kimi parser parity regression

Written before changing the parser. Reference: the customized Management UI at
`/private/tmp/management-ebb4289-repair-20261008/verified-final` and the initial
audit artifacts at `/private/tmp/claude-kimi-audit-HijvXvBm`.

1. An `iguana_necktie` with any finite numeric `limit_dollars`, `used_dollars`,
   or `remaining_dollars` (including zero and numeric strings) is a
   `cloud-session-credits` window with `periodHours: null`. Its utilization,
   reset and quota decision must survive HTTP transport and SQLite persistence;
   exhausted credits must not create a Fable-only routing scope.
2. Dollar credits and active Fable weekly limits must both persist. Credits
   below threshold leave exhausted Fable protection scoped to Fable. Exhausted
   credits make protection account-wide even when Fable also reaches threshold.
   Non-numeric, null, blank or non-finite dollar fields must retain the legacy
   weekly Fable alias behavior, and an actual Fable limit replaces that alias.
3. Kimi second/minute/hour/day/week, singular/plural and `TIME_UNIT_*` forms,
   must produce the same duration label and `periodHours` as manual refresh.
   Trim/case variations are accepted. Missing, blank, unknown and non-string
   units fall back to minutes in both fields. Counts, utilization and resets
   remain unchanged through HTTP and SQLite.

Run the new HTTP -> inspection -> SQLite -> routing cases against the original
parser first and preserve a failing log plus exported JSON. Then apply the
durable parser source through a private Go overlay and rerun with Go 1.26 and
the race detector. Preserve the passing log, request fixtures, SQLite JSON,
routing decisions and a replay command in a dedicated artifact directory.
Run the complete relevant HTTP parity fixture at the end; the main session
owns clean upstream replay and the final binary E2E suite.

# Account inspection batch receipt failure scenarios

This browser fixture mounts the real `AccountInspectionPage` and hydrates its
persisted batch operation through the same `sessionStorage` key used in
production. All API responses are local fixtures. No live account is inspected
and no administrative action is sent to Core.

The receipt must answer the operator's question after a batch action: what
state are the affected accounts in now? Execution transport state remains
available as supporting detail, but it must not be the primary conclusion.

## Frozen failure matrix

| Scenario | Failure before the change | Required browser evidence |
| --- | --- | --- |
| Recheck, every request succeeded but account health is mixed | The header reports only `success 5`, which implies the accounts are all healthy. | The summary separates healthy, quota exhausted, reauthorization required, invalid, and unknown accounts. The four abnormal accounts appear before the healthy account in details. |
| Enable, disable, delete, and quota-protection actions succeeded | The receipt groups everything under execution success and hides the resulting administrative state. | The summary and rows identify enabled, disabled, deleted, and quota protected states. |
| Recovery check returns an empty `after` object and another item has a restricted `authId` | Both transport calls look successful even though their resulting restriction states differ. | The empty `after` item is shown as restrictions cleared. The item whose final `after.authId` is non-empty remains visibly restricted. |
| Outcome payload is missing or contains only `{ result: { key } }` | A succeeded transport item disappears from problem details or a sparse identity-only result looks healthy. | Both items are counted as unknown and appear in the expanded account-state details. |
| Batch still contains running, skipped, and interrupted work | The receipt only exposes pending/skipped/interrupted operation counters without a useful account conclusion. | Running is shown as awaiting result. Stale, unsupported, and interrupted items remain unable to confirm, while their execution statuses still explain why. None is labeled healthy. |
| Warning is nested under `outcome.outcome.warning` | Only top-level errors are visible, so nested supporting evidence disappears. | The nested warning text is displayed on its sparse-result row. That row remains unable to confirm because its result has no health evidence; a warning by itself does not redefine an otherwise proven health state. |
| Responsive layout | Dense receipt content can overflow or hide conclusions on a narrow viewport. | Desktop and 390px-wide screenshots preserve the category summary, scenario selector, and readable account details. |

## Repeatability contract

- The fixture exposes every scenario in a visible dropdown and remounts the
  real page on selection.
- The smoke runner reuses one Ego Browser TaskSpace and writes one JSON result
  file plus desktop and mobile PNG screenshots.
- Assertions use rendered category labels, item order, state attributes, and
  nested warning text. They do not inspect React state or replace the receipt
  component with fixture markup.
- The JSON artifact includes the URL, viewport, observed category order, item
  order, and per-assertion evidence so another run can be compared directly.

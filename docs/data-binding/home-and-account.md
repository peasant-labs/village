# Home and account settings data binding

The root route mounts the signed-in home page only after the account is known.
It derives no repository matcher, transcript access rule, or redaction policy.
The signed-out front door remains its own mounted route surface.

## Home

| Surface | Read | Meaning |
| --- | --- | --- |
| Transcript table | Owner-filtered `GET /transcripts`, paginated | The caller's published sessions; a blank handle never produces an unfiltered request |
| Helper relationships | The existing grouped transcript read | Server decisions about stored helper relationships; the consumer does not recompute them |
| Totals | `GET /users/me/stats` | Server aggregates for the signed-in account, including recorded duration |
| Collective rail | `GET /groups` | The collectives the caller belongs to |
| Waiting contributions | `GET /users/me/collectives/contributions` | The caller's pending counts, separate from accepted contributions |
| Pull request links | Each transcript's served pull request summary | Every shown number links to its repository's pull request page; the full transcript page is the exit when the summary omits references |

Search is settled before issuing a request. Loading another page preserves
already loaded rows; a failed continuation is distinct from an empty first page.
The list, totals and rail reads report their own failures and retry separately.
Parent and child rows use one published-time comparison for ordering.

The home page has no pull request action queue or provider icons, following the
owner's review decisions. Pull request references are a many-valued list. User
handles and transcript text preserve their case.

## Account settings

`/settings` is keyed by account ID so text edits and failures from one account
cannot survive into another. Every `me` query is also keyed by the account handle.
Fairtrade supplies the settings groups, rows, switches and inline confirmations.

| Control | Read/write |
| --- | --- |
| Handle | Authenticated profile; `PATCH /auth/me/username` |
| Discoverable profile | Authenticated profile; `PATCH /auth/me/settings` |
| Automatic pull request linking | `GET`/`PATCH /users/me/settings`, sending only `auto_attach_pull_requests` |
| GitHub sign-out | `POST /auth/logout` |
| Peasant sign-ins | `GET /auth/api-keys`; working keys labelled `peasant-cli` |
| Sign out everywhere | Sequential `DELETE /auth/api-keys/{id}` calls, stopping at the first failure and refreshing the list afterwards |
| Delete account | Confirmed `DELETE /auth/me` |

Automatic linking requires the backend that acts on the saved choice. Its row
is disabled until the settings read succeeds; a read failure announces the
reason and offers retry. A failed write restores the previously saved value.
The setting restates the server's action and never selects transcripts itself.

The connection count uses active CLI sign-in keys, rather than inventing machine
identity. The copy follows the canonical account settings demo. Partial key
revocation reports how far it got and never claims every sign-in was revoked.

## Review evidence

Mounted tests execute the registered home and settings components under the real
auth and query providers, stubbing HTTP only. Named YAML fixtures cover writes,
rollback, retry and private owner scoping. Production journeys include the header
navigation into settings. Captures must use a production build from the reviewed
source head, verify its served chunks, and inspect the full shell in both themes.
The consolidated integration build also includes the backend attachment and
transcript-page dependencies; branch screenshots remain separate from its proof.

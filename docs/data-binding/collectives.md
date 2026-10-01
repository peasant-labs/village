# Collective pages data binding

The mounted collective routes adapt Village reads and writes to Fairtrade's
collective list, detail and settings views. The server owns membership,
visibility, admission and contribution decisions; the frontend displays its
answers and never rebuilds those access rules.

| Surface | Boundary |
| --- | --- |
| Your collective list | `GET /groups` |
| Visible collective table | `GET /groups/visible` |
| Create collective | `POST /groups`; navigate to the returned collective ID only after success |
| Collective detail and settings | `GET /groups/{id}` |
| Accepted transcript page | Paginated `GET /groups/{id}/transcripts` |
| The caller's contributions | `GET /groups/{id}/my-shares` |
| Change a setting | Partial `PATCH /groups/{id}`; omitted name and description remain unchanged |
| Join or leave | `POST /groups/{id}/join`; `DELETE /groups/{id}/members/{userID}` with the chosen retraction option |
| Member administration | Existing add-member and role-change routes, guarded by server ownership |
| Repository connections | Existing collective repository routes; the backend decides permissions and GitHub effects |

Failed reads are errors with retry controls, including the visible table, detail
and settings. They cannot become a successful empty list or a not-found claim.
Failed creation preserves the entered form, shows the reason and leaves the
current route in place. A successful creation opens the actual returned ID.
Settings writes use the canonical saved response and settle through Fairtrade.

Policy summaries restate the server's saved values and do not enforce them.
Owner-only controls follow the server response. User names retain their case,
counts use tabular figures, and hidden prior browsing helpers remain in source.

Named fixtures mount the registered page components with real auth and query
providers, replacing HTTP only. Production journeys exercise the table, detail
and settings routes. Review screenshots must show their mounted full shell in
both themes from the exact source branch, verify served build provenance, and
remain distinct from the consolidated integration captures.

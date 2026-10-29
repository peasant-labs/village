# Village interface review screenshots

Mounted standalone production build from `3c5505ca26b2aa93e7221cf08c83b438de3247fe`, using registry `@peasant-labs/fairtrade@0.0.53`. JS and CSS served hashes match that exact clean build. All content and identities are synthetic fixture data.

Both themes at1440×1000 desktop and390×1000 touch/mobile; reduced motion. `proof.json` records56 loaded route states,12 successful navigation/keyboard states, package integrity, build ID, served asset hashes, fixture provenance, and local check limitations.

The generic journey fixture does not serve public-profile metadata. Profile screenshots use the existing dedicated profile REST fixture with the same production build. This is explicit fixture composition, not a claim that the generic journey mock covers those reads. Long Mermaid diagrams render real SVG in their native scroll region; document width remains within the viewport.

| Surface | Desktop light | Desktop dark | Phone light | Phone dark |
|---|---|---|---|---|
| home | [light 1440](home-light-1440.png) | [dark 1440](home-dark-1440.png) | [light 390](home-light-390.png) | [dark 390](home-dark-390.png) |
| profile | [light 1440](profile-light-1440.png) | [dark 1440](profile-dark-1440.png) | [light 390](profile-light-390.png) | [dark 390](profile-dark-390.png) |
| discover-transcripts | [light 1440](discover-transcripts-light-1440.png) | [dark 1440](discover-transcripts-dark-1440.png) | [light 390](discover-transcripts-light-390.png) | [dark 390](discover-transcripts-dark-390.png) |
| discover-collectives | [light 1440](discover-collectives-light-1440.png) | [dark 1440](discover-collectives-dark-1440.png) | [light 390](discover-collectives-light-390.png) | [dark 390](discover-collectives-dark-390.png) |
| review | [light 1440](review-light-1440.png) | [dark 1440](review-dark-1440.png) | [light 390](review-light-390.png) | [dark 390](review-dark-390.png) |
| waiting | [light 1440](waiting-light-1440.png) | [dark 1440](waiting-dark-1440.png) | [light 390](waiting-light-390.png) | [dark 390](waiting-dark-390.png) |
| settings-email | [light 1440](settings-email-light-1440.png) | [dark 1440](settings-email-dark-1440.png) | [light 390](settings-email-light-390.png) | [dark 390](settings-email-dark-390.png) |
| settings-members | [light 1440](settings-members-light-1440.png) | [dark 1440](settings-members-dark-1440.png) | [light 390](settings-members-light-390.png) | [dark 390](settings-members-dark-390.png) |
| repositories | [light 1440](repositories-light-1440.png) | [dark 1440](repositories-dark-1440.png) | [light 390](repositories-light-390.png) | [dark 390](repositories-dark-390.png) |
| pull-requests | [light 1440](pull-requests-light-1440.png) | [dark 1440](pull-requests-dark-1440.png) | [light 390](pull-requests-light-390.png) | [dark 390](pull-requests-dark-390.png) |
| pr-digest | [light 1440](pr-digest-light-1440.png) | [dark 1440](pr-digest-dark-1440.png) | [light 390](pr-digest-light-390.png) | [dark 390](pr-digest-dark-390.png) |
| long-demo | [light 1440](long-demo-light-1440.png) | [dark 1440](long-demo-dark-1440.png) | [light 390](long-demo-light-390.png) | [dark 390](long-demo-dark-390.png) |
| long-diagram | [light 1440](long-diagram-light-1440.png) | [dark 1440](long-diagram-dark-1440.png) | [light 390](long-diagram-light-390.png) | [dark 390](long-diagram-dark-390.png) |
| repository-controls | [light 1440](repository-controls-light-1440.png) | [dark 1440](repository-controls-dark-1440.png) | [light 390](repository-controls-light-390.png) | [dark 390](repository-controls-dark-390.png) |

The source branch and this screenshot branch must remain available while PR342 is open. Screenshots are kept outside the production feature branch.

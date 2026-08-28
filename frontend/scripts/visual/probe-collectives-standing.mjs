/* Computed-style probe for the collectives card's standing slot.

   The collectives list shows every collective a person may see, so a card can
   have nothing to say about them. The shared card always emits its standing
   slot and draws each following separator BEFORE its own value, so such a card
   would open with a stray separator ahead of the member count. `globals.css`
   collapses the empty slot and that separator.

   The mounted test cannot check this. It loads no stylesheet, so it can only
   prove the rule still has a target. Whether the rule still HIDES the element
   needs a browser, which is what this probe is for: it reads the computed
   `display` of the empty slot and of the separator that follows it on a real
   served build.

   This is manual tooling. No workflow runs the visual scripts, so this does not
   gate a merge; run it when the card, the rule, or the pinned design system
   changes.

   usage:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm build && pnpm start
     CHROME_PATH=... VILLAGE_URL=http://localhost:3000 node scripts/visual/probe-collectives-standing.mjs
*/
import puppeteer from 'puppeteer-core'

const CHROME = process.env.CHROME_PATH
const ORIGIN = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const THEME = process.argv[2] || 'dark'

const die = (code, what, why, impact, repair) => {
  console.error(`ERROR [probe-collectives-standing.mjs] ${what}\nwhy: ${why}\nimpact: ${impact}\nrepair: ${repair}`)
  process.exit(code)
}

if (!CHROME) {
  die(1, 'CHROME_PATH is unset.', 'the probe needs a browser to compute styles.',
    'nothing was checked.', 'set CHROME_PATH to your Chrome or Chromium binary and retry.')
}

const browser = await puppeteer.launch({ executablePath: CHROME, headless: 'new', defaultViewport: { width: 1460, height: 1000 } })
const page = await browser.newPage()
await page.setCookie({ name: 'peasant_token', value: 'demo-token', url: ORIGIN, path: '/' })
if (THEME === 'light') await page.evaluateOnNewDocument(() => localStorage.setItem('peasant-theme', 'light'))
await page.goto(`${ORIGIN}/groups`, { waitUntil: 'domcontentloaded' })

// The cards arrive with the collectives response, and the standing slot is
// filled by a second response, so wait for a card rather than a fixed delay.
await page.waitForSelector('.cmg-col-card', { timeout: 20000 }).catch(() => {})

const cards = await page.evaluate(() => [...document.querySelectorAll('.cmg-col-card')].map((card) => {
  const foot = card.querySelector('.cmg-col-foot')
  const role = foot?.querySelector('.cmg-col-role')
  const firstDot = foot?.querySelector('.cmg-dot')
  return {
    name: card.querySelector('.cmg-col-name')?.textContent ?? '',
    standing: (role?.textContent ?? '').trim(),
    roleDisplay: role ? getComputedStyle(role).display : null,
    firstDotDisplay: firstDot ? getComputedStyle(firstDot).display : null,
    // What a reader actually sees, with the collapsed items removed.
    visibleFoot: foot
      ? [...foot.children].filter((c) => getComputedStyle(c).display !== 'none').map((c) => c.textContent).join('')
      : '',
  }
}))

await browser.close()

if (cards.length === 0) {
  die(2, 'the collectives page rendered no cards.',
    'the served build, the mock, or the signed-in session is not what this probe assumes.',
    'nothing was checked, and a green run would have been meaningless.',
    'confirm the server serves this worktree and the mock answers /groups/visible, then retry.')
}

const bare = cards.filter((c) => c.standing === '')
if (bare.length === 0) {
  die(2, 'no card has an empty standing slot.',
    'the rule under test only applies to a collective the viewer neither belongs to nor contributed to.',
    'the probe could not check the case it exists for.',
    'confirm the mock serves a collective with a null role and no contribution, then retry.')
}

const failures = []
for (const card of bare) {
  if (card.roleDisplay !== 'none') {
    failures.push(`"${card.name}": the empty standing slot computes display ${card.roleDisplay}, want none`)
  }
  if (card.firstDotDisplay !== 'none') {
    failures.push(`"${card.name}": the separator after the empty standing slot computes display ${card.firstDotDisplay}, want none`)
  }
  if (/^\s*·/.test(card.visibleFoot)) {
    failures.push(`"${card.name}": the footer a reader sees opens with a separator: "${card.visibleFoot}"`)
  }
}

for (const card of cards) {
  console.log(`${card.standing === '' ? 'bare ' : 'stand'} ${card.name.padEnd(28)} foot="${card.visibleFoot}"`)
}

if (failures.length > 0) {
  die(3, 'the collectives card shows a stray leading separator.',
    failures.join('; '),
    'a person browsing collectives sees a footer that opens on a separator.',
    'the rule in src/app/globals.css no longer hides the empty standing slot, or the design system now draws its separators between items and the rule should be deleted.')
}

console.log(`ok  ${bare.length} card(s) with no standing collapse their slot and its separator (${THEME})`)

/* Capture the production reader against the published-image REST fixture. */
import { mkdirSync } from 'node:fs'
import puppeteer from 'puppeteer-core'
import { applyDeterminism } from './determinism.mjs'

const theme = process.argv[2]
const out = process.argv[3]
if (!['dark', 'light'].includes(theme) || !out || !process.env.CHROME_PATH) throw new Error('Pass dark|light, output directory, and CHROME_PATH')
const origin = (process.env.VILLAGE_URL || 'http://localhost:3000').replace(/\/$/, '')
const browser = await puppeteer.launch({ executablePath: process.env.CHROME_PATH, headless: true, defaultViewport: { width: 1460, height: 1000 } })
try {
  const page = await browser.newPage()
  const requests = []
  page.on('request', (req) => requests.push(req.url()))
  await page.evaluateOnNewDocument((t) => localStorage.setItem('peasant-theme', t), theme)
  await applyDeterminism(page)
  await page.goto(`${origin}/transcripts/omitted-images`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-image-omitted="not_published"]')
  const info = await page.evaluate(() => {
    const cards = [...document.querySelectorAll('[data-image-omitted="not_published"]')]
    const note = [...document.querySelectorAll('*')].find((el) => el.textContent?.trim() === '2 images omitted')
    const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.visibility === 'visible' && s.display !== 'none' }
    return { theme: document.documentElement.dataset.theme, cards: cards.length, cardText: cards.map((el) => el.textContent), visible: cards.every(visible), note: !!note && visible(note), height: document.documentElement.scrollHeight }
  })
  if (info.theme !== theme || info.cards !== 2 || !info.visible || !info.note || info.cardText.some((t) => !t.includes('image not published'))) throw new Error(`reader assertion failed: ${JSON.stringify(info)}`)
  if (requests.some((url) => /\/api\/v1\/attachments\/|[a-f0-9]{64}/.test(url))) throw new Error('unexpected image-byte request')
  await page.setViewport({ width: 1460, height: Math.max(1000, info.height), deviceScaleFactor: 1 })
  await page.evaluate(() => window.scrollTo(0, 0))
  const bounds = await page.evaluate(() => ({ scroll: window.scrollY, height: document.documentElement.scrollHeight, viewport: innerHeight, cards: [...document.querySelectorAll('[data-image-omitted="not_published"]')].map((el) => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom } }) }))
  if (bounds.scroll !== 0 || bounds.height > bounds.viewport || bounds.cards.some((r) => r.top < 0 || r.bottom > bounds.viewport)) throw new Error(`capture clipped: ${JSON.stringify(bounds)}`)
  mkdirSync(out, { recursive: true })
  const path = `${out}/published-images-${theme}.png`
  await page.screenshot({ path })
  console.log(JSON.stringify({ path, info, bounds, requests }, null, 2))
} finally { await browser.close() }

#!/usr/bin/env node
/**
 * screenshot-dashboard.mjs — Capture screenshots of the Orchestra web dashboard.
 * Usage: node scripts/screenshot-dashboard.mjs [port] [output-dir]
 *
 * Starts a headless browser, navigates to each dashboard page, captures screenshots.
 * Used by orchestra agents for round-trip visual verification against reference screenshots.
 */
import { chromium } from 'playwright';

const port = process.argv[2] || '8000';
const outDir = process.argv[3] || 'specs/reference/screenshots/current';
const baseURL = `http://localhost:${port}`;

import { mkdirSync } from 'fs';
mkdirSync(outDir, { recursive: true });

const pages = [
  { name: 'factory', path: '/' },
  { name: 'briefing', path: '/briefing' },
  { name: 'config', path: '/config' },
  { name: 'git', path: '/git/latest' },
  { name: 'artifacts', path: '/artifacts' },
];

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1512, height: 900 } });

for (const { name, path } of pages) {
  try {
    const resp = await page.goto(`${baseURL}${path}`, { timeout: 5000 });
    await page.waitForTimeout(2000); // wait for HTMX polling to settle
    await page.screenshot({ path: `${outDir}/${name}.png`, fullPage: false });
    console.log(`OK: ${name}.png (${resp.status()})`);
  } catch (err) {
    console.log(`FAIL: ${name} — ${err.message}`);
  }
}

await browser.close();
console.log(`Screenshots saved to ${outDir}`);

import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { payload } from '../third_party/codex-wallpapers/src/apply.mjs';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(process.env.PLAYWRIGHT_MODULE) : 'playwright');
const browser = await chromium.launch({headless:true,...(process.env.CW_TEST_BROWSER ? {executablePath:process.env.CW_TEST_BROWSER} : {})});
try {
  const page = await browser.newPage({viewport:{width:1100,height:820}});
  await page.setContent(await fs.readFile('third_party/codex-wallpapers/tests/fixture.html','utf8'));
  await page.evaluate(() => {
    window.updateQueued = false;
    window.fetch = async (url, options={}) => {
      let result = {};
      if (url.endsWith('/accounts')) result = {accounts:[{id:'primary',label:'Primary',planLabel:'Plus',email:'hidden@example.com',enabled:true,connected:true,controller:true,rateLimits:{primary:{windowDurationMins:300,usedPercent:20}}}]};
      if (url.endsWith('/updates')) {
        if (options.method === 'POST') window.updateQueued = true;
        result = {supported:true,available:true,queued:window.updateQueued};
      }
      return {ok:true,json:async()=>result};
    };
  });
  await page.evaluate(await payload());
  await page.evaluate('window.__CODEX_WALLPAPERS_PUBLIC__.ready()');
  await page.addScriptTag({content:await fs.readFile('ui/windows-account-panel.js','utf8')});
  await page.getByRole('button',{name:'Subscriptions',exact:true}).click();
  await page.getByText('An update is available for the combined app.').waitFor();
  assert.equal(await page.locator('.cmx-panel').evaluate(e=>getComputedStyle(e).width),'390px');
  assert.equal(await page.getByText('hidden@example.com',{exact:true}).count(),0);
  await page.getByRole('button',{name:'Update on next launch',exact:true}).click();
  await page.getByText('Update queued.',{exact:false}).waitFor();
  await page.locator('#profile').click();
  assert.equal(await page.locator('.cmx-panel').evaluate(e=>e.classList.contains('cmx-hidden')),true);
  await page.locator('[data-cw-menu]').click();
  await page.locator('#heading').getByText('Your wallpapers').waitFor();
  await page.keyboard.press('Escape');
  await page.locator('textarea').fill('Both panels preserve the composer.');
  assert.equal(await page.locator('textarea').inputValue(),'Both panels preserve the composer.');
  await fs.mkdir('build/combined-ui',{recursive:true});
  await page.getByRole('button',{name:'Subscriptions',exact:true}).click();
  await page.screenshot({path:'build/combined-ui/router-wallpapers.png',animations:'disabled'});
  console.log('Combined UI passed: compact panel, masked email, queued update, outside dismissal, wallpaper picker and composer.');
} finally { await browser.close(); }

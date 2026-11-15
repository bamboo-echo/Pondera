
const { chromium } = require("playwright");
(async () => {
  const browser = await chromium.launch({ channel: "msedge", headless: true });
  const page = await browser.newPage({ locale: "zh-CN", viewport: { width: 1680, height: 1050 } });
  await page.goto("http://127.0.0.1:4681/?mock=deepseek_upgrade&bench=1&platform=windows", { waitUntil: "domcontentloaded" });
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor({ timeout: 60000 });
  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator(".settings-center__navitem").first().waitFor();
  await page.locator(".settings-center__navitem:has(svg.lucide-cable)").click();
  await page.waitForTimeout(1500);
  const info = await page.evaluate(() => {
    const rect = el => el ? { w: Math.round(el.getBoundingClientRect().width), h: Math.round(el.getBoundingClientRect().height) } : null;
    const content = document.querySelector(".settings-page-content");
    return { content: rect(content), page: rect(document.querySelector(".settings-page")), textLen: (content?.textContent||"").trim().length };
  });
  await page.screenshot({ path: "C:/Users/slougo/Desktop/dp1/pnodera改造/temp/settings-fixed.png" });
  console.log(JSON.stringify(info));
  await browser.close();
})();

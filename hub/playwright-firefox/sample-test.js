// Sample Playwright test to verify the browser is working correctly.
// Run: node /home/playwright/sample-test.js
const { firefox } = require("playwright-core");
const assert = require("assert");

(async () => {
  console.log("Connecting to Playwright server...");
  const browser = await firefox.connect("ws://localhost:8081");

  try {
    // Test 1: Load example.com
    console.log("\n--- Test 1: example.com ---");
    const page1 = await browser.newPage();
    await page1.goto("https://example.com");
    const title1 = await page1.title();
    console.log("Title:", title1);
    assert(title1.includes("Example Domain"), `Expected title to contain 'Example Domain', got: ${title1}`);
    console.log("PASS: example.com loaded successfully");
    await page1.close();

    // Test 2: Load Wikipedia
    console.log("\n--- Test 2: wikipedia.org ---");
    const page2 = await browser.newPage();
    await page2.goto("https://en.wikipedia.org/wiki/Main_Page");
    const title2 = await page2.title();
    console.log("Title:", title2);
    assert(title2.includes("Wikipedia"), `Expected title to contain 'Wikipedia', got: ${title2}`);
    const heading = await page2.textContent("#mp-welcome");
    console.log("Welcome heading found:", heading ? "yes" : "no");
    console.log("PASS: Wikipedia loaded successfully");
    await page2.close();

    console.log("\nAll tests passed!");
  } finally {
    await browser.close();
  }
})();

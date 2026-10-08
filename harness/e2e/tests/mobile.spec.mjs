// 하네스 웹 UI E2E (모바일 폭 390px).
import { expect, test } from "@playwright/test";

test("설정 서랍을 열고 닫는다, 가로 스크롤이 없다", async ({ page }) => {
  await page.goto("/");
  await page.locator(".tabs button[data-tab=chat]").click();
  const sidebar = page.locator("#sidebar");
  await expect(sidebar).not.toHaveClass(/open/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  await page.locator("#openSidebar").click();
  await expect(sidebar).toHaveClass(/open/);
  await expect(page.locator("#backdrop")).toBeVisible();
  await page.locator("#backdrop").click({ position: { x: 370, y: 400 } });
  await expect(sidebar).not.toHaveClass(/open/);
});

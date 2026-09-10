const { test, expect } = require("@playwright/test");

test("order support Agent requires confirmation before cancellation", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/user");
  await page.locator("#login-user-name").fill("demo");
  await page.locator("#login-password").fill("demo123456");
  await page.getByRole("button", { name: "验证登录" }).click();
  await expect(page.locator("#auth-status")).toHaveText("已登录");

  const token = await page.evaluate(() => sessionStorage.getItem("product_admin_token"));
  const orderResponse = await page.request.post("/order/create", {
    headers: {
      Authorization: `Bearer ${token}`,
      "Idempotency-Key": `agent-ui-${Date.now()}`,
      "Content-Type": "application/x-www-form-urlencoded"
    },
    form: { userID: "1", productID: "1", quantity: "1", orderStatus: "0" }
  });
  expect(orderResponse.ok()).toBeTruthy();
  const order = await orderResponse.json();
  const orderID = order.data.orderID;

  await page.goto("/agent");
  await expect(page.locator("#agent-mode")).toHaveText("本地演示");
  await page.locator("#agent-input").fill(`取消订单 ${orderID}`);
  await page.locator("#agent-send").click();
  await expect(page.getByText(`确认取消订单 ${orderID}？`)).toBeVisible();
  await expect(page.locator(".trace-item strong")).toHaveText("prepare_cancel_order");
  await page.getByRole("button", { name: "确认取消" }).click();
  await expect(page.getByText(`订单 ${orderID} 已取消，库存已经恢复。`)).toBeVisible();
  await expect(page.locator(".trace-item strong")).toHaveText("cancel_my_pending_order");

  await page.screenshot({ path: ".codex-cache/agent-desktop.png", fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy();

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator("#agent-input")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy();
  await page.screenshot({ path: ".codex-cache/agent-mobile.png", fullPage: true });
});

// Runs the built UI in a real browser with isolated, intercepted API contracts.
// No production authentication bypass or database fixture is installed.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import net from "node:net";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.XIANGWAN_PLAYWRIGHT_MODULE || "playwright");
const appDirectory = fileURLToPath(new URL("../", import.meta.url));
const uuid = (n) => `11111111-1111-4111-8111-${String(n).padStart(12, "0")}`;
const seriesID = uuid(1),
  sourceID = uuid(2),
  targetID = uuid(3);
const recoveryKey = "xiangwan-admin:activity-create-recovery:v1";
const series = {
  id: seriesID,
  title: "浏览器验证系列",
  version: 4,
  status: "active",
  is_recurring: true,
  home_visible: true,
  published_instances: 1,
  favorite_count: 0,
  registration_count: 0,
  updated_at: "2026-10-01T00:00:00Z",
};
const instance = {
  id: sourceID,
  series_id: seriesID,
  issue_no: 1,
  title: "第1期浏览器验证系列",
  version: 7,
  presentation_revision: 2,
  status: "completed",
  activity_type: "custom",
  quick_tag_codes: [],
  publication_version: 1,
  detail_blocks: [{ type: "text", title: "简介", body: "复制内容" }],
  updated_at: "2026-10-01T00:00:00Z",
};
const session = (id, index) => ({
  id,
  instance_id: sourceID,
  title: `来源场次${index + 1}`,
  status: "ended",
  sort_order: index,
  version: 1,
  capacity: 20,
  group_minimum: 2,
  low_stock_threshold: 3,
  price_cents: 0,
  delivery_mode: "offline",
  area: "hexi",
  venue_name: "测试场地",
  address: "测试地址",
  longitude: 117.2,
  latitude: 39.08,
  confirmed_registration_count: 0,
});
const source = {
  series,
  instance,
  sessions: [session(uuid(4), 0), session(uuid(5), 1), session(uuid(6), 2)],
  questionnaire: { configured: false, fields: [] },
};
const envelope = (data) => ({ code: 0, message: "ok", data });
const operationPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

async function startServer() {
  const port = await new Promise((resolve, reject) => {
    const socket = net.createServer();
    socket.once("error", reject);
    socket.listen(0, "127.0.0.1", () => {
      const port = socket.address().port;
      socket.close(() => resolve(port));
    });
  });
  const origin = `http://127.0.0.1:${port}`;
  const standalone = process.env.XIANGWAN_VERIFY_STANDALONE === "true";
  const serverArguments = standalone
    ? [
        fileURLToPath(
          new URL("../.next/standalone/apps/xiangwan-admin-web/server.js", import.meta.url),
        ),
      ]
    : [require.resolve("next/dist/bin/next"), "start", "-H", "127.0.0.1", "-p", String(port)];
  const server = spawn(process.execPath, serverArguments, {
    cwd: appDirectory,
    windowsHide: true,
    env: {
      ...process.env,
      XIANGWAN_ADMIN_API_ORIGIN: "http://127.0.0.1:1",
      HOSTNAME: "127.0.0.1",
      PORT: String(port),
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let output = "";
  for (const stream of [server.stdout, server.stderr])
    stream.on("data", (data) => {
      output = (output + data).slice(-4000);
    });
  try {
    for (let attempt = 0; attempt < 100; attempt++) {
      if (server.exitCode !== null) throw new Error(`Test server exited: ${output}`);
      try {
        if ((await fetch(origin)).ok) return { server, origin };
      } catch {
        /* starting */
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error(`Test server did not start: ${output}`);
  } catch (error) {
    server.kill();
    throw error;
  }
}

async function verifyCopy(browser, origin, loseSecondReceipt) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const errors = [],
    writes = [],
    receipts = new Map(),
    created = [];
  let currentVersion = 1,
    copyCount = 0,
    lost = false;
  const target = () => ({
    series,
    instance: {
      ...instance,
      id: targetID,
      issue_no: 2,
      title: "第2期浏览器验证系列",
      status: "draft",
      version: currentVersion,
    },
    sessions: created,
    questionnaire: source.questionnaire,
  });
  page.on("pageerror", (error) => errors.push(error.message));
  await context.route("**/api/v1/xiangwan/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1/xiangwan", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (request.method() === "GET") {
      if (path === "/admin/auth/status")
        return reply({
          enabled: true,
          authenticated: true,
          principal_id: uuid(99),
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        });
      if (path === "/public-policies")
        return reply({ published_versions: [{ kind: "privacy", version: "test-v1" }] });
      if (path === "/admin/series")
        return reply({ items: [series], page: 1, page_size: 100, total: 1 });
      if (path === "/admin/brand-profile") return reply({ quick_tags: [] });
      if (path === `/admin/instances/${sourceID}`) return reply(source);
      if (path === `/admin/instances/${targetID}`) return reply(target());
      if (path.endsWith("/review-status"))
        return reply({
          instance_id: targetID,
          instance_status: "draft",
          public_review_eligible: false,
          public_review_available: false,
          instance_review_document_count: 0,
          public_session_resource_count: 0,
          sessions: [],
        });
      if (path.endsWith("/roles") || path === "/admin/people")
        return reply({ items: [], total: 0, page: 1, page_size: 100 });
      throw new Error(`Unexpected test read ${path}`);
    }
    const body = request.postDataJSON(),
      operation = request.headers()["idempotency-key"];
    assert.match(operation, operationPattern);
    writes.push({ path, operation, body });
    if (receipts.has(operation)) {
      const prior = receipts.get(operation);
      assert.deepEqual({ path, body }, { path: prior.path, body: prior.body });
      return reply(prior.result);
    }
    let result;
    if (path === `/admin/instances/${sourceID}/copies`) {
      assert.equal(++copyCount, 1);
      assert.equal(body.expected_source_version, 7);
      assert.equal(body.expected_source_presentation_revision, 2);
      assert.equal(body.expected_series_version, 4);
      assert.equal(body.expected_source_questionnaire_version_id, "");
      assert.equal(body.activity_type, "custom");
      result = target().instance;
    } else if (path === `/admin/instances/${targetID}/sessions`) {
      assert.equal(body.expected_instance_version, currentVersion);
      assert.equal(body.sort_order, created.length);
      result = {
        ...session(uuid(10 + created.length), created.length),
        ...body,
        instance_id: targetID,
        status: "draft",
      };
      created.push(result);
      currentVersion++;
    } else throw new Error(`Unexpected test write ${path}`);
    receipts.set(operation, { path, body, result });
    if (loseSecondReceipt && created.length === 2 && !lost) {
      lost = true;
      return route.abort("connectionreset");
    }
    return reply(result);
  });
  try {
    await page.goto(`${origin}/activities/new?copy_instance_id=${sourceID}`);
    await page.getByText(/已带入.*3 个场次设置/).waitFor();
    assert.equal(await page.getByLabel("活动类型").getAttribute("aria-required"), "true");
    assert.equal(await page.getByLabel("活动简介（可选）").getAttribute("required"), null);
    const marks = await page
      .locator(".required-mark")
      .evaluateAll((elements) => elements.map((element) => getComputedStyle(element).color));
    assert.ok(marks.length > 20);
    assert.ok(
      marks.every((color) => color === "rgb(180, 35, 24)"),
      "required markers must be red",
    );
    const markerLines = await page.locator("label > .required-mark").evaluateAll((elements) =>
      elements.map((element) => {
        const text = [...element.parentElement.childNodes].find(
          (node) => node.nodeType === Node.TEXT_NODE && node.textContent.trim(),
        );
        if (!text) return true;
        const caption = document.createRange();
        caption.selectNodeContents(text);
        const captionBox = caption.getBoundingClientRect(),
          markerBox = element.getBoundingClientRect();
        return Math.abs(captionBox.top - markerBox.top) < 3;
      }),
    );
    assert.ok(
      markerLines.length > 10 && markerLines.every(Boolean),
      "required markers must share the caption line",
    );
    const inputs = page.locator('input[type="datetime-local"]');
    assert.equal(await inputs.count(), 12);
    for (let i = 0; i < 12; i++) {
      assert.equal(await inputs.nth(i).inputValue(), "", "source schedule must not copy");
      await inputs
        .nth(i)
        .fill(
          `2027-01-${String(10 + Math.floor(i / 4)).padStart(2, "0")}T${["09:00", "10:00", "11:00", "12:00"][i % 4]}`,
        );
    }
    await page.getByRole("button", { name: "创建草稿与 3 个场次" }).click();
    if (loseSecondReceipt) {
      await page.getByRole("button", { name: "确认未知结果并重试" }).waitFor();
      const saved = await page.evaluate(
        (key) => JSON.parse(sessionStorage.getItem(key)),
        recoveryKey,
      );
      assert.equal(saved.instanceCheckpoint.id, targetID);
      assert.equal(saved.additionalSessionExpectedVersion, 2);
      assert.equal(saved.additionalSessionCheckpoints.length, 0);
      assert.equal(saved.sessionCheckpoint.id, uuid(10));
      const uncertainWrite = writes.at(-1);
      assert.equal(saved.additionalSessionOperation, uncertainWrite.operation);
      await page.reload();
      await page.getByRole("button", { name: "确认未知结果并重试" }).waitFor();
      await page.getByRole("button", { name: "确认未知结果并重试" }).click();
      await page.waitForURL(`${origin}/activities/${targetID}`);
      assert.deepEqual(
        writes[3],
        uncertainWrite,
        "reload must replay the exact second Session request",
      );
      assert.equal(writes[4].body.expected_instance_version, 3);
    } else await page.waitForURL(`${origin}/activities/${targetID}`);
    assert.equal(copyCount, 1);
    assert.equal(created.length, 3);
    assert.equal(await page.evaluate((key) => sessionStorage.getItem(key), recoveryKey), null);
    assert.deepEqual(errors, []);
    console.log(
      `PASS browser: copy three Sessions${loseSecondReceipt ? " with committed response loss and reload" : " sequentially"}`,
    );
  } finally {
    await context.close();
  }
}

async function verifyPeople(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const errors = [],
    roleReads = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await context.route("**/api/v1/xiangwan/admin/**", async (route) => {
    const request = route.request();
    assert.equal(request.method(), "GET", "People read verification must not write");
    const path = new URL(request.url()).pathname.replace("/api/v1/xiangwan/admin", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (path === "/auth/status")
      return reply({
        enabled: true,
        authenticated: true,
        grants: [{ capability: "super_admin", scope_type: "tenant" }],
      });
    if (path === "/people") return reply({ items: [], page: 1, page_size: 100, total: 0 });
    if (path === "/instances")
      return reply({
        page: 1,
        page_size: 100,
        total: 2,
        items: [
          { instance, series_title: series.title },
          {
            instance: { ...instance, id: targetID, title: "第2期浏览器验证系列" },
            series_title: series.title,
          },
        ],
      });
    if (path === `/instances/${sourceID}/roles`) {
      roleReads.push(sourceID);
      return reply({ items: [] });
    }
    if (path === `/instances/${targetID}/roles`) {
      roleReads.push(targetID);
      return reply({
        items: [
          {
            id: uuid(70),
            instance_id: targetID,
            people_profile_id: uuid(71),
            display_name: "浏览器角色验证人物",
            role_code: "host",
            role_status: "active",
            grant_reason: "浏览器验证",
            version: 1,
            granted_at: "2026-10-01T00:00:00Z",
          },
        ],
      });
    }
    throw new Error(`Unexpected People test read ${path}`);
  });
  try {
    await page.goto(`${origin}/people`);
    await page.getByRole("option", { name: /第1期浏览器验证系列/ }).waitFor({ state: "attached" });
    await page.getByText("当前活动暂无角色", { exact: true }).waitFor();
    await page.getByLabel("目标活动").selectOption(targetID);
    await page.getByRole("cell", { name: /^浏览器角色验证人物/ }).waitFor();
    await page.getByLabel("目标活动").selectOption(sourceID);
    await page.getByText("当前活动暂无角色", { exact: true }).waitFor();
    assert.deepEqual(roleReads, [sourceID, targetID, sourceID]);
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: People reads role items envelope for empty and populated activities",
    );
  } finally {
    await context.close();
  }
}

async function verifyBrand(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const errors = [],
    writes = [];
  let current = {
    configured: true,
    version: 1,
    publication_version: 1,
    lifecycle_status: "active",
    community_name: "首页社区测试",
    brand_intro: "首页介绍测试",
    hero_mode: "image",
    hero_eyebrow: "首页眉题测试",
    hero_subtitle: "首页副标题测试",
    hero_image_url: "https://assets.example.com/banner.jpg",
    hero_image_alt: "",
    quick_tags: [],
  };
  page.on("pageerror", (error) => errors.push(error.message));
  await context.route("**/api/v1/xiangwan/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname.replace("/api/v1/xiangwan", "");
    if (path === "/admin/auth/status")
      return route.fulfill({
        json: envelope({
          enabled: true,
          authenticated: true,
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        }),
      });
    if (path === "/public-policies")
      return route.fulfill({ json: envelope({ published_versions: [] }) });
    assert.equal(path, "/admin/brand-profile");
    if (request.method() === "PATCH") {
      const body = request.postDataJSON();
      writes.push(body);
      assert.match(request.headers()["idempotency-key"], operationPattern);
      current = {
        ...current,
        ...body,
        version: current.version + 1,
        publication_version: current.publication_version + 1,
      };
    }
    return route.fulfill({ json: envelope(current) });
  });
  try {
    await page.goto(`${origin}/brand`);
    const preview = page.locator(".brand-phone-preview");
    await preview.getByRole("heading", { name: "首页社区测试" }).waitFor();
    assert.equal(await preview.locator(".brand-preview-image").count(), 1);
    for (const text of ["首页眉题测试", "首页副标题测试", "首页介绍测试"])
      await preview.getByText(text, { exact: true }).waitFor();
    await page.getByRole("button", { name: "发布首页配置", exact: true }).click();
    await page.getByText("首页品牌区已发布为第 2 版").waitFor();
    assert.equal(writes[0].hero_image_alt, "");
    await page.getByRole("button", { name: "移除横幅图片" }).click();
    for (const label of ["社区名称", "眉题", "副标题", "品牌介绍", "图片说明"])
      await page.getByLabel(label).fill("");
    await page.getByRole("button", { name: "添加主题" }).click();
    await page.getByRole("button", { name: "发布首页配置", exact: true }).click();
    await page.getByText("首页品牌区已发布为第 3 版").waitFor();
    assert.deepEqual(writes[1], {
      expected_version: 2,
      community_name: "",
      brand_intro: "",
      hero_mode: "image",
      hero_eyebrow: "",
      hero_subtitle: "",
      hero_image_url: "",
      hero_image_alt: "",
      quick_tags: [],
    });
    assert.equal(await preview.locator(".brand-preview-image, .brand-preview-copy").count(), 0);
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: homepage image and configured copy appear together; blank image, alt, copy and theme rows publish",
    );
  } finally {
    await context.close();
  }
}

let server, browser;
async function verifyCorrection(browser, origin, storageUnavailable) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const writes = [],
    errors = [];
  let resolved = false;
  const key = "xiangwan:coupon-correction-action:v1";
  page.on("pageerror", (error) => errors.push(error.message));
  if (storageUnavailable)
    await context.addInitScript((key) => {
      const original = Storage.prototype.setItem;
      Storage.prototype.setItem = function (name, value) {
        if (name === key) throw new DOMException("storage blocked", "SecurityError");
        return original.call(this, name, value);
      };
    }, key);
  await context.route("**/api/v1/xiangwan/admin/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1/xiangwan/admin", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (path === "/auth/status")
      return reply({
        enabled: true,
        authenticated: true,
        grants: [{ capability: "super_admin", scope_type: "tenant" }],
      });
    if (path === "/coupon-corrections")
      return reply({
        page: 1,
        page_size: 50,
        total: 1,
        as_of: "2026-10-01T10:00:00Z",
        items: [
          {
            entry_id: uuid(40),
            coupon_id: uuid(41),
            face_value_cents: "9007199254740993",
            source_checkin_event_id: uuid(42),
            related_entry_type: "redeemed",
            recorded_at: "2026-10-01T09:00:00Z",
            handling_status: resolved ? "resolved" : "processing",
            handling_version: resolved ? 2 : 1,
            evidence_kind: resolved ? "financial" : "",
            adjustment_cents: resolved ? "9007199254740993" : "0",
          },
        ],
      });
    assert.equal(path, `/coupon-corrections/${uuid(40)}/actions`);
    const command = {
      operation: request.headers()["idempotency-key"],
      body: request.postDataJSON(),
    };
    assert.match(command.operation, operationPattern);
    writes.push(command);
    if (!resolved) {
      resolved = true;
      return route.abort("connectionreset");
    }
    assert.deepEqual(command, writes[0]);
    return reply({ status: "resolved", version: 2 });
  });
  try {
    await page.goto(`${origin}/coupon-corrections`);
    await page.getByRole("button", { name: "登记结果", exact: true }).click();
    await page.getByLabel("外部凭证或工单引用").fill("official-case-1");
    await page.getByLabel("处理说明").fill("核对官方最终记录");
    await page.getByRole("button", { name: "核验并登记结案" }).click();
    if (storageUnavailable) {
      await page.getByText(/本次未提交/).waitFor();
      assert.equal(writes.length, 0);
      await page.getByRole("button", { name: "核验并登记结案" }).waitFor({ state: "visible" });
    } else {
      await page.getByText(/结果未知，请保持原操作并重试/).waitFor();
      assert.equal(writes[0].body.adjustment_cents, "9007199254740993");
      await page.reload();
      await page.getByRole("button", { name: "原样重试", exact: true }).click();
      await page.getByText(/人工处理证据已登记/).waitFor();
      assert.equal(writes.length, 2);
      assert.equal(await page.evaluate((key) => sessionStorage.getItem(key), key), null);
    }
    assert.deepEqual(errors, []);
    console.log(
      `PASS browser: Coupon correction ${storageUnavailable ? "blocks submission without recovery storage" : "replays exact money and evidence after reload"}`,
    );
  } finally {
    await context.close();
  }
}

async function verifyReviewEditor(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const errors = [],
    writes = [];
  let current = {
    relation_id: uuid(30),
    instance_id: sourceID,
    editable: true,
    expected_target_version: 7,
    photo_curation_version: 2,
    sort_order: 0,
    title: "本期回顾",
    description: "已发布摘要",
    video_url: "",
    photos: ["https://cdn.example.com/1.webp", "https://cdn.example.com/2.webp"],
    recording: {
      enabled: false,
      title: "已有纪要",
      subtitle: "已隐藏但保留",
      url: "https://feishu.cn/docx/old",
    },
    materials: {
      enabled: true,
      title: "现场资料",
      subtitle: "资料整理",
      url: "https://feishu.cn/drive/material",
    },
    video_channel: { finder_user_name: "sphExample", feed_id: "900719925474099312345" },
  };
  page.on("pageerror", (reason) => errors.push(reason.message));
  await context.route("**/api/v1/xiangwan/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1/xiangwan", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (request.method() === "GET") {
      if (path === "/admin/auth/status")
        return reply({
          enabled: true,
          authenticated: true,
          principal_id: uuid(99),
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        });
      if (path === `/admin/instances/${sourceID}`) return reply(source);
      if (path === `/admin/instances/${sourceID}/review-resources`)
        return reply({ items: [current] });
    }
    if (request.method() === "POST" && path.endsWith("/review-resources")) {
      const body = request.postData(),
        operation = request.headers()["idempotency-key"];
      writes.push({ body, operation });
      const parsed = JSON.parse(body);
      assert.equal(parsed.replaces_relation_id, uuid(30));
      assert.equal(parsed.expected_photo_curation_version, 2);
      assert.equal(parsed.recording.enabled, false);
      assert.equal(parsed.recording.title, "修改后纪要");
      assert.equal(parsed.materials.enabled, false);
      assert.deepEqual(parsed.photos, [
        "https://cdn.example.com/1.webp",
        "https://cdn.example.com/2.webp",
      ]);
      assert.equal(parsed.video_channel.feed_id, "900719925474099312345");
      assert.ok(operationPattern.test(operation));
      if (writes.length === 1) {
        current = { ...current, ...parsed, relation_id: uuid(31), photo_curation_version: 1 };
        return route.abort("failed");
      }
      assert.deepEqual(writes[1], writes[0]);
      return reply({ relation_id: uuid(31) });
    }
    return route.fulfill({ status: 404, json: { code: 1, message: "Unexpected fixture request" } });
  });
  try {
    await page.goto(`${origin}/past-activities/${sourceID}/resources`);
    await page.getByLabel("录音梳理显示名称").waitFor();
    assert.equal(await page.getByLabel("录音梳理显示名称").inputValue(), "已有纪要");
    assert.equal(await page.getByRole("checkbox", { name: "前台展示录音梳理" }).isChecked(), false);
    await page.getByLabel("录音梳理显示名称").fill("修改后纪要");
    await page.getByRole("checkbox", { name: "前台展示活动资料" }).uncheck();
    await page.getByRole("button", { name: "保存本期资料", exact: true }).click();
    await page.getByText(/结果尚未确认/).waitFor();
    await page.reload();
    await page.getByRole("button", { name: "重试上次保存", exact: true }).click();
    await page.getByText(/本期资料已更新/).waitFor();
    assert.equal(writes.length, 2);
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: review fields backfill, independent switches, immutable photos, native video IDs and exact retry after replacement",
    );
  } finally {
    await context.close();
  }
}

async function verifySessionCancellation(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const current = {
    ...source,
    instance: { ...source.instance, status: "published" },
    sessions: source.sessions.map((item) => ({ ...item, status: "published" })),
  };
  const writes = [],
    errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("dialog", (dialog) => dialog.accept());
  await context.route("**/api/v1/xiangwan/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace("/api/v1/xiangwan", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (request.method() === "GET") {
      if (path === "/admin/auth/status")
        return reply({
          enabled: true,
          authenticated: true,
          principal_id: uuid(99),
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        });
      if (path === "/public-policies") return reply({ published_versions: [] });
      if (path === `/admin/instances/${sourceID}`) return reply(current);
      if (path.endsWith("/review-status"))
        return reply({
          instance_id: sourceID,
          instance_status: "published",
          public_review_eligible: false,
          public_review_available: false,
          instance_review_document_count: 0,
          public_session_resource_count: 0,
          sessions: [],
        });
      if (path.endsWith("/roles") || path === "/admin/people")
        return reply({ items: [], total: 0, page: 1, page_size: 100 });
    }
    if (request.method() === "POST" && path.includes(`/sessions/${uuid(4)}/cancellation`)) {
      const body = request.postDataJSON(),
        operation = request.headers()["idempotency-key"];
      assert.match(operation, operationPattern);
      writes.push({ path, body, operation });
      if (path.endsWith("/cancellation-previews"))
        return reply({
          id: uuid(70),
          session_id: uuid(4),
          expected_session_version: 1,
          cancelled_registration_count: 2,
          pending_order_count: 1,
          unknown_payment_count: 1,
          requested_refund_cents: 8800,
          coupon_adjustment_count: 0,
          expires_at: "2099-01-01T00:00:00Z",
        });
      assert.deepEqual(body, {
        preview_id: uuid(70),
        expected_session_version: 1,
        reason: "场地调整",
      });
      current.sessions[0] = { ...current.sessions[0], status: "cancelled", version: 2 };
      if (writes.length === 2) return route.abort("failed");
      assert.deepEqual(writes[2], writes[1]);
      return reply({
        session: current.sessions[0],
        receipt_id: uuid(71),
        cancelled_at: "2026-10-02T04:00:00Z",
      });
    }
    return route.fulfill({ status: 404, json: { code: 1, message: "Unexpected fixture request" } });
  });
  try {
    await page.goto(`${origin}/activities/${sourceID}`);
    await page.getByRole("button", { name: "取消本场次", exact: true }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "继续", exact: true }).click();
    assert.equal(writes.length, 0);
    await dialog.getByRole("textbox").fill("场地调整");
    await dialog.getByRole("button", { name: "继续", exact: true }).click();
    await page.getByText(/再次操作将沿用原请求/).waitFor();
    await page.reload();
    await page.getByRole("button", { name: "核对取消结果", exact: true }).click();
    await page.getByText(/本场次已取消，请按报名名单/).waitFor();
    assert.equal(writes.length, 3);
    assert.equal(current.instance.status, "published");
    assert.deepEqual(
      current.sessions.slice(1).map((item) => item.status),
      ["published", "published"],
    );
    assert.equal(
      await page.evaluate(
        (key) => sessionStorage.getItem(key),
        `xiangwan-admin:session-cancellation:${uuid(4)}:v1`,
      ),
      null,
    );
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: required Session reason, exact preview, lost receipt reload recovery and unchanged sibling Sessions",
    );
  } finally {
    await context.close();
  }
}

async function verifyCheckinRevocation(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage();
  const registrationId = uuid(80),
    checkinId = uuid(81),
    writes = [],
    errors = [];
  let revoked = false;
  const detail = () => ({
    id: registrationId,
    series_id: seriesID,
    instance_id: sourceID,
    session_id: uuid(4),
    instance_title: "签到验证活动",
    session_title: "第一场",
    contact_name: "已脱敏",
    contact_phone: "****1234",
    participation_status: "confirmed",
    checkin_status: revoked ? "revoked" : "checked_in",
    checkin_id: checkinId,
    checked_in_at: "2026-10-02T04:00:00Z",
    instance_publication_version: 1,
    session_version: 1,
    privacy_policy_version: "test-v1",
  });
  const fact = () => ({
    checkin_id: checkinId,
    registration_id: registrationId,
    status: revoked ? "revoked" : "checked_in",
    version: revoked ? 2 : 1,
    checked_in_at: "2026-10-02T04:00:00Z",
    revocation_reason: revoked ? "误签纠正" : undefined,
  });
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("dialog", (dialog) => dialog.accept());
  await context.route("**/api/v1/xiangwan/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname.replace("/api/v1/xiangwan", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (request.method() === "GET") {
      if (path === "/admin/auth/status")
        return reply({
          enabled: true,
          authenticated: true,
          principal_id: uuid(99),
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        });
      if (path === `/admin/registrations/${registrationId}`) return reply(detail());
      if (path === `/admin/registrations/${registrationId}/checkin`) return reply(fact());
    }
    if (
      request.method() === "POST" &&
      path === `/admin/registrations/${registrationId}/checkin-revocations`
    ) {
      const body = request.postDataJSON(),
        operation = request.headers()["idempotency-key"];
      assert.match(operation, operationPattern);
      assert.deepEqual(body, { checkin_id: checkinId, expected_version: 1, reason: "误签纠正" });
      writes.push({ path, body, operation });
      revoked = true;
      if (writes.length === 1) return route.abort("failed");
      assert.deepEqual(writes[1], writes[0]);
      return reply({ checkin: fact(), event_id: uuid(82), duplicate: true });
    }
    return route.fulfill({ status: 404, json: { code: 1, message: "Unexpected fixture request" } });
  });
  try {
    await page.goto(`${origin}/registrations/${registrationId}`);
    await page.getByRole("button", { name: "撤销签到", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "继续", exact: true }).click();
    assert.equal(writes.length, 0);
    await dialog.getByRole("textbox").fill("误签纠正");
    await dialog.getByRole("button", { name: "继续", exact: true }).click();
    await page.getByText(/再次操作将沿用原请求核对/).waitFor();
    await page.reload();
    await page.getByRole("button", { name: "核对撤销结果", exact: true }).click();
    await page.getByText(/签到已撤销；原记录已保留/).waitFor();
    assert.equal(writes.length, 2);
    assert.equal(
      await page.evaluate(
        (key) => sessionStorage.getItem(key),
        `xiangwan-admin:checkin-revocation:${registrationId}:v1`,
      ),
      null,
    );
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: required Checkin reason, lost receipt reload, exact operation/version replay and authoritative revoked status",
    );
  } finally {
    await context.close();
  }
}

async function verifyPeopleBinding(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" });
  const page = await context.newPage(),
    writes = [],
    errors = [];
  const profileId = uuid(85),
    recovery = `xiangwan:people-binding:v1:${profileId}`;
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("dialog", (dialog) => dialog.accept());
  await context.route("**/api/v1/xiangwan/admin/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname.replace("/api/v1/xiangwan/admin", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (request.method() === "GET") {
      if (path === "/auth/status")
        return reply({
          enabled: true,
          authenticated: true,
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        });
      if (path === "/people")
        return reply({
          items: [
            {
              id: profileId,
              display_name: "本人绑定验证",
              headline: "人物",
              introduction: "公开简介",
              profile_status: "published",
              moderation_status: "approved",
              version: 2,
              has_active_binding: false,
              updated_at: "2026-10-02T00:00:00Z",
            },
          ],
          page: 1,
          page_size: 100,
          total: 1,
        });
      if (path === "/instances") return reply({ items: [], page: 1, page_size: 100, total: 0 });
    }
    if (request.method() === "POST" && path === `/people/${profileId}/binding-invitations`) {
      const operation = request.headers()["idempotency-key"],
        body = request.postDataJSON();
      assert.match(operation, operationPattern);
      assert.deepEqual(body, { expected_version: 2, reason: "核对本人" });
      writes.push({ path, operation, body });
      if (writes.length === 1) return route.abort("failed");
      assert.deepEqual(writes[1], writes[0]);
      return reply({
        id: operation,
        people_profile_id: profileId,
        profile_version: 2,
        status: "pending",
        version: 1,
        expires_at: "2026-10-03T00:00:00Z",
        code: operation + "." + "a".repeat(43),
      });
    }
    if (
      request.method() === "POST" &&
      path === `/people/${profileId}/binding-invitation-revocations`
    ) {
      const body = request.postDataJSON();
      assert.equal(body.binding_id, writes[0].operation);
      assert.equal(body.expected_version, 1);
      assert.equal(body.reason, "邀请取消");
      assert.match(request.headers()["idempotency-key"], operationPattern);
      return reply({
        id: body.binding_id,
        people_profile_id: profileId,
        profile_version: 2,
        status: "revoked",
        version: 2,
        expires_at: "2026-10-03T00:00:00Z",
      });
    }
    throw new Error(`Unexpected binding fixture ${path}`);
  });
  try {
    await page.goto(`${origin}/people`);
    await page.getByRole("button", { name: "邀请本人绑定", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "继续", exact: true }).click();
    assert.equal(writes.length, 0);
    await dialog.getByRole("textbox").fill("核对本人");
    await dialog.getByRole("button", { name: "继续", exact: true }).click();
    await page.getByText(/再次操作将沿用原请求核对/).waitFor();
    const saved = await page.evaluate((key) => sessionStorage.getItem(key), recovery);
    assert.ok(saved);
    assert.equal(JSON.parse(saved).code, undefined);
    await page.reload();
    await page.getByRole("button", { name: "邀请本人绑定", exact: true }).click();
    const input = page.getByRole("textbox", { name: "本人确认邀请码", exact: true });
    await input.waitFor();
    assert.equal(await input.inputValue(), writes[0].operation + "." + "a".repeat(43));
    assert.equal(await page.evaluate((key) => sessionStorage.getItem(key), recovery), null);
    await page.getByRole("button", { name: "关闭显示", exact: true }).click();
    await input.waitFor({ state: "detached" });
    await page.getByRole("button", { name: "撤销未使用邀请", exact: true }).click();
    await page.getByRole("dialog").getByRole("textbox").fill("邀请取消");
    await page.getByRole("dialog").getByRole("button", { name: "继续", exact: true }).click();
    await page.getByRole("button", { name: "邀请本人绑定", exact: true }).waitFor();
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: required binding reason, private invitation display, lost receipt reload, same request and unused invitation revocation",
    );
  } finally {
    await context.close();
  }
}

async function verifyHostApplications(browser, origin) {
  const context = await browser.newContext({ serviceWorkers: "block" }),
    page = await context.newPage(),
    errors = [],
    writes = [];
  const id = uuid(88),
    owner = uuid(89),
    key = "xiangwan:host-operation:v1";
  let rules = {
      version: 0,
      application_cycle: "",
      policy_version: "",
      requirements: "",
      benefits: "",
      enabled: false,
    },
    status = "pending";
  const item = () => ({
    id,
    application_cycle: "cycle-v1",
    policy_version: "host-v1",
    status,
    version: status === "pending" ? 1 : 2,
    submitted_at: "2026-10-02T00:00:00Z",
    updated_at: "2026-10-02T00:00:00Z",
  });
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => d.accept());
  await context.route("**/api/v1/xiangwan/admin/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname.replace("/api/v1/xiangwan/admin", "");
    const reply = (data) => route.fulfill({ json: envelope(data) });
    if (request.method() === "GET") {
      if (path === "/auth/status")
        return reply({
          enabled: true,
          authenticated: true,
          principal_id: owner,
          grants: [{ capability: "super_admin", scope_type: "tenant" }],
        });
      if (path === "/host-rules") return reply(rules);
      if (path === "/host-applications")
        return reply({
          items: status === "pending" ? [item()] : [],
          page: 1,
          page_size: 20,
          total: status === "pending" ? 1 : 0,
        });
      if (path === `/host-applications/${id}`) {
        assert.equal(new URL(request.url()).searchParams.get("purpose"), "host_application_review");
        return reply({
          ...item(),
          personal_introduction: "申请原文",
          relevant_experience: "经验原文",
          availability: "周末",
          contact_method: "private-review-contact",
        });
      }
    }
    if (request.method() === "POST") {
      const operation = request.headers()["idempotency-key"],
        body = request.postDataJSON();
      assert.match(operation, operationPattern);
      writes.push({ path, operation, body });
      const same = writes.filter((v) => v.path === path);
      if (path === "/host-rules") {
        assert.equal(body.expected_version, 0);
        rules = { ...body, version: 1 };
        if (same.length === 1) return route.abort("failed");
        assert.deepEqual(same[1], same[0]);
        return reply(rules);
      }
      if (path === `/host-applications/${id}/reviews`) {
        assert.deepEqual(body, {
          expected_version: 1,
          decision: "approved",
          comment: "经审核通过",
        });
        status = "approved";
        if (same.length === 1) return route.abort("failed");
        assert.deepEqual(same[1], same[0]);
        return reply(item());
      }
    }
    throw new Error(`Unexpected host fixture ${path}`);
  });
  try {
    await page.goto(`${origin}/host-applications`);
    const form = page.locator("form").first();
    await form.getByRole("button", { name: "保存规则版本" }).click();
    assert.equal(writes.length, 0);
    await page.getByLabel("申请周期").fill("cycle-v1");
    await page.getByLabel("规则版本").fill("host-v1");
    await page.getByLabel("申请要求").fill("客户申请要求");
    await page.getByLabel("可获支持").fill("客户支持说明");
    await page.getByLabel("发布原因").fill("客户确认规则");
    await page.getByLabel("开放申请").check();
    await page.getByRole("button", { name: "保存规则版本" }).click();
    await page.getByRole("button", { name: "重试原操作" }).waitFor();
    await page.reload();
    await page.getByRole("button", { name: "重试原操作" }).click();
    await page.getByText("规则版本已保存", { exact: true }).waitFor();
    assert.equal(await page.evaluate((k) => sessionStorage.getItem(k), key), null);
    await page.getByRole("button", { name: "查看申请", exact: true }).click();
    await page.getByText("private-review-contact", { exact: true }).waitFor();
    await page.getByRole("button", { name: "通过申请" }).click();
    assert.equal(writes.length, 2);
    await page.getByLabel("审核意见").fill("经审核通过");
    await page.getByRole("button", { name: "通过申请" }).click();
    await page.getByRole("button", { name: "重试原操作" }).waitFor();
    assert.equal(await page.getByText("private-review-contact", { exact: true }).count(), 0);
    const saved = await page.evaluate((k) => sessionStorage.getItem(k), key);
    assert.ok(saved);
    assert.equal(saved.includes("private-review-contact"), false);
    assert.equal(saved.includes("申请原文"), false);
    await page.reload();
    await page.getByRole("button", { name: "重试原操作" }).click();
    await page.getByText("审核结果已保存", { exact: true }).waitFor();
    assert.equal(await page.evaluate((k) => sessionStorage.getItem(k), key), null);
    assert.deepEqual(errors, []);
    console.log(
      "PASS browser: host rule required fields, audited private detail, review comment, reload after lost receipts and exact original operations",
    );
  } finally {
    await context.close();
  }
}

try {
  const started = await startServer();
  server = started.server;
  browser = await chromium.launch({
    headless: true,
    ...(process.env.XIANGWAN_BROWSER_EXECUTABLE
      ? { executablePath: process.env.XIANGWAN_BROWSER_EXECUTABLE }
      : {}),
  });
  await verifyCopy(browser, started.origin, false);
  await verifyCopy(browser, started.origin, true);
  await verifyCorrection(browser, started.origin, false);
  await verifyCorrection(browser, started.origin, true);
  await verifyPeople(browser, started.origin);
  await verifyBrand(browser, started.origin);
  await verifyReviewEditor(browser, started.origin);
  await verifySessionCancellation(browser, started.origin);
  await verifyCheckinRevocation(browser, started.origin);
  await verifyPeopleBinding(browser, started.origin);
  await verifyHostApplications(browser, started.origin);
} finally {
  if (browser) await browser.close();
  if (server) {
    const exited = new Promise((resolve) => server.once("exit", resolve));
    server.kill();
    await exited;
  }
}

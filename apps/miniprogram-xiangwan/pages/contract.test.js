"use strict";

const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");

const ROOT = path.resolve(__dirname, "..");

test("registered pages include the complete free-registration user chain", () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(ROOT, "app.json"), "utf8"));
  [
    "pages/index/index",
    "pages/people/index",
    "pages/person-detail/index",
    "pages/past-activities/index",
    "pages/activity-review/index",
    "pages/session-collection/index",
    "pages/external-resource/index",
    "pages/session-detail/index",
    "pages/registration/index",
    "pages/contact-setup/index",
    "pages/policies/index",
    "pages/registration-confirm/index",
    "pages/registration-success/index",
    "pages/my/index",
    "pages/my-benefits/index",
    "pages/my-registrations/index",
    "pages/registration-detail/index",
    "pages/my-orders/index",
    "pages/order-detail/index",
    "pages/my-coupons/index",
    "pages/my-favorites/index",
    "pages/checkin-credential/index",
  ].forEach((page) => assert.ok(manifest.pages.includes(page)));
});

test("home exposes only detail navigation and exact Session detail has no selector", () => {
  const home = fs.readFileSync(path.join(ROOT, "pages", "index", "index.wxml"), "utf8");
  const detail = fs.readFileSync(path.join(ROOT, "pages", "session-detail", "index.wxml"), "utf8");
  assert.doesNotMatch(home, /立即报名/);
  assert.match(home, /openDetail/);
  assert.doesNotMatch(detail, /picker|选择场次|session-selector/i);
  assert.match(detail, /startRegistration/);
  assert.match(detail, /openLocation/);
  assert.match(detail, /detail\.historyText/);
  assert.match(detail, /wx:if="{{detail\.registrationReady}}"/);
  assert.match(detail, /wx:elif="{{detail\.paidRegistrationPending}}"/);
});

test("session detail keeps the period cover when a custom activity also has content blocks", () => {
  const detail = fs.readFileSync(path.join(ROOT, "pages", "session-detail", "index.wxml"), "utf8");
  assert.match(detail, /<image wx:if="\{\{detail\.coverImageUrl\}\}"[^>]*detail-cover-fallback/);
  assert.doesNotMatch(detail, /detail\.coverImageUrl && !detail\.contentBlocks\.length/);
  assert.match(detail, /detail-content-flow/);
});

test("home keeps social counts and renders trusted small avatar stacks", () => {
  const home = fs.readFileSync(path.join(ROOT, "pages", "index", "index.wxml"), "utf8");
  assert.match(home, /item\.participantCountText/);
  assert.match(home, /item\.wantText/);
  assert.match(home, /class="event-participant-avatar"/);
  assert.match(home, /class="stack-avatar"/);
  assert.match(home, /binderror="onSocialAvatarError"/);
});

test("sitemap indexes only anonymous catalog pages", () => {
  const sitemap = JSON.parse(fs.readFileSync(path.join(ROOT, "sitemap.json"), "utf8"));
  assert.deepEqual(sitemap.rules, [
    { action: "allow", page: "pages/index/index" },
    { action: "allow", page: "pages/session-detail/index" },
    { action: "allow", page: "pages/past-activities/index" },
    { action: "allow", page: "pages/activity-review/index" },
    { action: "allow", page: "pages/people/index" },
    { action: "allow", page: "pages/person-detail/index" },
    { action: "disallow", page: "*" },
  ]);
});

test("global tabs keep the canonical home, past activities and my order", () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(ROOT, "app.json"), "utf8"));
  assert.deepEqual(
    manifest.tabBar.list.map(({ pagePath, text }) => ({ pagePath, text })),
    [
      { pagePath: "pages/index/index", text: "首页" },
      { pagePath: "pages/past-activities/index", text: "往期活动" },
      { pagePath: "pages/my/index", text: "我的" },
    ],
  );
});

test("every page keeps its viewport gutter and TDesign classes cross component boundaries", () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(ROOT, "app.json"), "utf8"));
  const styles = fs.readFileSync(path.join(ROOT, "app.wxss"), "utf8");
  const templates = manifest.pages
    .map((page) => fs.readFileSync(path.join(ROOT, `${page}.wxml`), "utf8"))
    .join("\n");

  assert.match(styles, /\.page\s*\{[^}]*padding-right:\s*28rpx;[^}]*padding-left:\s*28rpx;/s);
  assert.doesNotMatch(styles, /padding\s*:[^;]*env\(/);
  assert.doesNotMatch(templates, /<t-[^>]*\sclass="/s);
  assert.doesNotMatch(
    templates,
    /<(?:view|text|image|video|audio|block|scroll-view|canvas)[^>]*\st-class="/s,
  );
});

test("My hub exposes orders and exact order detail gates the payment action", () => {
  const hub = fs.readFileSync(path.join(ROOT, "pages", "my", "index.wxml"), "utf8");
  const hubSource = fs.readFileSync(path.join(ROOT, "pages", "my", "index.js"), "utf8");
  const orders = fs.readFileSync(path.join(ROOT, "pages", "my-orders", "index.wxml"), "utf8");
  const detail = fs.readFileSync(path.join(ROOT, "pages", "order-detail", "index.wxml"), "utf8");
  assert.match(hubSource, /我的报名/);
  assert.match(hubSource, /我的订单/);
  assert.match(orders, /item\.outcomeLabel/);
  assert.match(detail, /detail\.order\.paymentStatusLabel/);
  assert.match(detail, /detail\.order\.refund\.successfulRefundText/);
  assert.doesNotMatch(orders, /立即支付|继续支付|requestPayment/);
  assert.match(detail, /wx:if="{{detail\.order\.canPay}}"/);
  assert.match(detail, /bindtap="startPayment"/);
  assert.match(detail, /bindtap="verifyPayment"/);
});

test("My hub exposes coupons and favorites without inventing payment or coupon application", () => {
  const hubSource = fs.readFileSync(path.join(ROOT, "pages", "my", "index.js"), "utf8");
  const coupons = fs.readFileSync(path.join(ROOT, "pages", "my-coupons", "index.wxml"), "utf8");
  const favorites = fs.readFileSync(path.join(ROOT, "pages", "my-favorites", "index.wxml"), "utf8");
  const detail = fs.readFileSync(path.join(ROOT, "pages", "session-detail", "index.wxml"), "utf8");
  assert.match(hubSource, /我的优惠券/);
  assert.match(hubSource, /我的收藏/);
  assert.match(coupons, /item\.applicabilityText/);
  assert.match(favorites, /openSessions/);
  assert.match(detail, /favoriteSeries/);
  assert.doesNotMatch(coupons, /使用优惠券|立即支付|requestPayment/);
});

test("public People and owner Benefits are reachable without invented identity mutations", () => {
  const home = fs.readFileSync(path.join(ROOT, "pages", "index", "index.wxml"), "utf8");
  const people = fs.readFileSync(path.join(ROOT, "pages", "people", "index.js"), "utf8");
  const person = fs.readFileSync(path.join(ROOT, "pages", "person-detail", "index.js"), "utf8");
  const hubSource = fs.readFileSync(path.join(ROOT, "pages", "my", "index.js"), "utf8");
  const benefits = fs.readFileSync(path.join(ROOT, "pages", "my-benefits", "index.wxml"), "utf8");
  assert.match(home, /openPeople/);
  assert.match(people, /people_id=/);
  assert.match(person, /getPerson\(this\._peopleId\)/);
  assert.match(hubSource, /身份与贡献/);
  assert.match(benefits, /benefits\.currentRoles/);
  assert.match(benefits, /benefits\.hostContributionCount/);
  assert.doesNotMatch(benefits, /bindtap="(?:apply|edit)/i);
});

test("past review routes preserve exact Instance and explicit next-Session choice", () => {
  const list = fs.readFileSync(path.join(ROOT, "pages", "past-activities", "index.js"), "utf8");
  const review = fs.readFileSync(path.join(ROOT, "pages", "activity-review", "index.js"), "utf8");
  const reviewTemplate = fs.readFileSync(
    path.join(ROOT, "pages", "activity-review", "index.wxml"),
    "utf8",
  );
  assert.match(list, /instance_id=/);
  assert.match(review, /session_selection_required/);
  assert.match(review, /pages\/session-collection\/index\?instance_id=/);
  assert.match(review, /pages\/session-collection\/index\?review_instance_id=/);
  assert.match(review, /文件下载失败/);
  assert.match(reviewTemplate, /review\.contentBlocks/);
  assert.match(reviewTemplate, /活动详情/);
  assert.match(reviewTemplate, /!review\.documents\.length && !review\.contentBlocks\.length/);
  assert.match(reviewTemplate, /图片地址暂不可用/);
  assert.doesNotMatch(review, /candidateSessionIds\[0\]/);
});

test("sensitive registration drafts have no persistent-storage callsite", () => {
  const sources = [
    path.join(ROOT, "app.js"),
    path.join(ROOT, "pages", "registration", "index.js"),
    path.join(ROOT, "pages", "registration-confirm", "index.js"),
  ].map((file) => fs.readFileSync(file, "utf8"));
  assert.doesNotMatch(sources.join("\n"), /(?:setStorage|setString|setJSON)\s*\(/);
});

test("check-in plaintext is neither persisted, logged nor bound into WXML", () => {
  const source = fs.readFileSync(
    path.join(ROOT, "features", "checkin-credential", "controller.js"),
    "utf8",
  );
  const model = fs.readFileSync(
    path.join(ROOT, "features", "checkin-credential", "model.js"),
    "utf8",
  );
  const template = fs.readFileSync(
    path.join(ROOT, "pages", "checkin-credential", "index.wxml"),
    "utf8",
  );
  assert.doesNotMatch(`${source}\n${model}`, /(?:setStorage|setString|setJSON|console\.)\s*\(/);
  assert.doesNotMatch(template, /qrToken|qr_token/);
});

test("final confirmation shows time and price with consent and a dedicated policies link", () => {
  const confirmation = fs.readFileSync(
    path.join(ROOT, "pages", "registration-confirm", "index.wxml"),
    "utf8",
  );
  assert.match(confirmation, /draft\.startAt.*draft\.endAt/);
  assert.match(confirmation, /最终应付/);
  assert.match(confirmation, /已同意本次报名/);
  assert.doesNotMatch(confirmation, /政策版本/);
  assert.match(confirmation, /openPoliciesPage/);
  assert.doesNotMatch(confirmation, /policy-content/);
  assert.match(confirmation, /draft\.address/);
  assert.match(confirmation, /wx:key="fieldId"/);
});

test("registration form exposes policy content before contact inputs", () => {
  const form = fs.readFileSync(path.join(ROOT, "pages", "registration", "index.wxml"), "utf8");
  assert.match(form, /openPoliciesPage/);
  assert.doesNotMatch(form, /policy-content/);
  assert.ok(form.indexOf("openPoliciesPage") < form.indexOf('bindchange="updateContactName"'));
  assert.match(form, /昵称[\s\S]*class="required"[\s\S]*\*/);
  assert.match(form, /手机号[\s\S]*class="required"[\s\S]*\*/);
  assert.match(form, /手机号不做验证码校验/);
  const policies = fs.readFileSync(path.join(ROOT, "pages", "policies", "index.wxml"), "utf8");
  assert.match(policies, /contactText/);
  assert.match(policies, /openPrivacyContract/);
});

test("my registrations labels retained cards as stale when refresh fails", () => {
  const registrations = fs.readFileSync(
    path.join(ROOT, "pages", "my-registrations", "index.wxml"),
    "utf8",
  );
  assert.match(registrations, /errorMessage && items\.length/);
  assert.match(registrations, /上次成功加载/);
});

test("home labels retained cards as stale when a filter refresh fails", () => {
  const home = fs.readFileSync(path.join(ROOT, "pages", "index", "index.wxml"), "utf8");
  assert.match(home, /errorMessage && cards\.length/);
  assert.match(home, /上次成功加载/);
  assert.match(home, /可能不符合刚选择的筛选/);
});

test("registration list and detail render the authoritative street address", () => {
  const list = fs.readFileSync(path.join(ROOT, "pages", "my-registrations", "index.wxml"), "utf8");
  const detail = fs.readFileSync(
    path.join(ROOT, "pages", "registration-detail", "index.wxml"),
    "utf8",
  );
  assert.match(list, /item\.address/);
  assert.match(detail, /detail\.address/);
});

test("registration detail renders authoritative refund outcome and amounts", () => {
  const detail = fs.readFileSync(
    path.join(ROOT, "pages", "registration-detail", "index.wxml"),
    "utf8",
  );
  assert.match(detail, /detail\.refund\.statusLabel/);
  assert.match(detail, /detail\.refund\.requestedRefundText/);
  assert.match(detail, /detail\.refund\.successfulRefundText/);
  assert.match(detail, /detail\.refund\.resolvedAt/);
});

test("registration detail uses only its final server decision and persisted coupon fact", () => {
  const source = fs.readFileSync(
    path.join(ROOT, "pages", "registration-detail", "index.js"),
    "utf8",
  );
  const template = fs.readFileSync(
    path.join(ROOT, "pages", "registration-detail", "index.wxml"),
    "utf8",
  );
  assert.doesNotMatch(source, /applyCancellationPolicyCapability/);
  assert.match(template, /detail\.couponAdjustment/);
  assert.doesNotMatch(template, /cancellationResult\.couponAdjustment/);
});

test("registration detail restores the submitted contact with only a masked phone", () => {
  const detail = fs.readFileSync(
    path.join(ROOT, "pages", "registration-detail", "index.wxml"),
    "utf8",
  );
  assert.match(detail, /detail\.contactName/);
  assert.match(detail, /detail\.contactPhoneMasked/);
  assert.doesNotMatch(detail, /phoneE164|phone_e164/);
});

test("custom TDesign tabs track actual routes and recover a failed navigation", (t) => {
  const { TABS, createTabBarDefinition } = require("../custom-tab-bar/index");
  const { syncTabBar } = require("../utils/tab-bar");
  let pending;
  global.getCurrentPages = () => [{ route: TABS[1].value }];
  global.wx = {
    switchTab(options) {
      pending = options;
    },
    showToast() {},
  };
  t.after(() => {
    delete global.getCurrentPages;
    delete global.wx;
  });
  const def = createTabBarDefinition();
  const bar = {
    data: { ...def.data },
    ...def.methods,
    setData(changes) {
      this.data = { ...this.data, ...changes };
    },
  };
  bar.syncSelection();
  assert.equal(bar.data.value, TABS[1].value);
  bar.changeTab({ detail: { value: TABS[2].value } });
  assert.equal(bar.data.switching, true);
  assert.equal(bar.data.value, TABS[1].value);
  pending.fail();
  pending.complete();
  assert.equal(bar.data.switching, false);
  assert.equal(bar.data.value, TABS[1].value);
  bar.changeTab({ detail: { value: TABS[2].value } });
  pending.success();
  pending.complete();
  assert.equal(bar.data.value, TABS[2].value);
  syncTabBar({ getTabBar: () => bar }, TABS[0].value);
  assert.equal(bar.data.value, TABS[0].value);
  assert.ok(TABS.every((tab) => tab.icon));
  assert.equal(
    JSON.parse(fs.readFileSync(path.join(ROOT, "app.json"), "utf8")).tabBar.custom,
    true,
  );
});

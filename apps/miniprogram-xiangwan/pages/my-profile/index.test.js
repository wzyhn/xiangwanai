"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyProfilePageDefinition } = require("./index");

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function createPage(definition) {
  definition.setData = (changes) => {
    definition.data = { ...definition.data, ...changes };
  };
  return definition;
}

function createApp(overrides = {}) {
  return {
    runtimeReady: Promise.resolve(true),
    weconqAuth: { hasValidSession: () => true },
    globalData: {
      apiBaseUrl: "https://api.weconq.cn",
      profile: { nickname: "旧名", avatarUrl: "", etag: "e1" },
    },
    async refreshXiangwanProfile() {
      return this.globalData.profile;
    },
    ...overrides,
  };
}

test("profile editor requires authentication and offers only a way back", async (t) => {
  const app = createApp({ weconqAuth: { hasValidSession: () => false } });
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createPage(createMyProfilePageDefinition());
  page.onLoad();
  page.onShow();
  await flush();

  assert.equal(page.data.authenticated, false);
  assert.equal(page.data.needsLogin, true);
  assert.equal(page.data.loading, false);
});

test("profile editor hides an owner-bound profile after the principal changes", async (t) => {
  const app = createApp({
    getXiangwanProfile() {
      return null;
    },
    async refreshXiangwanProfile() {
      return null;
    },
  });
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createPage(createMyProfilePageDefinition());
  page.onLoad();
  page.onShow();
  await flush();
  await flush();

  assert.equal(page.data.profileReady, false);
  assert.equal(page.data.nickname, "");
  assert.equal(page.data.avatarUrl, "");
});

test("profile editor fills current values and validates nickname before saving", async (t) => {
  const saved = [];
  const api = {
    async updateNickname(value) {
      saved.push(value);
      return { nickname: value };
    },
    async uploadAvatar() {
      throw new Error("unused");
    },
  };
  const app = createApp();
  global.getApp = () => app;
  global.wx = {
    showToast() {},
    navigateBack() {},
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();

  assert.equal(page.data.nickname, "旧名");
  assert.equal(page.data.avatarInitial, "旧");

  page.onNicknameInput({ detail: { value: "   " } });
  await page.saveNickname();
  assert.equal(page.data.nicknameError, "昵称需为 1–64 个字符");
  assert.deepEqual(saved, []);

  page.onNicknameInput({ detail: { value: ` ${"长".repeat(65)} ` } });
  await page.saveNickname();
  assert.equal(page.data.nicknameError, "昵称需为 1–64 个字符");
  assert.deepEqual(saved, []);

  page.onNicknameInput({ detail: { value: "  新昵称  " } });
  await page.saveNickname();
  assert.deepEqual(saved, ["新昵称"]);
  assert.equal(page.data.nicknameError, "");
  assert.equal(app.globalData.profile.nickname, "新昵称");
  assert.equal(app.globalData.profile.etag, "e1");
  assert.equal(page.data.saving, false);
  page.onUnload();
});

test("profile editor refreshes the latest etag immediately before saving", async (t) => {
  const requests = [];
  const app = createApp({
    async refreshXiangwanProfile() {
      this.globalData.profile = { nickname: "旧名", avatarUrl: "", etag: "fresh-etag" };
      return this.globalData.profile;
    },
  });
  const api = {
    async updateNickname(nickname, etag) {
      requests.push({ nickname, etag });
      return { nickname, principalProfileETag: "next-etag" };
    },
    async uploadAvatar() {
      throw new Error("unused");
    },
  };
  global.getApp = () => app;
  global.wx = { showToast() {}, navigateBack() {} };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });

  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();
  page.onNicknameInput({ detail: { value: "新昵称" } });
  await page.saveNickname();

  assert.deepEqual(requests, [{ nickname: "新昵称", etag: "fresh-etag" }]);
  page.onUnload();
});

test("profile editor preserves the draft after a concurrent profile conflict", async (t) => {
  let refreshes = 0;
  const app = createApp({
    async refreshXiangwanProfile() {
      refreshes += 1;
      this.globalData.profile = {
        nickname: "服务端名字",
        avatarUrl: "",
        etag: `fresh-etag-${refreshes}`,
      };
      return this.globalData.profile;
    },
  });
  const api = {
    async updateNickname() {
      const error = new Error("conflict");
      error.statusCode = 409;
      throw error;
    },
    async uploadAvatar() {
      throw new Error("unused");
    },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);

  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();
  page.onNicknameInput({ detail: { value: "我的新昵称" } });
  await page.saveNickname();

  assert.equal(refreshes, 3);
  assert.equal(page.data.nickname, "我的新昵称");
  assert.equal(page.data.canSave, true);
  assert.equal(page.data.errorMessage, "资料已更新，请确认昵称后再保存");
  page.onUnload();
});

test("profile editor keeps the edited nickname when a background refresh lands", async (t) => {
  const app = createApp({ globalData: { apiBaseUrl: "https://api.weconq.cn", profile: null } });
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createPage(createMyProfilePageDefinition());
  page.onLoad();
  page.onShow();
  await flush();

  page.onNicknameInput({ detail: { value: "正在输入的名字" } });
  app.globalData.profile = { nickname: "服务端名字", avatarUrl: "", etag: "e2" };
  await page.refreshProfile(app);
  assert.equal(page.data.nickname, "正在输入的名字");
  assert.equal(page.data.avatarInitial, "正");
});

test("profile editor clears busy gates and fences a save after hide", async (t) => {
  let resolveSave;
  const api = {
    updateNickname() {
      return new Promise((resolve) => {
        resolveSave = resolve;
      });
    },
    async uploadAvatar() {
      throw new Error("unused");
    },
  };
  const app = createApp();
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();

  page.onNicknameInput({ detail: { value: "新昵称" } });
  const pending = page.saveNickname();
  await flush();
  assert.equal(page.data.saving, true);
  page.onHide();
  assert.equal(page.data.saving, false);
  page.onShow();
  resolveSave({ nickname: "新昵称", principalProfileETag: "e2" });
  await pending;
  assert.equal(app.globalData.profile.nickname, "新昵称");
  assert.equal(app.globalData.profile.etag, "e2");
  assert.equal(page.data.savedMessage, "", "late save updates owner cache but not hidden UI");
});

test("profile editor uploads the chosen avatar and echoes the joined url", async (t) => {
  const uploads = [];
  const api = {
    async updateNickname() {
      throw new Error("unused");
    },
    async uploadAvatar(filePath) {
      uploads.push(filePath);
      return { avatar_url: "/api/v1/xiangwan/avatars/new.png" };
    },
  };
  const app = createApp();
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();

  await page.onChooseAvatar({ detail: { avatarUrl: "wxfile://tmp/avatar.png" } });
  assert.deepEqual(uploads, ["wxfile://tmp/avatar.png"]);
  assert.equal(page.data.avatarUrl, "https://api.weconq.cn/api/v1/xiangwan/avatars/new.png");
  assert.equal(page.data.canSave, true, "changing only the avatar enables completion");
  assert.equal(
    app.globalData.profile.avatarUrl,
    "https://api.weconq.cn/api/v1/xiangwan/avatars/new.png",
  );
  let completed = 0;
  global.wx = {
    navigateBack() {
      completed += 1;
    },
  };
  t.after(() => delete global.wx);
  await page.saveNickname();
  assert.equal(completed, 1, "completion does not require a nickname PATCH");
  assert.equal(page.data.avatarUploading, false);
});

test("profile editor locks avatar selection during asynchronous size preflight", async (t) => {
  const uploads = [];
  let finishPreflight;
  const api = {
    async updateNickname() {
      throw new Error("unused");
    },
    async uploadAvatar(filePath) {
      uploads.push(filePath);
      return { avatar_url: "/api/v1/xiangwan/avatars/new.png" };
    },
  };
  const app = createApp();
  global.getApp = () => app;
  global.wx = {
    getFileSystemManager() {
      return {
        getFileInfo({ success }) {
          finishPreflight = success;
        },
      };
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();

  const first = page.onChooseAvatar({ detail: { avatarUrl: "wxfile://tmp/first.png" } });
  const second = page.onChooseAvatar({ detail: { avatarUrl: "wxfile://tmp/second.png" } });
  assert.equal(page.data.avatarUploading, true);
  finishPreflight({ size: 1024 });
  await Promise.all([first, second]);

  assert.deepEqual(uploads, ["wxfile://tmp/first.png"]);
  assert.equal(page.data.avatarUploading, false);
});

test("profile editor does not save a nickname while the avatar is uploading", async (t) => {
  let finishPreflight;
  const updates = [];
  const api = {
    async updateNickname(value) {
      updates.push(value);
      return { nickname: value, principalProfileETag: "e2" };
    },
    async uploadAvatar() {
      return { avatar_url: "/api/v1/xiangwan/avatars/new.png" };
    },
  };
  const app = createApp();
  global.getApp = () => app;
  global.wx = {
    getFileSystemManager() {
      return {
        getFileInfo({ success }) {
          finishPreflight = success;
        },
      };
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });

  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();
  page.onNicknameInput({ detail: { value: "新昵称" } });
  const pendingAvatar = page.onChooseAvatar({ detail: { avatarUrl: "wxfile://tmp/avatar.png" } });
  await page.saveNickname();
  assert.deepEqual(updates, []);
  finishPreflight({ size: 1024 });
  await pendingAvatar;
  page.onUnload();
});

test("profile editor blocks oversized avatars before upload", async (t) => {
  const toasts = [];
  const uploads = [];
  const api = {
    async updateNickname() {
      throw new Error("unused");
    },
    async uploadAvatar(filePath) {
      uploads.push(filePath);
      return { avatar_url: "/api/v1/xiangwan/avatars/new.png" };
    },
  };
  const app = createApp();
  global.getApp = () => app;
  global.wx = {
    showToast(options) {
      toasts.push(options);
    },
    getFileSystemManager() {
      return {
        getFileInfo({ success }) {
          success({ size: 1048577 });
        },
      };
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();

  await page.onChooseAvatar({ detail: { avatarUrl: "wxfile://tmp/big.png" } });
  assert.deepEqual(uploads, []);
  assert.deepEqual(toasts, [{ title: "图片不能超过 1MB，请换一张", icon: "none" }]);
  assert.equal(page.data.avatarUploading, false);
});

test("profile editor toasts on avatar upload failure and keeps the old avatar", async (t) => {
  const toasts = [];
  const api = {
    async updateNickname() {
      throw new Error("unused");
    },
    async uploadAvatar() {
      throw new Error("network down");
    },
  };
  const app = createApp({
    globalData: {
      apiBaseUrl: "https://api.weconq.cn",
      profile: {
        nickname: "小林",
        avatarUrl: "https://api.weconq.cn/api/v1/xiangwan/avatars/old.png",
        etag: "e1",
      },
    },
  });
  global.getApp = () => app;
  global.wx = {
    showToast(options) {
      toasts.push(options);
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();

  await page.onChooseAvatar({ detail: { avatarUrl: "wxfile://tmp/avatar.png" } });
  assert.deepEqual(toasts, [{ title: "头像上传失败，请稍后重试", icon: "none" }]);
  assert.equal(page.data.avatarUrl, "https://api.weconq.cn/api/v1/xiangwan/avatars/old.png");
  assert.equal(page.data.avatarUploading, false);

  await page.onChooseAvatar({ detail: {} });
  assert.deepEqual(toasts, [{ title: "头像上传失败，请稍后重试", icon: "none" }]);
});

test("profile editor can correct private tags and introduction through the moderated owner endpoint", async (t) => {
  let operation = null;
  const writes = [];
  const app = createApp({
    getXiangwanAuthBinding: () => ({ principalId: "user", generation: 1 }),
    getProfileExtensionOperation: () => operation,
    setProfileExtensionOperation: (value) => {
      operation = value;
      return value;
    },
    clearProfileExtensionOperation: () => {
      operation = null;
    },
  });
  const api = {
    getMyProfile: async () => ({
      profile: {
        version: 0,
        occupation: "",
        introduction: "",
        tags: [],
        visibility: { occupation: false, introduction: false, tags: false },
      },
    }),
    newIdempotencyKey: () => "33333333-3333-4333-8333-333333333333",
    updateProfileExtension: async (payload, key) => {
      writes.push({ payload, key });
      return { moderation_status: "pending_review" };
    },
  };
  global.getApp = () => app;
  t.after(() => {
    delete global.getApp;
  });
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();
  await flush();
  assert.equal(page.data.extensionReady, true);
  page.onExtensionTagsInput({ detail: { value: "徒步，AI" } });
  page.onExtensionIntroductionInput({ detail: { value: "喜欢探索" } });
  await page.saveExtension();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].key, "33333333-3333-4333-8333-333333333333");
  assert.deepEqual(writes[0].payload.tags, ["徒步", "AI"]);
  assert.equal(writes[0].payload.visibility.tags, false);
  assert.equal(operation, null);
  page.onUnload();
});

test("avatar errors from an obsolete image cannot hide the new image", () => {
  const page = createPage(createMyProfilePageDefinition());
  page.data.avatarUrl = "https://api.weconq.cn/api/v1/xiangwan/avatars/new.png";
  page.onAvatarError({
    currentTarget: {
      dataset: { url: "https://api.weconq.cn/api/v1/xiangwan/avatars/old.png" },
    },
  });
  assert.equal(page.data.avatarFailed, false);
  page.onAvatarError({ currentTarget: { dataset: { url: page.data.avatarUrl } } });
  assert.equal(page.data.avatarFailed, true);
  assert.ok(page.data.avatarUrl);
});

test("an avatar upload finishing after hide still updates its owner cache, but never another account", async (t) => {
  const app = createApp({
    binding: { principalId: "owner-a", generation: 1 },
    getXiangwanAuthBinding() {
      return this.binding;
    },
    mutations: 0,
    markXiangwanProfileMutation() {
      this.mutations++;
    },
  });
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  let finish;
  const api = {
    uploadAvatar() {
      return new Promise((resolve) => {
        finish = resolve;
      });
    },
  };
  const page = createPage(createMyProfilePageDefinition(api));
  page.onLoad();
  page.onShow();
  await flush();
  let pending = page.onChooseAvatar({ detail: { avatarUrl: "wxfile://first" } });
  await flush();
  page.onHide();
  finish({ avatar_url: "/api/v1/xiangwan/avatars/first.png" });
  await pending;
  assert.equal(
    app.globalData.profile.avatarUrl,
    "https://api.weconq.cn/api/v1/xiangwan/avatars/first.png",
  );
  assert.equal(page.data.avatarChanged, false);
  assert.equal(app.mutations, 2);
  page.onShow();
  await flush();
  assert.equal(page.data.canSave, true);
  pending = page.onChooseAvatar({ detail: { avatarUrl: "wxfile://second" } });
  await flush();
  app.binding = { principalId: "owner-b", generation: 2 };
  app.globalData.profile = { nickname: "乙", avatarUrl: "", etag: "b1" };
  finish({ avatar_url: "/api/v1/xiangwan/avatars/second.png" });
  await pending;
  assert.equal(app.globalData.profile.avatarUrl, "");
  assert.equal(app.globalData.profile.nickname, "乙");
  page.onUnload();
});

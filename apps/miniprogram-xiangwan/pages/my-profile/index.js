"use strict";

const {
  extractAvatarUrl,
  isXiangwanProfile,
  joinAvatarUrl,
  nicknameInitial,
  projectProfileBadge,
  validateNickname,
} = require("../../features/profile/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
const {
  buildPrivateProfileExtension,
  readProfileExtension,
} = require("../../features/profile/extension");
const { getUserMessage } = require("../../utils/errors");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function hasValidSession(app) {
  return Boolean(app && app.weconqAuth && app.weconqAuth.hasValidSession());
}

function readAuthBinding(app) {
  return app && typeof app.getXiangwanAuthBinding === "function"
    ? app.getXiangwanAuthBinding()
    : null;
}

function sameAuthBinding(left, right) {
  return (
    !left ||
    Boolean(
      right &&
        left.principalId &&
        left.principalId === right.principalId &&
        left.generation === right.generation,
    )
  );
}

function readProfile(app) {
  if (app && typeof app.getXiangwanProfile === "function") {
    return app.getXiangwanProfile();
  }
  const profile = app && app.globalData && app.globalData.profile;
  return isXiangwanProfile(profile) ? profile : null;
}

function writeProfile(app, patch) {
  const next = { ...(readProfile(app) || { nickname: "", avatarUrl: "", etag: "" }), ...patch };
  if (app && typeof app.setXiangwanProfile === "function") {
    return app.setXiangwanProfile(next) || readProfile(app);
  }
  app.globalData.profile = next;
  return app.globalData.profile;
}

function createMyProfilePageDefinition(api = xiangwanApi) {
  return {
    data: {
      authenticated: false,
      needsLogin: false,
      loading: true,
      avatarUrl: "",
      avatarInitial: "享",
      avatarUploading: false,
      avatarFailed: false,
      avatarChanged: false,
      nickname: "",
      nicknameError: "",
      saving: false,
      canSave: false,
      profileReady: false,
      savedMessage: "",
      errorMessage: "",
      extensionReady: false,
      extensionLoading: false,
      extensionSaving: false,
      extensionIntroduction: "",
      extensionTagsText: "",
      extensionError: "",
      extensionNotice: "",
      extensionPendingOperation: false,
      contactPhone: "",
      contactLoading: false,
      contactError: "",
    },

    onLoad() {
      this._active = true;
      this._nicknameDirty = false;
      this._avatarChanged = false;
      this._extensionDirty = false;
    },

    onShow() {
      this._active = true;
      const version = Number(this._showVersion || 0) + 1;
      this._showVersion = version;
      const app = resolveApp();
      this.setData({ loading: true, profileReady: false, canSave: false, errorMessage: "" });
      Promise.resolve((app && app.runtimeReady) || null)
        .then(() => {
          if (!this._active || version !== this._showVersion) return;
          const authenticated = hasValidSession(app);
          const binding = readAuthBinding(app);
          if (this._editorBinding && !sameAuthBinding(this._editorBinding, binding)) {
            this._nicknameDirty = false;
            this._avatarChanged = false;
            this.setData({ nickname: "", avatarUrl: "", avatarChanged: false, savedMessage: "" });
          }
          this._editorBinding = binding;
          this.setData({ authenticated, needsLogin: !authenticated });
          if (!authenticated) {
            this.setData({ loading: false });
            return;
          }
          void this.loadExtension(app, version);
          void this.loadRegistrationContact(app, version);
          this.presentProfile(app);
          if (isXiangwanProfile(readProfile(app))) {
            this.setData({ loading: false, profileReady: true });
          }
          void this.refreshProfile(app, version);
        })
        .catch((error) => {
          if (!this._active || version !== this._showVersion) return;
          this.setData({
            authenticated: false,
            needsLogin: true,
            loading: false,
            profileReady: false,
            errorMessage: getUserMessage(error, "登录状态读取失败，请稍后重试"),
          });
        });
    },

    presentProfile(app) {
      const badge = projectProfileBadge(readProfile(app));
      const nickname = this._nicknameDirty ? this.data.nickname : badge.nickname;
      const changes = {
        avatarUrl: badge.avatarUrl,
        avatarInitial: nicknameInitial(nickname),
        avatarFailed: false,
        avatarChanged: Boolean(this._avatarChanged),
      };
      if (!this._nicknameDirty) {
        changes.nickname = badge.nickname;
        this._savedNickname = badge.nickname;
        changes.canSave = Boolean(this._avatarChanged);
      }
      this.setData(changes);
    },

    async loadRegistrationContact(app, version = this._showVersion) {
      if (typeof api.getRegistrationContact !== "function") return;
      const binding = readAuthBinding(app);
      this.setData({ contactLoading: true, contactPhone: "", contactError: "" });
      try {
        const value = await api.getRegistrationContact();
        if (
          !this._active ||
          version !== this._showVersion ||
          !sameAuthBinding(binding, readAuthBinding(app))
        )
          return;
        this.setData({ contactPhone: value.phone_e164 || "", contactLoading: false });
      } catch (error) {
        if (
          this._active &&
          version === this._showVersion &&
          sameAuthBinding(binding, readAuthBinding(app))
        )
          this.setData({
            contactLoading: false,
            contactError: getUserMessage(error, "报名资料读取失败，请重试"),
          });
      }
    },

    async editRegistrationContact() {
      const app = resolveApp();
      if (!hasValidSession(app) || typeof app.ensureContactSetup !== "function") return;
      try {
        await app.ensureContactSetup(true);
      } catch (error) {
        if (this._active)
          this.setData({ contactError: getUserMessage(error, "请填写报名资料后保存") });
      }
    },

    async refreshProfile(app, expectedVersion = this._showVersion) {
      const binding = readAuthBinding(app);
      if (!app || typeof app.refreshXiangwanProfile !== "function") {
        if (this._active && expectedVersion === this._showVersion) {
          this.setData({
            loading: false,
            profileReady: false,
            errorMessage: "资料暂时无法读取，请稍后重试",
          });
        }
        return;
      }
      try {
        await app.refreshXiangwanProfile();
        if (
          !this._active ||
          expectedVersion !== this._showVersion ||
          !sameAuthBinding(binding, readAuthBinding(app))
        )
          return;
        const ready = isXiangwanProfile(readProfile(app));
        this.presentProfile(app);
        this.setData({
          loading: false,
          profileReady: ready,
          errorMessage: ready ? "" : "资料暂时无法读取，请稍后重试",
        });
      } catch (error) {
        if (
          !this._active ||
          expectedVersion !== this._showVersion ||
          !sameAuthBinding(binding, readAuthBinding(app))
        )
          return;
        this.setData({
          loading: false,
          profileReady: false,
          errorMessage: getUserMessage(error, "资料暂时无法读取，请稍后重试"),
        });
      }
    },

    onHide() {
      this._active = false;
      this._showVersion = Number(this._showVersion || 0) + 1;
      // The page can be retained after a background transition. Release the
      // local busy gates immediately; stale continuations remain fenced by
      // _active/_showVersion and cannot write UI state back on return.
      this.setData({
        avatarUploading: false,
        saving: false,
        extensionSaving: false,
        contactPhone: "",
      });
      this.clearNavigateBackTimer();
    },

    onUnload() {
      this._active = false;
      this._showVersion = Number(this._showVersion || 0) + 1;
      this.clearNavigateBackTimer();
    },

    async loadExtension(app, expectedVersion = this._showVersion) {
      if (!api || typeof api.getMyProfile !== "function") return;
      const binding = readAuthBinding(app);
      const isCurrent = () =>
        this._active &&
        expectedVersion === this._showVersion &&
        sameAuthBinding(binding, readAuthBinding(app));
      this.setData({ extensionLoading: true, extensionError: "" });
      try {
        const raw = await api.getMyProfile();
        if (!isCurrent()) return;
        const extension = readProfileExtension(raw);
        if (!extension) throw new Error("invalid profile extension");
        this._extensionRaw = raw;
        const display = extension.pending || extension.profile;
        const changes = {
          extensionReady: true,
          extensionPendingOperation: Boolean(
            app &&
              typeof app.getProfileExtensionOperation === "function" &&
              app.getProfileExtensionOperation(),
          ),
          extensionNotice: extension.pending
            ? "资料正在审核；审核完成前暂不能提交新的标签或简介。"
            : "标签和简介仅自己可见；新提交内容会经过审核。",
        };
        if (!this._extensionDirty) {
          changes.extensionIntroduction = display.introduction;
          changes.extensionTagsText = display.tags.join("，");
        }
        this.setData(changes);
      } catch (error) {
        if (isCurrent())
          this.setData({
            extensionReady: false,
            extensionError: getUserMessage(error, "标签和简介暂不可读取，请稍后重试"),
          });
      } finally {
        if (isCurrent()) this.setData({ extensionLoading: false });
      }
    },

    onExtensionIntroductionInput(event) {
      this._extensionDirty = true;
      this.clearNavigateBackTimer();
      this.setData({
        extensionIntroduction: String((event && event.detail && event.detail.value) || ""),
        extensionError: "",
      });
    },

    onExtensionTagsInput(event) {
      this._extensionDirty = true;
      this.clearNavigateBackTimer();
      this.setData({
        extensionTagsText: String((event && event.detail && event.detail.value) || ""),
        extensionError: "",
      });
    },

    restoreExtensionOperation() {
      const app = resolveApp();
      const operation =
        app && typeof app.getProfileExtensionOperation === "function"
          ? app.getProfileExtensionOperation()
          : null;
      if (!operation || !operation.payload) return;
      this._extensionDirty = true;
      this.setData({
        extensionIntroduction: String(operation.payload.introduction || ""),
        extensionTagsText: Array.isArray(operation.payload.tags)
          ? operation.payload.tags.join("，")
          : "",
        extensionError: "",
        extensionPendingOperation: true,
      });
    },

    async saveExtension() {
      if (
        !this.data.extensionReady ||
        this.data.extensionSaving ||
        this.data.saving ||
        this.data.avatarUploading
      )
        return;
      const app = resolveApp();
      if (!hasValidSession(app) || !app || typeof app.getProfileExtensionOperation !== "function") {
        this.setData({ extensionError: "登录状态已变化，请重新登录" });
        return;
      }
      const version = Number(this._showVersion || 0);
      const binding = readAuthBinding(app);
      const isCurrent = () =>
        this._active &&
        version === this._showVersion &&
        sameAuthBinding(binding, readAuthBinding(app));
      const fingerprint = JSON.stringify([
        this.data.extensionIntroduction,
        this.data.extensionTagsText,
      ]);
      this.setData({ extensionSaving: true, extensionError: "", extensionNotice: "" });
      try {
        let operation = app.getProfileExtensionOperation();
        if (operation && operation.fingerprint !== fingerprint) {
          this.setData({ extensionError: "上次提交结果待确认，请恢复原内容并重试" });
          return;
        }
        if (!operation) {
          const latest = await api.getMyProfile();
          if (!isCurrent()) return;
          const candidate = buildPrivateProfileExtension(
            latest,
            this.data.extensionIntroduction,
            this.data.extensionTagsText,
          );
          if (!candidate.ok) {
            this.setData({ extensionError: candidate.message });
            return;
          }
          if (candidate.unchanged) {
            this._extensionDirty = false;
            this.setData({
              extensionNotice: readProfileExtension(latest).pending
                ? "资料仍在审核中"
                : "资料已是最新",
            });
            return;
          }
          operation = app.setProfileExtensionOperation({
            key: api.newIdempotencyKey(),
            fingerprint,
            payload: candidate.payload,
          });
          if (!operation) {
            this.setData({ extensionError: "资料提交暂不可用，请稍后重试" });
            return;
          }
          this.setData({ extensionPendingOperation: true });
        }
        if (!isCurrent()) return;
        const receipt = await api.updateProfileExtension(operation.payload, operation.key);
        if (!isCurrent()) return;
        app.clearProfileExtensionOperation();
        this._extensionDirty = false;
        this.setData({
          extensionPendingOperation: false,
          extensionNotice:
            receipt.moderation_status === "pending_review"
              ? "资料已提交审核，审核通过后更新"
              : "资料已保存",
        });
        await this.loadExtension(app, version);
      } catch (error) {
        if (isCurrent())
          this.setData({ extensionError: getUserMessage(error, "资料提交失败，请使用原内容重试") });
      } finally {
        if (isCurrent()) this.setData({ extensionSaving: false });
      }
    },

    clearNavigateBackTimer() {
      if (this._navigateBackTimer) {
        clearTimeout(this._navigateBackTimer);
        this._navigateBackTimer = null;
      }
    },

    goBack() {
      if (typeof wx === "undefined" || typeof wx.navigateBack !== "function") return;
      wx.navigateBack({
        delta: 1,
        fail() {
          if (typeof wx.switchTab === "function") wx.switchTab({ url: "/pages/my/index" });
        },
      });
    },

    onNicknameInput(event) {
      this._nicknameDirty = true;
      const nickname = String((event && event.detail && event.detail.value) || "");
      const validation = validateNickname(nickname);
      this.setData({
        nickname,
        nicknameError: "",
        avatarInitial: nicknameInitial(nickname),
        canSave:
          validation.ok &&
          (this._avatarChanged || validation.nickname !== String(this._savedNickname || "")),
        savedMessage: "",
      });
    },

    async onChooseAvatar(event) {
      const filePath = String((event && event.detail && event.detail.avatarUrl) || "").trim();
      if (!filePath || this.data.avatarUploading || this.data.saving) return;
      const version = Number(this._showVersion || 0);
      const app = resolveApp();
      const binding = readAuthBinding(app);
      const isOwner = () => hasValidSession(app) && sameAuthBinding(binding, readAuthBinding(app));
      const isCurrent = () => this._active && version === this._showVersion && isOwner();
      if (!hasValidSession(app)) {
        this.setData({ authenticated: false, needsLogin: true });
        return;
      }
      this.setData({ avatarUploading: true, errorMessage: "", savedMessage: "" });
      try {
        // Lock before the asynchronous size preflight so a second chooser
        // callback cannot start a competing immutable upload.
        if (await this.avatarFileTooLarge(filePath)) {
          if (typeof wx !== "undefined" && typeof wx.showToast === "function") {
            wx.showToast({ title: "图片不能超过 1MB，请换一张", icon: "none" });
          }
          return;
        }
        if (!isCurrent()) return;
        if (typeof app.markXiangwanProfileMutation === "function") {
          app.markXiangwanProfileMutation();
        }
        const result = await api.uploadAvatar(filePath);
        const avatarUrl = joinAvatarUrl(
          app.globalData && app.globalData.apiBaseUrl,
          extractAvatarUrl(result),
        );
        if (!avatarUrl) throw new Error("avatar url missing");
        if (!isOwner()) return;
        // Invalidate reads started while the upload was in flight as well as
        // reads started before it. Cache belongs to this captured account.
        if (typeof app.markXiangwanProfileMutation === "function")
          app.markXiangwanProfileMutation();
        writeProfile(app, { avatarUrl });
        this._avatarChanged = true;
        if (isCurrent())
          this.setData({
            avatarUrl,
            avatarFailed: false,
            avatarChanged: true,
            canSave: validateNickname(this.data.nickname).ok,
            savedMessage: "头像已更新，请点击完成",
          });
      } catch (_error) {
        if (isCurrent() && typeof wx !== "undefined" && typeof wx.showToast === "function") {
          wx.showToast({ title: "头像上传失败，请稍后重试", icon: "none" });
        }
      } finally {
        if (isCurrent()) this.setData({ avatarUploading: false });
      }
    },

    avatarFileTooLarge(filePath) {
      return new Promise((resolve) => {
        if (typeof wx === "undefined") return resolve(false);
        const done = (size) => resolve(Number(size) > 1048576);
        const manager =
          typeof wx.getFileSystemManager === "function" ? wx.getFileSystemManager() : null;
        if (manager && typeof manager.getFileInfo === "function") {
          manager.getFileInfo({
            filePath,
            success: (res) => done(res && res.size),
            fail: () => resolve(false),
          });
          return;
        }
        if (typeof wx.getFileInfo === "function") {
          wx.getFileInfo({
            filePath,
            success: (res) => done(res && res.size),
            fail: () => resolve(false),
          });
          return;
        }
        resolve(false);
      });
    },

    onAvatarError(event) {
      if (!this.data.avatarUrl) return;
      const failedUrl =
        event &&
        event.currentTarget &&
        event.currentTarget.dataset &&
        event.currentTarget.dataset.url;
      if (failedUrl && failedUrl !== this.data.avatarUrl) return;
      this.setData({ avatarFailed: true });
    },

    retryAvatar() {
      if (this.data.avatarUploading || this.data.saving) return;
      this.setData({ avatarFailed: false });
      void this.refreshProfile(resolveApp());
    },

    async saveNickname() {
      if (this.data.saving || this.data.avatarUploading) return;
      const validation = validateNickname(this.data.nickname);
      if (!validation.ok) {
        this.setData({ nicknameError: validation.message });
        return;
      }
      if (!this.data.profileReady || !this.data.canSave) return;
      const app = resolveApp();
      if (!hasValidSession(app)) {
        this.setData({ authenticated: false, needsLogin: true });
        return;
      }
      if (validation.nickname === String(this._savedNickname || "") && this._avatarChanged) {
        this._avatarChanged = false;
        this.setData({ avatarChanged: false, canSave: false, savedMessage: "资料已保存" });
        if (typeof wx !== "undefined" && typeof wx.navigateBack === "function")
          wx.navigateBack({ delta: 1 });
        return;
      }
      const version = Number(this._showVersion || 0);
      const binding = readAuthBinding(app);
      const isOwner = () => hasValidSession(app) && sameAuthBinding(binding, readAuthBinding(app));
      const isCurrent = () => this._active && version === this._showVersion && isOwner();
      this.setData({ saving: true, nicknameError: "", errorMessage: "", savedMessage: "" });
      try {
        if (typeof app.markXiangwanProfileMutation === "function") {
          app.markXiangwanProfileMutation();
        }
        let currentProfile = readProfile(app) || { nickname: "", avatarUrl: "", etag: "" };
        // Nickname/avatar writes share the Principal profile etag. Refresh
        // immediately before PATCH so a profile read started during login or
        // a prior avatar update cannot submit an obsolete compare-and-swap
        // value and surface an avoidable 409.
        if (typeof app.refreshXiangwanProfile === "function") {
          const latestProfile = await app.refreshXiangwanProfile();
          if (isXiangwanProfile(latestProfile)) currentProfile = latestProfile;
        }
        if (!isCurrent()) return;
        const { nickname, principalProfileETag } = await api.updateNickname(
          validation.nickname,
          currentProfile.etag,
        );
        if (!isOwner()) return;
        if (typeof app.markXiangwanProfileMutation === "function")
          app.markXiangwanProfileMutation();
        writeProfile(app, { nickname, etag: principalProfileETag || currentProfile.etag });
        if (!isCurrent()) return;
        this._savedNickname = nickname;
        this._nicknameDirty = false;
        this._avatarChanged = false;
        this.setData({ canSave: false, avatarChanged: false, savedMessage: "资料已保存" });
        if (typeof wx !== "undefined" && typeof wx.showToast === "function") {
          wx.showToast({ title: "资料已更新", icon: "success", duration: 800 });
        }
        this.clearNavigateBackTimer();
        this._navigateBackTimer = setTimeout(() => {
          this._navigateBackTimer = null;
          if (typeof wx !== "undefined" && typeof wx.navigateBack === "function") {
            wx.navigateBack({ delta: 1 });
          }
        }, 800);
      } catch (error) {
        if (isCurrent()) {
          if (Number(error && error.statusCode) === 409) {
            // A concurrent avatar/nickname update is a real CAS conflict. Pull
            // the new etag into the app cache, but keep the user's draft so
            // they can explicitly review and save it again.
            if (typeof app.refreshXiangwanProfile === "function") {
              try {
                await app.refreshXiangwanProfile();
              } catch (_refreshError) {
                // Keep the conflict copy visible even when the follow-up read
                // is unavailable; the next explicit save will retry the read.
              }
            }
            if (!isCurrent()) return;
            this.presentProfile(app);
            this.setData({ errorMessage: "资料已更新，请确认昵称后再保存" });
          } else {
            this.setData({ errorMessage: getUserMessage(error, "保存失败，请稍后重试") });
          }
        }
      } finally {
        if (isCurrent()) this.setData({ saving: false });
      }
    },
  };
}

if (typeof Page === "function") Page(createMyProfilePageDefinition());

module.exports = { createMyProfilePageDefinition };

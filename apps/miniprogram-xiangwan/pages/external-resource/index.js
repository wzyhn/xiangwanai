"use strict";

const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function normalizeResolvedExternalUrl(value) {
  const raw = String(value || "").trim();
  if (raw.length > 4096 || /%(?:00|0a|0d)/i.test(raw) || !/^https:\/\/[^\s]+$/.test(raw)) {
    return "";
  }
  return raw;
}

function resolveExternalResourceUrl(review, blockId) {
  const block = resolveExternalResourceBlock(review, blockId);
  return block ? normalizeResolvedExternalUrl(block.external_url) : "";
}

function resolveExternalResourceBlock(review, blockId) {
  const matches = [];
  const documents = Array.isArray(review && review.documents) ? review.documents : [];
  for (const document of documents) {
    const blocks = Array.isArray(document && document.blocks) ? document.blocks : [];
    for (const block of blocks) {
      if (
        String((block && block.block_id) || "")
          .trim()
          .toLowerCase() !== blockId
      )
        continue;
      matches.push(block);
    }
  }
  if (matches.length !== 1) return null;
  const block = matches[0];
  if (
    String(block.type || "").trim() !== "link" ||
    String(block.availability || "").trim() !== "available"
  ) {
    return null;
  }
  return block;
}

function validVideoChannel(value) {
  return (
    value &&
    typeof value.finder_user_name === "string" &&
    /^sph[^\s\x00-\x20\x7f-\uffff]{1,125}$/.test(value.finder_user_name) &&
    typeof value.feed_id === "string" &&
    /^[^\s\x00-\x20\x7f-\uffff]{1,512}$/.test(value.feed_id)
  );
}

function createExternalResourcePageDefinition(api = xiangwanApi) {
  return {
    data: { url: "", videoChannel: null, loading: true, errorMessage: "" },

    onLoad(options = {}) {
      this._active = true;
      try {
        this._instanceId = canonicalUUID(options.instance_id, "活动期次");
        this._sessionId = options.session_id ? canonicalUUID(options.session_id, "活动场次") : "";
        this._blockId = canonicalUUID(options.block_id, "外部资源");
      } catch (error) {
        this.setData({
          url: "",
          loading: false,
          errorMessage: getUserMessage(error, "外部资源链接无效或已失效"),
        });
        return;
      }
      void this.loadResource();
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    async loadResource() {
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData({ url: "", videoChannel: null, loading: true, errorMessage: "" });
      try {
        const review = await api.getPublicReview(this._instanceId, this._sessionId);
        if (!this._active || version !== this._requestVersion) return;
        const block = resolveExternalResourceBlock(review, this._blockId);
        if (block && validVideoChannel(block.video_channel) && !block.external_url) {
          this.setData({ videoChannel: { ...block.video_channel }, url: "", errorMessage: "" });
          return;
        }
        const url =
          block && !block.video_channel ? normalizeResolvedExternalUrl(block.external_url) : "";
        this.setData(
          url ? { url, errorMessage: "" } : { url: "", errorMessage: "外部资源链接无效或已失效" },
        );
      } catch (error) {
        if (this._active && version === this._requestVersion) {
          this.setData({
            url: "",
            errorMessage: getUserMessage(error, "外部资源链接无效或已失效"),
          });
        }
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false });
        }
      }
    },

    retry() {
      if (this._instanceId && this._blockId) void this.loadResource();
    },

    openVideoChannel() {
      const channel = this.data.videoChannel;
      if (!this._active || !validVideoChannel(channel)) return;
      if (typeof wx === "undefined" || typeof wx.openChannelsActivity !== "function") {
        this.setData({ errorMessage: "当前微信版本暂不支持视频号回顾，请更新微信后重新打开" });
        return;
      }
      wx.openChannelsActivity({
        finderUserName: channel.finder_user_name,
        feedId: channel.feed_id,
        fail: () => {
          if (this._active)
            this.setData({ errorMessage: "视频号回顾暂时无法打开，请重新核验后重试" });
        },
      });
    },
  };
}

if (typeof Page === "function") Page(createExternalResourcePageDefinition());

module.exports = { createExternalResourcePageDefinition, resolveExternalResourceUrl };

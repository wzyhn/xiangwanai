"use strict";

const { projectSessionCollection } = require("../../features/past-activities/model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

const REVIEWABLE_SESSION_STATUSES = new Set(["ended", "archived"]);

function keepReviewableSessions(collection) {
  const sessions = collection.sessions.filter((session) =>
    REVIEWABLE_SESSION_STATUSES.has(session.status),
  );
  const directSessionId = sessions.length === 1 ? sessions[0].sessionId : "";
  return {
    ...collection,
    action:
      sessions.length === 0
        ? "unavailable"
        : sessions.length === 1
          ? "session_detail"
          : "session_selection_required",
    directSessionId,
    sessions,
  };
}

function createSessionCollectionPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      errorMessage: "",
      emptyMessage: "",
      collection: null,
      canRetry: false,
      heading: {
        eyebrow: "选择场次",
        title: "选择活动场次",
        subtitle: "多个场次不会自动代选，请按实际时间选择",
      },
    },

    onLoad(options = {}) {
      this._active = true;
      const instanceId = String(options.instance_id || "").trim();
      const seriesId = String(options.series_id || "").trim();
      const reviewInstanceId = String(options.review_instance_id || "").trim();
      if ([instanceId, seriesId, reviewInstanceId].filter(Boolean).length !== 1) {
        this.setData({ loading: false, errorMessage: "场次链接无效" });
        return;
      }
      try {
        if (seriesId) {
          this._sourceKind = "series";
          this._sourceId = canonicalUUID(seriesId, "活动系列");
          this.setData({
            heading: {
              eyebrow: "收藏活动",
              title: "选择收藏活动场次",
              subtitle: "展示该活动系列当前公开期次，请按实际时间选择",
            },
          });
        } else if (reviewInstanceId) {
          this._sourceKind = "review";
          this._sourceId = canonicalUUID(reviewInstanceId, "活动期次");
          this.setData({
            heading: {
              eyebrow: "活动资料",
              title: "选择场次资料",
              subtitle: "每个场次的资料相互独立，请选择实际参加的场次",
            },
          });
        } else {
          this._sourceKind = "instance";
          this._sourceId = canonicalUUID(instanceId, "活动期次");
          this.setData({
            heading: {
              eyebrow: "后续场次",
              title: "选择下一期场次",
              subtitle: "多个场次不会自动代选，请按实际时间选择",
            },
          });
        }
        this.setData({ canRetry: true });
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "场次链接无效") });
        return;
      }
      void this.loadSessions();
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    async loadSessions() {
      if (!this._sourceId) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData({ loading: true, errorMessage: "", emptyMessage: "" });
      try {
        const response =
          this._sourceKind === "series"
            ? await api.getSeriesSessions(this._sourceId)
            : await api.getInstanceSessions(this._sourceId);
        const projected = projectSessionCollection(response);
        const collection =
          this._sourceKind === "review" ? keepReviewableSessions(projected) : projected;
        if (!this._active || version !== this._requestVersion) return;
        if (collection.action === "session_detail" && collection.directSessionId) {
          this.setData({ collection });
          if (this.navigateToSession(collection.directSessionId, true, collection)) return;
        }
        this.setData({
          collection,
          emptyMessage: collection.sessions.length
            ? ""
            : this._sourceKind === "series"
              ? "该收藏暂时没有可查看的场次"
              : this._sourceKind === "review"
                ? "本期暂时没有可查看的场次资料"
                : "下一期暂时没有可查看的场次",
        });
      } catch (error) {
        if (this._active && version === this._requestVersion) {
          this.setData({ errorMessage: getUserMessage(error, "活动场次加载失败") });
        }
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false });
        }
      }
    },

    retry() {
      if (this.data.canRetry) void this.loadSessions();
    },

    openSession(event) {
      this.navigateToSession(event.currentTarget.dataset.sessionId, false);
    },

    navigateToSession(value, replace, collectionOverride = null) {
      let sessionId;
      try {
        sessionId = canonicalUUID(value, "活动场次");
      } catch (_error) {
        return false;
      }
      if (typeof wx === "undefined") return false;
      const collection = collectionOverride || this.data.collection;
      const selectedSession =
        collection && Array.isArray(collection.sessions)
          ? collection.sessions.find((session) => session.sessionId === sessionId)
          : null;
      const opensReview =
        this._sourceKind === "review" || Boolean(selectedSession && selectedSession.reviewPath);
      let reviewInstanceId = "";
      if (opensReview) {
        try {
          reviewInstanceId = canonicalUUID(
            this._sourceKind === "review" ? this._sourceId : collection && collection.instanceId,
            "活动期次",
          );
        } catch (_error) {
          return false;
        }
      }
      const url = opensReview
        ? `/pages/activity-review/index?instance_id=${encodeURIComponent(
            reviewInstanceId,
          )}&session_id=${encodeURIComponent(sessionId)}`
        : `/pages/session-detail/index?session_id=${encodeURIComponent(sessionId)}`;
      const navigate =
        replace && typeof wx.redirectTo === "function"
          ? wx.redirectTo
          : typeof wx.navigateTo === "function"
            ? wx.navigateTo
            : null;
      if (!navigate) return false;
      navigate.call(wx, { url });
      return true;
    },
  };
}

if (typeof Page === "function") Page(createSessionCollectionPageDefinition());

module.exports = { createSessionCollectionPageDefinition };

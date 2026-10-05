"use strict";

const { createClient, createRequestError, generateIdempotencyKey } = require("mp-http");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function readGlobalData(key) {
  const app = resolveApp();
  return String((app && app.globalData && app.globalData[key]) || "").trim();
}

async function recoverAuthentication() {
  const app = resolveApp();
  if (!app || typeof app.ensureAuthenticated !== "function") return false;
  if (typeof app.resetRejectedSession === "function") {
    app.resetRejectedSession();
  }
  await app.ensureAuthenticated();
  return true;
}

const request = createClient({
  baseUrl: () => readGlobalData("apiBaseUrl"),
  token: () => readGlobalData("accessToken"),
  onAuthRequired: recoverAuthentication,
});

// multipart 上传走 wx.uploadFile(request 只发 JSON)。鉴权与 request 一致
// (Bearer globalData.accessToken);响应按同一套 code/data 信封解析,失败
// reject 与 mp-http 相同的 createRequestError 风格(statusCode/code)。
function uploadFile(options = {}) {
  return uploadFileAttempt(options, false);
}

async function uploadFileAttempt(options, retriedAfterAuth) {
  const baseUrl = readGlobalData("apiBaseUrl").replace(/\/+$/, "");
  const url = String(options.url || "").trim();
  const filePath = String(options.filePath || "").trim();
  if (!baseUrl || !url || !filePath) {
    throw createRequestError("invalid upload request", { code: 10001 });
  }
  const header = { ...(options.header || {}) };
  const token = readGlobalData("accessToken");
  if (token) header.Authorization = `Bearer ${token}`;
  try {
    return await new Promise((resolve, reject) => {
      wx.uploadFile({
        url: `${baseUrl}${url}`,
        filePath,
        name: String(options.name || "file"),
        header,
        success(response) {
          const statusCode = Number((response && response.statusCode) || 0);
          let body = null;
          try {
            body = JSON.parse(String((response && response.data) || ""));
          } catch (_error) {
            body = null;
          }
          const validEnvelope = Boolean(
            body &&
              typeof body === "object" &&
              !Array.isArray(body) &&
              Number.isSafeInteger(body.code),
          );
          if (statusCode >= 200 && statusCode < 300 && validEnvelope && body.code === 0) {
            resolve(body.data);
            return;
          }
          reject(
            createRequestError(
              validEnvelope
                ? String(body.message || `upload failed with status ${statusCode}`)
                : "invalid response envelope",
              {
                code: validEnvelope ? body.code || statusCode || 500 : 10006,
                statusCode,
                invalidResponse: !validEnvelope,
                url: `${baseUrl}${url}`,
              },
            ),
          );
        },
        fail(error) {
          reject(
            createRequestError(String((error && error.errMsg) || "upload failed"), {
              url: `${baseUrl}${url}`,
            }),
          );
        },
      });
    });
  } catch (error) {
    const statusCode = Number(error && error.statusCode);
    const code = Number(error && error.code);
    if (!retriedAfterAuth && (statusCode === 401 || code === 10002)) {
      await recoverAuthentication();
      return uploadFileAttempt(options, true);
    }
    throw error;
  }
}

module.exports = {
  generateIdempotencyKey,
  recoverAuthentication,
  request,
  uploadFile,
};

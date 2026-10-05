const { generateRequestId } = require("./request-id");

function trimTrailingSlash(value) {
  return String(value || "").replace(/\/+$/, "");
}

// 微信 wx.request 把 response.header 的 key 大小写不规范化(iOS 全小写,
// Android 首字母大写)。统一查 4 种常见拼法。
function readHeader(headers, name) {
  if (!headers) return "";
  const candidates = [name, name.toLowerCase(), name.toUpperCase()];
  // 也覆盖 X-Request-Id (Id lowercase d) — 后端 echo 用这个写法
  if (name === "X-Request-ID") candidates.push("X-Request-Id", "x-request-id");
  for (const k of candidates) {
    if (headers[k] != null) return String(headers[k]);
  }
  return "";
}

function createRequestError(message, extras = {}) {
  const error = new Error(String(message || "request failed").trim() || "request failed");
  Object.keys(extras || {}).forEach((key) => {
    error[key] = extras[key];
  });
  return error;
}

function defaultIsAuthRequiredError(error) {
  return Boolean(
    error &&
      (error.authRequired ||
        Number(error.statusCode || 0) === 401 ||
        Number(error.code || 0) === 10002),
  );
}

function createClient(options = {}) {
  const baseUrlGetter =
    typeof options.baseUrl === "function"
      ? options.baseUrl
      : () => String(options.baseUrl || "").trim();
  const tokenGetter =
    typeof options.token === "function" ? options.token : () => String(options.token || "").trim();
  const onAuthRequired =
    typeof options.onAuthRequired === "function" ? options.onAuthRequired : null;
  const isAuthRequiredError =
    typeof options.isAuthRequiredError === "function"
      ? options.isAuthRequiredError
      : defaultIsAuthRequiredError;
  // onResponseMeta: optional hook — called for BOTH success and failure responses with
  // { headers, statusCode }.  Not called on network-level fail() (no HTTP response).
  // Callers that don't pass this option see zero behaviour change.
  const onResponseMeta =
    typeof options.onResponseMeta === "function" ? options.onResponseMeta : null;

  return function request(requestOptions = {}) {
    const authMode = requestOptions.authMode === undefined ? "required" : requestOptions.authMode;
    if (!["required", "optional", "none"].includes(authMode)) {
      return Promise.reject(createRequestError("invalid request auth mode", { code: 10001 }));
    }
    const baseUrl = trimTrailingSlash(baseUrlGetter());
    const url = String(requestOptions.url || "").trim();
    if (!url) {
      return Promise.reject(createRequestError("missing request url"));
    }
    if (!baseUrl) {
      return Promise.reject(createRequestError("missing api base url"));
    }

    const execute = (hasRetriedAuth) =>
      new Promise((resolve, reject) => {
        const header = { ...(requestOptions.header || {}) };
        const hasContentType = Object.keys(header).some(
          (key) => key.toLowerCase() === "content-type",
        );
        if (!hasContentType && requestOptions.data !== undefined) {
          header["Content-Type"] = "application/json";
        }

        // Anonymous requests must work before a session store exists. An
        // explicit token keeps a session probe bound to its intended owner.
        const token =
          authMode === "none"
            ? ""
            : String(requestOptions.token || "").trim() || String(tokenGetter() || "").trim();
        if (authMode === "none" || token) {
          Object.keys(header).forEach((key) => {
            if (key.toLowerCase() === "authorization") delete header[key];
          });
        }
        if (token) {
          header.Authorization = `Bearer ${token}`;
        }

        // X-Request-ID: 前端生成,后端 internal/middleware/requestid.go 会保留
        // 入站 header 并 echo 回来,实现前后端 trace 串联。
        // 调用方可通过 requestOptions.header['X-Request-ID'] 显式覆盖(重试场景)。
        const requestId = String(header["X-Request-ID"] || "").trim() || generateRequestId();
        header["X-Request-ID"] = requestId;

        wx.request({
          url: `${baseUrl}${url}`,
          method: requestOptions.method || "GET",
          data: requestOptions.data,
          timeout: requestOptions.timeout || 15000,
          header,
          success(response) {
            const echoedRequestId = readHeader(response.header, "X-Request-ID") || requestId;
            // onResponseMeta fires for every HTTP response (success + error status).
            // Wrapped in try/catch so a buggy hook never breaks the request pipeline.
            if (onResponseMeta) {
              try {
                onResponseMeta({ headers: response.header || {}, statusCode: response.statusCode });
              } catch (_hookErr) {}
            }
            const body = response.data;
            // A transport-level 2xx is not a command receipt. The API emits an
            // integer code, including zero when data is omitted (e.g. confirm).
            // Missing/coerced codes must leave callers on their failure path.
            const validEnvelope = Boolean(
              body &&
                typeof body === "object" &&
                !Array.isArray(body) &&
                Number.isSafeInteger(body.code),
            );
            if (
              response.statusCode >= 200 &&
              response.statusCode < 300 &&
              validEnvelope &&
              body.code === 0
            ) {
              // 把 requestId 暴露给调用方:对于成功响应,若 data 是 object 则
              // 附加 __requestId(non-enumerable,避免污染序列化)。原始数据
              // 保持向后兼容(原本 resolve 的就是 body.data)。
              const data = body.data;
              if (data && typeof data === "object") {
                try {
                  Object.defineProperty(data, "__requestId", {
                    value: echoedRequestId,
                    enumerable: false,
                    configurable: true,
                  });
                } catch (_) {
                  // some sandboxes reject defineProperty on frozen objects;不致命
                }
              }
              resolve(data);
              return;
            }
            const error = createRequestError(
              validEnvelope
                ? String(body.message || `request failed with status ${response.statusCode}`)
                : "invalid response envelope",
              {
                code: validEnvelope ? body.code || Number(response.statusCode || 500) : 10006,
                statusCode: Number(response.statusCode || 0),
                body: validEnvelope ? body : undefined,
                data: validEnvelope ? body : undefined,
                invalidResponse: !validEnvelope,
                url: `${baseUrl}${url}`,
                requestId: echoedRequestId,
                headers: response.header || {},
                retryAfterSeconds: Number(readHeader(response.header, "Retry-After") || 0),
              },
            );
            const authHandler =
              typeof requestOptions.onAuthRequired === "function"
                ? requestOptions.onAuthRequired
                : onAuthRequired;
            if (
              !hasRetriedAuth &&
              authMode === "required" &&
              requestOptions.skipAuthRetry !== true &&
              typeof authHandler === "function"
            ) {
              // Host hooks run after wx has dispatched the response. Catch
              // synchronous throws as well as rejected recovery promises so
              // the original request always settles with its own error.
              Promise.resolve()
                .then(() => isAuthRequiredError(error) && authHandler(error, requestOptions))
                .then((shouldRetry) => {
                  if (!shouldRetry) {
                    reject(error);
                    return;
                  }
                  execute(true).then(resolve).catch(reject);
                })
                .catch(() => reject(error));
              return;
            }
            reject(error);
          },
          fail(error) {
            reject(
              createRequestError(String((error && error.errMsg) || "network request failed"), {
                url: `${baseUrl}${url}`,
                requestId,
              }),
            );
          },
        });
      });

    return execute(false);
  };
}

module.exports = {
  createRequestError,
  createClient,
};

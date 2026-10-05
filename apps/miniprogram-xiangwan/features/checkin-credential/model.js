"use strict";

const qrcode = require("qrcode-generator");
const { formatDateTime } = require("../../utils/format");
const { UUID_PATTERN, canonicalUUID, invalidResponse } = require("../../services/xiangwan-api");

const BACKUP_CODE_PATTERN = /^[0-9A-HJKMNP-TV-Z]{4}(?:-[0-9A-HJKMNP-TV-Z]{4}){2}$/;
const SERVER_TIME_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/;
const MAX_CREDENTIAL_TTL_MS = 15 * 60 * 1000;
const REISSUE_FLOOR_MS = 5 * 1000;
const QR_CANVAS_SIZE = 260;
const QR_QUIET_ZONE_MODULES = 4;
const QR_TOKEN_PATTERN = new RegExp(
  `^xw1\\.(${UUID_PATTERN.source.slice(1, -1)})\\.([A-Za-z0-9_-]{42}[AEIMQUYcgkosw048])$`,
);

function responseUUID(value, label, expected = "") {
  try {
    if (typeof value !== "string") throw invalidResponse("签到凭证");
    const normalized = canonicalUUID(value, label);
    if (expected && normalized !== expected) throw invalidResponse("签到凭证");
    return normalized;
  } catch (_error) {
    throw invalidResponse("签到凭证");
  }
}

function serverTimestamp(value) {
  if (typeof value !== "string") throw invalidResponse("签到凭证");
  const normalized = value.trim();
  const parts = normalized.match(SERVER_TIME_PATTERN);
  if (!parts) throw invalidResponse("签到凭证");
  const expectedParts = parts.slice(1, 7).map(Number);
  const milliseconds = Number(
    String(parts[7] || "")
      .padEnd(3, "0")
      .slice(0, 3),
  );
  const timestamp = Date.UTC(
    expectedParts[0],
    expectedParts[1] - 1,
    expectedParts[2],
    expectedParts[3],
    expectedParts[4],
    expectedParts[5],
    milliseconds,
  );
  if (!Number.isFinite(timestamp)) throw invalidResponse("签到凭证");
  const parsed = new Date(timestamp);
  const actualParts = [
    parsed.getUTCFullYear(),
    parsed.getUTCMonth() + 1,
    parsed.getUTCDate(),
    parsed.getUTCHours(),
    parsed.getUTCMinutes(),
    parsed.getUTCSeconds(),
  ];
  if (actualParts.some((part, index) => part !== expectedParts[index])) {
    throw invalidResponse("签到凭证");
  }
  return { normalized, timestamp };
}

function checkinQrToken(value, credentialJti) {
  if (typeof value !== "string") throw invalidResponse("签到凭证");
  const token = value.trim();
  const match = token.match(QR_TOKEN_PATTERN);
  if (!match || match[1] !== credentialJti) throw invalidResponse("签到凭证");
  return token;
}

function projectCheckinCredential(payload = {}, expectedRegistrationId) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    throw invalidResponse("签到凭证");
  }
  const registrationId = responseUUID(
    payload.registration_id,
    "报名记录",
    canonicalUUID(expectedRegistrationId, "报名记录"),
  );
  const credentialJti = responseUUID(payload.credential_jti, "签到凭证");
  const issuedAt = serverTimestamp(payload.issued_at);
  const expiresAt = serverTimestamp(payload.expires_at);
  const credentialEpoch = payload.credential_epoch;
  if (typeof payload.backup_code !== "string") throw invalidResponse("签到凭证");
  const backupCode = payload.backup_code.trim();

  if (
    !Number.isSafeInteger(credentialEpoch) ||
    credentialEpoch < 1 ||
    !expiresAt.timestamp ||
    expiresAt.timestamp <= issuedAt.timestamp ||
    expiresAt.timestamp - issuedAt.timestamp > MAX_CREDENTIAL_TTL_MS ||
    !BACKUP_CODE_PATTERN.test(backupCode)
  ) {
    throw invalidResponse("签到凭证");
  }

  return {
    registrationId,
    seriesId: responseUUID(payload.series_id, "签到凭证"),
    instanceId: responseUUID(payload.instance_id, "签到凭证"),
    sessionId: responseUUID(payload.session_id, "签到凭证"),
    credentialJti,
    credentialEpoch,
    qrToken: checkinQrToken(payload.qr_token, credentialJti),
    backupCode,
    issuedAtMs: issuedAt.timestamp,
    expiresAtMs: expiresAt.timestamp,
    issuedAtText: formatDateTime(issuedAt.normalized),
    expiresAtText: formatDateTime(expiresAt.normalized),
  };
}

function createQrMatrix(token) {
  const normalized = String(token || "").trim();
  if (!normalized) throw invalidResponse("签到二维码");
  const qr = qrcode(0, "M");
  qr.addData(normalized, "Byte");
  qr.make();
  const size = qr.getModuleCount();
  if (!Number.isSafeInteger(size) || size < 21 || size > 177) {
    throw invalidResponse("签到二维码");
  }
  const modules = [];
  for (let row = 0; row < size; row += 1) {
    const line = [];
    for (let column = 0; column < size; column += 1) {
      line.push(qr.isDark(row, column) === true);
    }
    modules.push(line);
  }
  return { size, modules };
}

function drawQrMatrix(context, matrix, canvasSize = QR_CANVAS_SIZE) {
  const setFillStyle =
    typeof context?.setFillStyle === "function"
      ? (color) => context.setFillStyle(color)
      : typeof context?.fillStyle !== "undefined"
        ? (color) => {
            context.fillStyle = color;
          }
        : null;
  if (
    !context ||
    !setFillStyle ||
    typeof context.fillRect !== "function" ||
    !matrix ||
    !Number.isSafeInteger(matrix.size) ||
    !Array.isArray(matrix.modules) ||
    !Number.isSafeInteger(canvasSize) ||
    canvasSize < 1
  ) {
    throw invalidResponse("签到二维码");
  }
  const cellSize = Math.floor(canvasSize / (matrix.size + QR_QUIET_ZONE_MODULES * 2));
  if (cellSize < 1) throw invalidResponse("签到二维码");
  const renderedSize = cellSize * (matrix.size + QR_QUIET_ZONE_MODULES * 2);
  const offset = Math.floor((canvasSize - renderedSize) / 2);
  const dataOffset = offset + QR_QUIET_ZONE_MODULES * cellSize;

  setFillStyle("#ffffff");
  context.fillRect(0, 0, canvasSize, canvasSize);
  setFillStyle("#10233f");
  matrix.modules.forEach((line, row) => {
    if (!Array.isArray(line) || line.length !== matrix.size) {
      throw invalidResponse("签到二维码");
    }
    let runStart = -1;
    for (let column = 0; column <= matrix.size; column += 1) {
      const dark = column < matrix.size && line[column] === true;
      if (dark && runStart < 0) runStart = column;
      if (!dark && runStart >= 0) {
        context.fillRect(
          dataOffset + runStart * cellSize,
          dataOffset + row * cellSize,
          (column - runStart) * cellSize,
          cellSize,
        );
        runStart = -1;
      }
    }
  });
  return { cellSize, offset, renderedSize };
}

module.exports = {
  BACKUP_CODE_PATTERN,
  MAX_CREDENTIAL_TTL_MS,
  QR_CANVAS_SIZE,
  REISSUE_FLOOR_MS,
  createQrMatrix,
  drawQrMatrix,
  projectCheckinCredential,
};

"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const jsQR = require("jsqr");
const {
  QR_CANVAS_SIZE,
  createQrMatrix,
  drawQrMatrix,
  projectCheckinCredential,
} = require("./model");

const REGISTRATION_ID = "11111111-1111-4111-8111-111111111111";
const SERIES_ID = "22222222-2222-4222-8222-222222222222";
const INSTANCE_ID = "33333333-3333-4333-8333-333333333333";
const SESSION_ID = "44444444-4444-4444-8444-444444444444";
const CREDENTIAL_JTI = "55555555-5555-4555-8555-555555555555";
const QR_TOKEN = `xw1.${CREDENTIAL_JTI}.${"A".repeat(43)}`;

function credentialFixture(overrides = {}) {
  return {
    registration_id: REGISTRATION_ID,
    series_id: SERIES_ID,
    instance_id: INSTANCE_ID,
    session_id: SESSION_ID,
    credential_jti: CREDENTIAL_JTI,
    credential_epoch: 2,
    qr_token: QR_TOKEN,
    backup_code: "0123-ABCD-EFGH",
    issued_at: "2026-09-14T09:00:00Z",
    expires_at: "2026-09-14T09:10:00Z",
    ...overrides,
  };
}

test("credential projection validates exact resource identity, secrets and bounded expiry", () => {
  const credential = projectCheckinCredential(
    credentialFixture({
      issued_at: "2026-09-14T09:00:00.123456789Z",
      expires_at: "2026-09-14T09:10:00.123456789Z",
    }),
    REGISTRATION_ID,
  );

  assert.equal(credential.registrationId, REGISTRATION_ID);
  assert.equal(credential.credentialJti, CREDENTIAL_JTI);
  assert.equal(credential.qrToken, QR_TOKEN);
  assert.equal(credential.backupCode, "0123-ABCD-EFGH");
  assert.equal(credential.expiresAtMs - credential.issuedAtMs, 10 * 60 * 1000);
  assert.equal(credential.expiresAtText, "09-14 17:10");
});

test("credential projection fails closed on crossed, malformed or overlong facts", () => {
  [
    credentialFixture({ registration_id: SESSION_ID }),
    credentialFixture({ credential_epoch: 0 }),
    credentialFixture({ credential_epoch: "1" }),
    credentialFixture({ qr_token: `xw1.${SESSION_ID}.${"A".repeat(43)}` }),
    credentialFixture({ backup_code: "IIII-IIII-IIII" }),
    credentialFixture({ expires_at: "2026-09-14T09:15:00.001Z" }),
    credentialFixture({ expires_at: "2026-09-31T09:10:00Z" }),
    credentialFixture({ expires_at: "not-a-time" }),
  ].forEach((fixture) => {
    assert.throws(
      () => projectCheckinCredential(fixture, REGISTRATION_ID),
      (error) => error.invalidResponse === true,
    );
  });
});

test("local QR matrix has standard finder cells and renders an integer quiet zone", () => {
  const matrix = createQrMatrix(QR_TOKEN);
  assert.equal(matrix.modules.length, matrix.size);
  assert.equal(matrix.modules[0].length, matrix.size);
  assert.equal(matrix.modules[0][0], true);
  assert.equal(matrix.modules[1][1], false);
  assert.equal(matrix.modules[3][3], true);

  const fills = [];
  const pixels = new Uint8ClampedArray(QR_CANVAS_SIZE * QR_CANVAS_SIZE * 4);
  const context = {
    color: "",
    setFillStyle(color) {
      this.color = color;
    },
    fillRect(x, y, width, height) {
      fills.push({ color: this.color, x, y, width, height });
      const rgb = this.color === "#10233f" ? [16, 35, 63] : [255, 255, 255];
      for (let pixelY = y; pixelY < y + height; pixelY += 1) {
        for (let pixelX = x; pixelX < x + width; pixelX += 1) {
          const index = (pixelY * QR_CANVAS_SIZE + pixelX) * 4;
          pixels[index] = rgb[0];
          pixels[index + 1] = rgb[1];
          pixels[index + 2] = rgb[2];
          pixels[index + 3] = 255;
        }
      }
    },
  };
  const layout = drawQrMatrix(context, matrix);
  assert.ok(layout.cellSize >= 1);
  assert.equal(fills[0].color, "#ffffff");
  assert.deepEqual(fills[0], {
    color: "#ffffff",
    x: 0,
    y: 0,
    width: QR_CANVAS_SIZE,
    height: QR_CANVAS_SIZE,
  });
  const firstDark = fills.find((fill) => fill.color === "#10233f");
  assert.ok(firstDark.x >= layout.offset + 4 * layout.cellSize);
  assert.ok(firstDark.y >= layout.offset + 4 * layout.cellSize);
  assert.ok(fills.length > 100);
  const decoded = jsQR(pixels, QR_CANVAS_SIZE, QR_CANVAS_SIZE, {
    inversionAttempts: "dontInvert",
  });
  assert.ok(decoded, "independent decoder must locate the rendered QR");
  assert.equal(decoded.data, QR_TOKEN);
});

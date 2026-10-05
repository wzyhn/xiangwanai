import assert from "node:assert/strict";
import { test } from "node:test";
import { reviewMediaChoice, reviewMediaSHA256 } from "./review-media.ts";

test("review media selection enforces the server MIME and byte limits", () => {
  assert.deepEqual(reviewMediaChoice("event.jpg", "image/jpeg", 1024), {
    kind: "photo", mime: "image/jpeg", maxBytes: 10 << 20,
  });
  assert.equal(reviewMediaChoice("event.svg", "image/svg+xml", 1024), null);
  assert.equal(reviewMediaChoice("event.jpg", "image/png", 1024), null);
  assert.equal(reviewMediaChoice("event.mp4", "video/mp4", (200 << 20) + 1), null);
  assert.equal(reviewMediaChoice("event.pdf", "application/pdf", 0), null);
});

test("review media digest uses the exact uploaded bytes", async () => {
  assert.equal(await reviewMediaSHA256(new Blob(["abc"])),
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
});

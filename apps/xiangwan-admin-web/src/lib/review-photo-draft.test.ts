import test from "node:test";
import assert from "node:assert/strict";
import { moveReviewPhoto, removeReviewPhoto, selectedReviewPhotoURLs, type ReviewPhotoDraft } from "./review-photo-draft.ts";

function rows(): ReviewPhotoDraft[] {
  return [
    { id: 1, url: " https://media.example.com/one.webp ", enabled: true },
    { id: 2, url: "https://media.example.com/two.webp", enabled: false },
    { id: 3, url: "https://media.example.com/three.webp", enabled: true },
  ];
}

test("selectedReviewPhotoURLs keeps editor order and omits disabled/blank rows", () => {
  assert.deepEqual(selectedReviewPhotoURLs(rows()), [
    "https://media.example.com/one.webp",
    "https://media.example.com/three.webp",
  ]);
});

test("moveReviewPhoto swaps only valid adjacent positions", () => {
  const value = rows();
  assert.deepEqual(moveReviewPhoto(value, 1, -1).map((row) => row.id), [2, 1, 3]);
  assert.deepEqual(moveReviewPhoto(value, 0, -1), value);
  assert.deepEqual(moveReviewPhoto(value, 2, 1), value);
});

test("removeReviewPhoto is immutable and ignores an invalid row", () => {
  const value = rows();
  assert.deepEqual(removeReviewPhoto(value, 1).map((row) => row.id), [1, 3]);
  assert.deepEqual(removeReviewPhoto(value, 9), value);
  assert.deepEqual(value.map((row) => row.id), [1, 2, 3]);
});

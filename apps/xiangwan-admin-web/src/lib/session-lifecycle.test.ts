import assert from "node:assert/strict";
import test from "node:test";
import {
  allSessionsTerminal,
  canCompleteInstance,
  isSessionTerminal,
  nextSessionLifecycleBoundaryMs,
} from "./session-lifecycle.ts";

const now = Date.parse("2026-09-28T10:00:00.000Z");

test("cancelled sessions are terminal even when their scheduled end is in the future", () => {
  assert.equal(isSessionTerminal({ status: "cancelled", session_end_at: "2026-09-29T10:00:00.000Z" }, now), true);
  assert.equal(
    allSessionsTerminal([
      { status: "ended", session_end_at: "2026-09-28T09:00:00.000Z" },
      { status: "cancelled", session_end_at: "2026-09-29T10:00:00.000Z" },
    ], now),
    true,
  );
  assert.equal(canCompleteInstance([
    { status: "ended", session_end_at: "2026-09-28T09:00:00.000Z" },
    { status: "cancelled", session_end_at: "2026-09-29T10:00:00.000Z" },
  ], now), true);
});

test("a future published session keeps completion disabled", () => {
  assert.equal(
    allSessionsTerminal([
      { status: "published", session_end_at: "2026-09-28T09:00:00.000Z" },
      { status: "published", session_end_at: "2026-09-28T11:00:00.000Z" },
    ], now),
    false,
  );
  assert.equal(allSessionsTerminal([], now), false);
});

test("all-cancelled and overdue draft sessions cannot be marked completed", () => {
  assert.equal(allSessionsTerminal([{ status: "cancelled" }], now), true);
  assert.equal(canCompleteInstance([{ status: "cancelled" }], now), false);
  assert.equal(isSessionTerminal({ status: "draft", session_end_at: "2026-09-28T09:00:00.000Z" }, now), false);
  assert.equal(canCompleteInstance([
    { status: "published", session_end_at: "2026-09-28T09:00:00.000Z" },
    { status: "draft", session_end_at: "2026-09-28T09:00:00.000Z" },
  ], now), false);
});

test("the next future published end is exposed for a live completion refresh", () => {
  assert.equal(
    nextSessionLifecycleBoundaryMs([
      { status: "published", session_end_at: "2026-09-28T12:00:00.000Z" },
      { status: "published", session_end_at: "2026-09-28T11:00:00.000Z" },
      { status: "ended", session_end_at: "2026-09-28T08:00:00.000Z" },
    ],
    now,
    ),
    Date.parse("2026-09-28T11:00:00.000Z"),
  );
  assert.equal(nextSessionLifecycleBoundaryMs([{ status: "cancelled" }], now), null);
});

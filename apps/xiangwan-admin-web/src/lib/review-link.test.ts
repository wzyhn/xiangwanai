import assert from "node:assert/strict";
import test from "node:test";
import { reviewLinkRequest } from "./review-link.ts";

test("resource existence, visibility and a missing URL remain independent", () => {
  const draft = { added: false, enabled: false, title: "活动资料", subtitle: "待整理", url: "" };
  assert.equal(reviewLinkRequest(draft), undefined);
  assert.deepEqual(reviewLinkRequest({ ...draft, added: true, enabled: true }), {
    enabled: true,
    title: "活动资料",
    subtitle: "待整理",
    url: "",
  });
  assert.deepEqual(reviewLinkRequest({ ...draft, added: true }), {
    enabled: false,
    title: "活动资料",
    subtitle: "待整理",
    url: "",
  });
  assert.throws(() => reviewLinkRequest({ ...draft, added: true, title: " " }), /显示名称/);
});

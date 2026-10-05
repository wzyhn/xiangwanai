"use strict";
const test = require("node:test"),
  assert = require("node:assert/strict");
const { createHostApplicationPageDefinition } = require("./index");
const raw = {
  can_apply_for_host: true,
  host_rules: {
    state: "configured",
    application_cycle: "cycle-v1",
    policy_version: "rules-v1",
    requirements: "要求",
    benefits: "支持",
  },
  host_application_history: [],
};
function setup(t, extra = {}) {
  let binding = { principalId: "owner", generation: 1 };
  const old = global.getApp;
  global.getApp = () => ({
    ensureAuthenticated: async () => {},
    getXiangwanAuthBinding: () => binding,
  });
  t.after(() => {
    global.getApp = old;
  });
  const page = createHostApplicationPageDefinition({
    getMyBenefits: async () => raw,
    getPublicPolicies: async () => ({
      published_versions: [{ kind: "privacy", version: "privacy-v1" }],
    }),
    ...extra,
  });
  page.setData = (p) => {
    page.data = { ...page.data, ...p };
  };
  page.onLoad();
  return {
    page,
    switchOwner: () => {
      binding = { principalId: "other", generation: 2 };
    },
  };
}
function fill(p) {
  for (const f of [
    "personal_introduction",
    "relevant_experience",
    "availability",
    "contact_method",
  ])
    p.onInput({ currentTarget: { dataset: { field: f } }, detail: { value: "申请资料" } });
  p.onConsent({ detail: { value: ["consent"] } });
}
test("host application requires current rules, privacy and explicit consent", async (t) => {
  let writes = 0;
  const { page } = setup(t, {
    applyHostApplication: async (body) => {
      writes++;
      assert.equal(body.expected_policy_version, "rules-v1");
      assert.equal(body.expected_privacy_policy_version, "privacy-v1");
      return { application: { application_status: "pending" } };
    },
  });
  await page.submit();
  assert.equal(writes, 0);
  await page.load();
  await page.submit();
  assert.equal(writes, 0);
  fill(page);
  await page.submit();
  assert.equal(writes, 1);
  assert.equal(page.data.contact_method, "");
});
test("unknown submission freezes original private payload and retries exactly", async (t) => {
  const requests = [];
  const { page } = setup(t, {
    applyHostApplication: async (body) => {
      requests.push({ ...body });
      if (requests.length === 1) throw new Error("lost receipt");
      return { application: { application_status: "pending" } };
    },
  });
  await page.load();
  fill(page);
  await page.submit();
  assert.equal(page.data.pending, true);
  page.onInput({
    currentTarget: { dataset: { field: "contact_method" } },
    detail: { value: "changed" },
  });
  page.onConsent({ detail: { value: [] } });
  await page.retryPending();
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(page.data.pending, false);
});
test("account switch prevents original applicant write", async (t) => {
  let writes = 0;
  const { page, switchOwner } = setup(t, {
    applyHostApplication: async () => {
      writes++;
    },
  });
  await page.load();
  fill(page);
  switchOwner();
  await page.submit();
  assert.equal(writes, 0);
  assert.equal(page.data.benefits, null);
});
test("hide clears private fields and discards late receipts", async (t) => {
  let finish;
  const { page } = setup(t, {
    applyHostApplication: () =>
      new Promise((r) => {
        finish = r;
      }),
  });
  await page.load();
  fill(page);
  const promise = page.submit();
  page.onHide();
  finish({ application: { application_status: "pending" } });
  await promise;
  assert.equal(page.data.contact_method, "");
  assert.equal(page.data.notice, "");
  assert.equal(page._pending, null);
});
test("double submit sends only one application", async (t) => {
  let finish,
    writes = 0;
  const { page } = setup(t, {
    applyHostApplication: () => {
      writes++;
      return new Promise((r) => {
        finish = r;
      });
    },
  });
  await page.load();
  fill(page);
  const first = page.submit();
  await page.submit();
  assert.equal(writes, 1);
  finish({ application: { application_status: "pending" } });
  await first;
});
test("withdraw confirmation captures exact pending application version", async (t) => {
  let modal, called;
  const old = global.wx;
  global.wx = {
    showModal: (v) => {
      modal = v;
    },
  };
  t.after(() => {
    global.wx = old;
  });
  const { page } = setup(t, {
    getMyBenefits: async () => ({
      ...raw,
      can_apply_for_host: false,
      current_host_application: {
        application_id: "application",
        application_cycle: "cycle-v1",
        policy_version: "rules-v1",
        application_status: "pending",
        version: 1,
      },
      host_application_history: [],
    }),
    withdrawHostApplication: async (id, v) => {
      called = { id, v };
      return { application: { application_status: "withdrawn" } };
    },
  });
  await page.load();
  await page.withdraw();
  assert.equal(called, undefined);
  modal.success({ confirm: true });
  await new Promise((r) => setImmediate(r));
  assert.deepEqual(called, { id: "application", v: 1 });
});

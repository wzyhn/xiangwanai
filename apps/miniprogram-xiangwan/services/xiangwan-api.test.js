"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  couponsQuery,
  createXiangwanApi,
  favoritesQuery,
  homeQuery,
  ordersQuery,
  pastActivitiesQuery,
  peopleQuery,
  registrationsQuery,
} = require("./xiangwan-api");

const SESSION_ID = "11111111-1111-4111-8111-111111111111";
const REGISTRATION_ID = "22222222-2222-4222-8222-222222222222";
const OPERATION_ID = "33333333-3333-4333-8333-333333333333";
const SERIES_ID = "44444444-4444-4444-8444-444444444444";
const INSTANCE_ID = "55555555-5555-4555-8555-555555555555";
const CREDENTIAL_JTI = "66666666-6666-4666-8666-666666666666";
const ORDER_ID = "77777777-7777-4777-8777-777777777777";
const COUPON_ID = "88888888-8888-4888-8888-888888888888";
const SERIES_ID_2 = "99999999-9999-4999-8999-999999999999";
const PEOPLE_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const APPLICATION_ID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";

test("previous highlights preserve a selected Session and reject crossed identities", async () => {
  const result = {
    series_id: SERIES_ID,
    instance_id: INSTANCE_ID,
    session_id: SESSION_ID,
    previous_review: { instance_id: SERIES_ID_2, session_id: REGISTRATION_ID },
  };
  const api = createXiangwanApi({ request: async () => result });
  assert.equal(
    (await api.getSessionDetail(SESSION_ID)).previous_review.session_id,
    REGISTRATION_ID,
  );
  result.previous_review.instance_id = INSTANCE_ID;
  await assert.rejects(api.getSessionDetail(SESSION_ID));
  result.previous_review.instance_id = SERIES_ID_2;
  result.previous_review.session_id = "broken";
  await assert.rejects(api.getSessionDetail(SESSION_ID));
});

test("private profile extension uses an exact owner route and stable operation key", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return { candidate_version: 1, moderation_status: "pending_review" };
    },
  });
  const payload = {
    expected_version: 0,
    occupation: "",
    introduction: "喜欢徒步",
    tags: ["徒步"],
    visibility: { occupation: false, introduction: false, tags: false },
  };
  await api.updateProfileExtension(payload, OPERATION_ID);
  assert.deepEqual(calls, [
    {
      url: "/api/v1/xiangwan/me/profile",
      method: "PATCH",
      header: { "Idempotency-Key": OPERATION_ID },
      data: payload,
    },
  ]);
  await assert.rejects(api.updateProfileExtension(payload, "not-a-uuid"));
  assert.equal(calls.length, 1);
});

function validOrder(overrides = {}) {
  return {
    order_id: ORDER_ID,
    order_version: 2,
    registration_id: REGISTRATION_ID,
    registration_version: 1,
    series_id: SERIES_ID,
    series_title: "AI 课程",
    instance_id: INSTANCE_ID,
    instance_title: "第二期",
    session_id: SESSION_ID,
    session_title: "周末场",
    session_start_at: "2026-09-20T02:00:00Z",
    session_end_at: "2026-09-20T04:00:00Z",
    participation_status: "pending_payment",
    reservation_state: "pending_payment",
    reservation_has_active_access: false,
    payment_status: "pending",
    payment_confirmation_pending: false,
    original_price_cents: 9900,
    discount_cents: 1100,
    payable_cents: 8800,
    currency: "CNY",
    hold_status: "active",
    hold_expires_at: "2026-09-15T09:10:00Z",
    hold_version: 1,
    can_continue_payment: true,
    state: "pending_payment",
    outcome: "pending_payment",
    last_business_at: "2026-09-15T09:00:00Z",
    ...overrides,
  };
}

function validCoupon(overrides = {}) {
  return {
    coupon_id: COUPON_ID,
    face_value_cents: 1000,
    currency: "CNY",
    minimum_order_cents: 5000,
    valid_from: "2026-09-01T00:00:00Z",
    expires_at: "2026-12-01T00:00:00Z",
    grant_kind: "initial_guest",
    applicability: { scope_type: "activity_type", activity_type: "ai_roundtable" },
    state: "available",
    usable: true,
    correction_required: false,
    ...overrides,
  };
}

function validFavorite(overrides = {}) {
  return {
    series_id: SERIES_ID,
    title: "AI 圆桌",
    series_status: "active",
    favorite_count: 8,
    favorited_at: "2026-09-15T08:00:00Z",
    sessions_available: true,
    sessions_path: `/api/v1/xiangwan/series/${SERIES_ID}/sessions`,
    ...overrides,
  };
}

function validPerson(overrides = {}) {
  return {
    people_id: PEOPLE_ID,
    display_name: "林老师",
    headline: "AI 社群共创者",
    introduction: "持续组织面向普通人的 AI 实践活动。",
    version: 2,
    published_at: "2026-09-14T08:00:00Z",
    ...overrides,
  };
}

function validBenefits(overrides = {}) {
  const role = {
    series_id: SERIES_ID,
    instance_id: INSTANCE_ID,
    role_code: "invited_guest",
    role_status: "active",
    instance_status: "published",
    state: "current",
    series_title: "AI 圆桌",
    instance_title: "秋季场",
    granted_at: "2026-09-10T08:00:00Z",
  };
  return {
    has_host_identity: false,
    current_roles: [role],
    role_history: [role],
    host_rules: {
      state: "configured",
      requirements: "认同社区准则，并有稳定的活动组织时间。",
      benefits: "获得活动组织支持与社区贡献记录。",
    },
    can_apply_for_host: true,
    host_application_history: [],
    host_contribution_count: 1,
    host_contribution_history: [
      {
        series_id: SERIES_ID,
        instance_id: INSTANCE_ID,
        contribution_type: "host_checkin",
        state: "active",
        earned_at: "2026-09-09T08:00:00Z",
      },
    ],
    identity_history_available: true,
    ...overrides,
  };
}

test("home query preserves repeated quick tags and canonical filters", () => {
  assert.equal(
    homeQuery({
      activityType: "course",
      area: "hexi",
      timeWindow: "this_week",
      quickTags: ["newcomer", "hands_on"],
      cursor: "next/page",
      limit: 20,
    }),
    "?activity_type=course&area=hexi&time_window=this_week&quick_tag=newcomer&quick_tag=hands_on&cursor=next%2Fpage&limit=20",
  );
  assert.equal(
    registrationsQuery({ state: "registered", limit: 20 }),
    "?state=registered&limit=20",
  );
  assert.equal(
    pastActivitiesQuery({ activityType: "course", cursor: "next/page", limit: 20 }),
    "?activity_type=course&cursor=next%2Fpage&limit=20",
  );
  assert.equal(
    ordersQuery({ state: "pending_payment", cursor: "next/page", limit: 20 }),
    "?state=pending_payment&cursor=next%2Fpage&limit=20",
  );
  assert.equal(
    couponsQuery({ state: "available", cursor: "next/page", limit: 20 }),
    "?state=available&cursor=next%2Fpage&limit=20",
  );
  assert.equal(favoritesQuery({ cursor: "next/page", limit: 20 }), "?cursor=next%2Fpage&limit=20");
  assert.equal(peopleQuery({ cursor: "next/page", limit: 20 }), "?cursor=next%2Fpage&limit=20");
});

test("home brand limits count Unicode code points instead of UTF-16 units", async () => {
  const response = {
    community_name: "🤖".repeat(100),
    brand_intro: "💬".repeat(2000),
    hero_mode: "text",
    hero_eyebrow: "🏠".repeat(100),
    hero_subtitle: "✨".repeat(200),
    hero_image_url: "",
    hero_image_alt: "",
    brand_status: "active",
    brand_publication_version: 1,
    brand_published_at: "2026-09-15T09:00:00Z",
    available_quick_tags: [{ code: "community", label: "🎯".repeat(40) }],
    cards: [],
  };
  const api = createXiangwanApi({ request: async () => response });

  await assert.doesNotReject(() => api.getHomeSessions());
  response.community_name = "🤖".repeat(101);
  await assert.rejects(() => api.getHomeSessions(), { code: "invalid_response" });
});

test("every anonymous Xiangwan read explicitly strips stored credentials", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      const { url } = options;
      if (url === "/api/v1/xiangwan/home-sessions")
        return {
          community_name: "享玩 AI",
          brand_intro: "真实交流产生新的连接。",
          hero_mode: "text",
          hero_eyebrow: "天津 AI 共创社区",
          hero_subtitle: "线下深度交流",
          hero_image_url: "",
          hero_image_alt: "",
          brand_status: "active",
          brand_publication_version: 1,
          brand_published_at: "2026-09-15T09:00:00Z",
          available_quick_tags: [],
          cards: [],
        };
      if (url === "/api/v1/xiangwan/past-activities") {
        return { active_activity_type: "all", items: [] };
      }
      if (url === "/api/v1/xiangwan/people") {
        return {
          items: [],
          as_of: "2026-09-15T09:00:00Z",
          empty_state: "no_people",
        };
      }
      if (url === `/api/v1/xiangwan/people/${PEOPLE_ID}`) {
        return { person: validPerson() };
      }
      if (url === `/api/v1/xiangwan/sessions/${SESSION_ID}`) {
        return {
          series_id: SERIES_ID,
          instance_id: INSTANCE_ID,
          session_id: SESSION_ID,
        };
      }
      if (url.endsWith("/sessions")) {
        return {
          series_id: SERIES_ID,
          instance_id: INSTANCE_ID,
          action: "unavailable",
          sessions: [],
          empty_state: "no_public_sessions",
        };
      }
      if (url === `/api/v1/xiangwan/instances/${INSTANCE_ID}/review`) {
        return {
          series_id: SERIES_ID,
          series_title: "AI 课程",
          successful_published_instance_count: 2,
          historical_registration_count: 18,
          instance_id: INSTANCE_ID,
          instance_title: "第二期",
          instance_status: "completed",
          activity_type: "course",
          publication_version: 2,
          published_at: "2026-09-10T08:00:00Z",
          completed_at: "2026-09-12T08:00:00Z",
          content_blocks: [],
          documents: [],
          next_instance: { action: "unavailable", candidate_session_ids: [] },
        };
      }
      if (url === "/api/v1/xiangwan/public-policies") {
        return { published_versions: [], capabilities: {} };
      }
      throw new Error(`unexpected public read ${url}`);
    },
  });

  await api.getHomeSessions();
  await api.getPastActivities();
  await api.getPeople();
  await api.getPerson(PEOPLE_ID);
  await api.getSessionDetail(SESSION_ID);
  await api.getInstanceSessions(INSTANCE_ID);
  await api.getSeriesSessions(SERIES_ID);
  await api.getPublicReview(INSTANCE_ID);
  await api.getPublicPolicies();

  assert.equal(calls.length, 9);
  assert.ok(calls.every((options) => options.authMode === "none"));
});

test("My Orders list and detail use exact owner-scoped routes", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      const order = validOrder();
      if (options.url.endsWith(`/${ORDER_ID}`)) {
        return { order, as_of: "2026-09-15T09:00:01Z" };
      }
      return {
        active_state: "pending_payment",
        items: [order],
        as_of: "2026-09-15T09:00:01Z",
        next_cursor: "next-order",
      };
    },
  });

  await api.getMyOrders({ state: "pending_payment", limit: 20 });
  await api.getMyOrderDetail(ORDER_ID);

  assert.deepEqual(calls, [
    { url: "/api/v1/xiangwan/me/orders?state=pending_payment&limit=20" },
    { url: `/api/v1/xiangwan/me/orders/${ORDER_ID}` },
  ]);
});

test("My Order accepts final refund facts and a paid order that was previously closed", async () => {
  const responses = [
    validOrder({
      participation_status: "cancelled",
      reservation_state: "refunded",
      payment_status: "paid_confirmed",
      actual_paid_cents: 8800,
      paid_at: "2026-09-15T09:05:00Z",
      closed_at: "2026-09-15T08:55:00Z",
      hold_status: "released",
      can_continue_payment: false,
      state: "refunded",
      outcome: "refunded",
      last_business_at: "2026-09-15T09:08:00Z",
      refund: {
        refund_case_id: CREDENTIAL_JTI,
        refund_status: "refunded",
        reason_code: "user_cancelled",
        requested_refund_cents: 8800,
        successful_refund_cents: 8800,
        resolved_at: "2026-09-15T09:08:00Z",
        version: 2,
        updated_at: "2026-09-15T09:08:00Z",
      },
    }),
    validOrder({
      participation_status: "confirmed",
      reservation_state: "registered",
      reservation_has_active_access: true,
      payment_status: "paid_confirmed",
      actual_paid_cents: 8800,
      paid_at: "2026-09-15T09:05:00Z",
      closed_at: "2026-09-15T08:55:00Z",
      hold_status: "converted",
      can_continue_payment: false,
      state: "paid",
      outcome: "paid_confirmed",
      last_business_at: "2026-09-15T09:05:00Z",
    }),
  ];
  for (const order of responses) {
    const api = createXiangwanApi({
      request: async () => ({ order, as_of: "2026-09-15T09:10:00Z" }),
    });
    const detail = await api.getMyOrderDetail(ORDER_ID);
    assert.equal(detail.order.order_id, ORDER_ID);
  }
});

test("My Orders rejects cross-resource and internally inconsistent facts", async () => {
  const inconsistent = [
    validOrder({ order_id: SESSION_ID }),
    validOrder({ payable_cents: 8700 }),
    validOrder({ state: "paid" }),
    validOrder({ payment_confirmation_pending: true }),
  ];
  for (const order of inconsistent) {
    const api = createXiangwanApi({
      request: async ({ url }) =>
        url.endsWith(`/${ORDER_ID}`)
          ? { order, as_of: "2026-09-15T09:00:01Z" }
          : {
              active_state: "pending_payment",
              items: [order],
              as_of: "2026-09-15T09:00:01Z",
            },
    });
    await assert.rejects(
      () => api.getMyOrderDetail(ORDER_ID),
      (error) => error.invalidResponse === true,
    );
  }
});

test("past activities and review use exact public Instance routes", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      if (options.url.includes("past-activities")) {
        return {
          active_activity_type: "course",
          items: [
            {
              series_id: SERIES_ID,
              series_title: "AI 课程",
              successful_published_instance_count: 2,
              historical_registration_count: 18,
              instance_id: INSTANCE_ID,
              instance_title: "第二期",
              instance_status: "completed",
              activity_type: "course",
              publication_version: 2,
              published_at: "2026-09-10T08:00:00Z",
              completed_at: "2026-09-12T08:00:00Z",
            },
          ],
        };
      }
      return {
        series_id: SERIES_ID,
        series_title: "AI 课程",
        successful_published_instance_count: 2,
        historical_registration_count: 18,
        instance_id: INSTANCE_ID,
        instance_title: "第二期",
        instance_status: "completed",
        activity_type: "course",
        publication_version: 2,
        published_at: "2026-09-10T08:00:00Z",
        completed_at: "2026-09-12T08:00:00Z",
        content_blocks: [],
        documents: [
          {
            relation_id: REGISTRATION_ID,
            scope: "instance_review",
            blocks: [
              {
                block_id: CREDENTIAL_JTI,
                type: "text",
                availability: "available",
                text: "回顾",
              },
            ],
          },
        ],
        next_instance: { action: "unavailable", candidate_session_ids: [] },
      };
    },
  });

  await api.getPastActivities({ activityType: "course", limit: 20 });
  await api.getPublicReview(INSTANCE_ID);

  assert.deepEqual(calls, [
    {
      url: "/api/v1/xiangwan/past-activities?activity_type=course&limit=20",
      authMode: "none",
    },
    { url: `/api/v1/xiangwan/instances/${INSTANCE_ID}/review`, authMode: "none" },
  ]);
});

test("Instance Session collection never invents a default choice", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      series_id: SERIES_ID,
      instance_id: INSTANCE_ID,
      action: "session_selection_required",
      sessions: [
        {
          session_id: SESSION_ID,
          session_title: "上午工作坊",
          status: "archived",
          session_start_at: "2026-10-01T10:00:00Z",
          sort_order: 0,
          review_path: `/api/v1/xiangwan/instances/${INSTANCE_ID}/review?session_id=${SESSION_ID}`,
        },
        {
          session_id: REGISTRATION_ID,
          session_title: "下午工作坊",
          status: "ended",
          session_start_at: "2026-10-01T12:00:00Z",
          sort_order: 1,
          review_path: `/api/v1/xiangwan/instances/${INSTANCE_ID}/review?session_id=${REGISTRATION_ID}`,
        },
      ],
    }),
  });
  const result = await api.getInstanceSessions(INSTANCE_ID);
  assert.equal(result.sessions.length, 2);
  assert.equal(result.sessions[0].status, "archived");
  assert.equal(
    result.sessions[0].review_path,
    `/api/v1/xiangwan/instances/${INSTANCE_ID}/review?session_id=${SESSION_ID}`,
  );
  assert.equal(
    result.sessions[1].review_path,
    `/api/v1/xiangwan/instances/${INSTANCE_ID}/review?session_id=${REGISTRATION_ID}`,
  );
  assert.equal(result.direct_session_id, undefined);
});

test("archived Instance Session requires an exact review target", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      series_id: SERIES_ID,
      instance_id: INSTANCE_ID,
      action: "session_detail",
      direct_session_id: SESSION_ID,
      sessions: [
        {
          session_id: SESSION_ID,
          session_title: "归档回顾资料",
          status: "archived",
          session_start_at: "2026-10-01T10:00:00Z",
          sort_order: 0,
          detail_path: `/api/v1/xiangwan/sessions/${SESSION_ID}`,
        },
      ],
    }),
  });

  await assert.rejects(api.getInstanceSessions(INSTANCE_ID), /活动场次/);
});

test("Series Session collection rejects Instance-only review targets", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      series_id: SERIES_ID,
      instance_id: INSTANCE_ID,
      action: "session_detail",
      direct_session_id: SESSION_ID,
      sessions: [
        {
          session_id: SESSION_ID,
          session_title: "已结束场次",
          status: "ended",
          session_start_at: "2026-10-01T10:00:00Z",
          sort_order: 0,
          review_path: `/api/v1/xiangwan/instances/${INSTANCE_ID}/review?session_id=${SESSION_ID}`,
        },
      ],
    }),
  });

  await assert.rejects(api.getSeriesSessions(SERIES_ID), /活动场次/);
});

test("Series Session collection stays bound to the requested Series", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      series_id: SERIES_ID,
      instance_id: INSTANCE_ID,
      action: "session_detail",
      direct_session_id: SESSION_ID,
      sessions: [
        {
          session_id: SESSION_ID,
          session_title: "当前场次",
          status: "published",
          session_start_at: "2026-10-01T10:00:00Z",
          sort_order: 0,
          detail_path: `/api/v1/xiangwan/sessions/${SESSION_ID}`,
        },
      ],
    }),
  });
  const result = await api.getSeriesSessions(SERIES_ID);
  assert.equal(result.series_id, SERIES_ID);
  assert.equal(result.direct_session_id, SESSION_ID);
});

test("owner coupon and favorite reads validate exact PostgreSQL projections", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      if (options.url.includes("/coupons")) {
        return {
          active_state: "available",
          items: [validCoupon()],
          as_of: "2026-09-15T09:00:00Z",
          next_cursor: "coupon-next",
        };
      }
      return {
        items: [validFavorite()],
        as_of: "2026-09-15T09:00:00Z",
        next_cursor: "favorite-next",
      };
    },
  });

  await api.getMyCoupons({ state: "available", limit: 20 });
  await api.getMyFavorites({ limit: 20 });

  assert.deepEqual(calls, [
    { url: "/api/v1/xiangwan/me/coupons?state=available&limit=20" },
    { url: "/api/v1/xiangwan/me/favorites?limit=20" },
  ]);
});

test("public People list/detail and My Benefits use exact routes", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      if (options.url === `/api/v1/xiangwan/people/${PEOPLE_ID}`) {
        return { person: validPerson() };
      }
      if (options.url === "/api/v1/xiangwan/me/benefits") {
        return validBenefits();
      }
      return {
        items: [validPerson()],
        as_of: "2026-09-15T09:00:00Z",
        next_cursor: "people-next",
      };
    },
  });

  await api.getPeople({ cursor: "next/page", limit: 20 });
  await api.getPerson(PEOPLE_ID);
  await api.getMyBenefits();

  assert.deepEqual(calls, [
    {
      url: "/api/v1/xiangwan/people?cursor=next%2Fpage&limit=20",
      authMode: "none",
    },
    { url: `/api/v1/xiangwan/people/${PEOPLE_ID}`, authMode: "none" },
    { url: "/api/v1/xiangwan/me/benefits" },
  ]);
});

test("public People rejects crossed, future and malformed projections", async () => {
  const responses = [
    { person: validPerson({ people_id: SERIES_ID }) },
    {
      items: [validPerson({ published_at: "2026-09-16T09:00:00Z" })],
      as_of: "2026-09-15T09:00:00Z",
    },
    {
      items: [],
      as_of: "2026-09-15T09:00:00Z",
      next_cursor: "impossible",
      empty_state: "no_people",
    },
  ];

  const crossed = createXiangwanApi({ request: async () => responses[0] });
  await assert.rejects(
    () => crossed.getPerson(PEOPLE_ID),
    (error) => error.invalidResponse === true,
  );
  for (const response of responses.slice(1)) {
    const api = createXiangwanApi({ request: async () => response });
    await assert.rejects(
      () => api.getPeople(),
      (error) => error.invalidResponse === true,
    );
  }
});

test("My Benefits accepts exact role, application and contribution history", async () => {
  const approvedApplication = {
    application_id: APPLICATION_ID,
    application_cycle: "2026-autumn",
    policy_version: "host-policy-v1",
    application_status: "approved",
    review_comment: "申请通过",
    version: 2,
    submitted_at: "2026-09-01T08:00:00Z",
    updated_at: "2026-09-02T08:00:00Z",
  };
  const api = createXiangwanApi({
    request: async () =>
      validBenefits({
        trusted_people_profile_id: PEOPLE_ID,
        has_host_identity: true,
        can_apply_for_host: false,
        current_host_application: approvedApplication,
        host_application_history: [approvedApplication],
      }),
  });

  const result = await api.getMyBenefits();
  assert.equal(result.trusted_people_profile_id, PEOPLE_ID);
  assert.equal(result.current_host_application.application_status, "approved");
});

test("My Benefits rejects facts that contradict its server-owned summary", async () => {
  const malformed = [
    validBenefits({ current_roles: [] }),
    validBenefits({ host_contribution_count: 0 }),
    validBenefits({ host_rules: { state: "pending", requirements: "不应出现" } }),
    validBenefits({ can_apply_for_host: false }),
    validBenefits({ identity_history_available: false }),
  ];
  for (const response of malformed) {
    const api = createXiangwanApi({ request: async () => response });
    await assert.rejects(
      () => api.getMyBenefits(),
      (error) => error.invalidResponse === true,
    );
  }
});

test("favorite target commands are exact empty idempotent-state requests", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return {
        series_id: SERIES_ID,
        favorited: options.method === "PUT",
        changed: true,
        favorite_count: options.method === "PUT" ? 9 : 8,
        series_version: 4,
        occurred_at: "2026-09-15T09:00:00Z",
      };
    },
  });

  await api.setSeriesFavorite(SERIES_ID, true);
  await api.setSeriesFavorite(SERIES_ID, false);
  assert.deepEqual(calls, [
    { url: `/api/v1/xiangwan/series/${SERIES_ID}/favorite`, method: "PUT" },
    { url: `/api/v1/xiangwan/series/${SERIES_ID}/favorite`, method: "DELETE" },
  ]);
});

test("coupon and favorite clients reject crossed or inconsistent facts", async () => {
  const badCoupon = createXiangwanApi({
    request: async () => ({
      active_state: "available",
      items: [validCoupon({ usable: false })],
      as_of: "2026-09-15T09:00:00Z",
    }),
  });
  await assert.rejects(
    () => badCoupon.getMyCoupons({ state: "available" }),
    (error) => error.invalidResponse === true,
  );

  const badFavorite = createXiangwanApi({
    request: async () => ({
      items: [validFavorite({ sessions_path: `/api/v1/xiangwan/series/${SERIES_ID_2}/sessions` })],
      as_of: "2026-09-15T09:00:00Z",
    }),
  });
  await assert.rejects(
    () => badFavorite.getMyFavorites(),
    (error) => error.invalidResponse === true,
  );
});

test("registration submission uses the exact Session path and stable operation key", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return { registration_id: REGISTRATION_ID, session_id: SESSION_ID };
    },
    generateIdempotencyKey: () => OPERATION_ID,
  });
  assert.equal(api.newIdempotencyKey(), OPERATION_ID);
  const payload = {
    instance_publication_version: 3,
    price_cents: 0,
    privacy_policy_version: "privacy-v3",
  };
  const result = await api.createRegistration(SESSION_ID, payload, OPERATION_ID);
  assert.equal(result.registration_id, REGISTRATION_ID);
  assert.deepEqual(calls, [
    {
      url: `/api/v1/xiangwan/sessions/${SESSION_ID}/registrations`,
      method: "POST",
      data: { instance_publication_version: 3, price_cents: 0 },
      header: {
        "Idempotency-Key": OPERATION_ID,
        "X-Xiangwan-Privacy-Policy-Version": "privacy-v3",
      },
    },
  ]);
  assert.equal(payload.privacy_policy_version, "privacy-v3");
});

test("paid registration requires an exact Order receipt", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      registration_id: REGISTRATION_ID,
      session_id: SESSION_ID,
      next_action: "wechat_payment_required",
    }),
  });
  await assert.rejects(
    () =>
      api.createRegistration(
        SESSION_ID,
        {
          instance_publication_version: 3,
          price_cents: 8800,
          privacy_policy_version: "privacy-v3",
        },
        OPERATION_ID,
      ),
    (error) => error.invalidResponse === true,
  );
});

test("prepay uses exact Order version, payable amount and stable key; query sends no client facts", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      if (options.url.endsWith("wechat-prepay-attempts"))
        return {
          attempt_id: CREDENTIAL_JTI,
          order_id: ORDER_ID,
          attempt_status: "ready",
          hold_expires_at: "2026-09-15T09:10:00Z",
          next_action: "invoke_wechat_payment",
          payment_parameters: {
            timeStamp: "1789290000",
            nonceStr: "nonce-1",
            package: "prepay_id=wx123",
            signType: "RSA",
            paySign: "signed",
          },
        };
      return {
        order_id: ORDER_ID,
        payment_status: "paid_confirmed",
        query_status: "converged",
        confirmation_pending: false,
        retry_payment_allowed: false,
        next_action: "payment_confirmed",
      };
    },
  });
  const prepay = await api.createWeChatPrepayAttempt(ORDER_ID, 2, 8800, OPERATION_ID);
  const query = await api.queryWeChatPayment(ORDER_ID);
  assert.equal(prepay.payment_parameters.package, "prepay_id=wx123");
  assert.equal(query.next_action, "payment_confirmed");
  assert.deepEqual(calls, [
    {
      url: `/api/v1/xiangwan/orders/${ORDER_ID}/wechat-prepay-attempts`,
      method: "POST",
      data: { order_version: 2, payable_cents: 8800 },
      header: { "Idempotency-Key": OPERATION_ID },
    },
    {
      url: `/api/v1/xiangwan/orders/${ORDER_ID}/payment-queries`,
      method: "POST",
      header: { "Content-Type": "" },
    },
  ]);
});

test("payment client rejects crossed results and never accepts parameters on a pending prepay", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      attempt_id: CREDENTIAL_JTI,
      order_id: SESSION_ID,
      attempt_status: "ready",
      hold_expires_at: "2026-09-15T09:10:00Z",
      next_action: "invoke_wechat_payment",
      payment_parameters: {
        timeStamp: "1789290000",
        nonceStr: "nonce-1",
        package: "prepay_id=wx123",
        signType: "RSA",
        paySign: "signed",
      },
    }),
  });
  await assert.rejects(
    () => api.createWeChatPrepayAttempt(ORDER_ID, 2, 8800, OPERATION_ID),
    (error) => error.invalidResponse === true,
  );
  const pending = createXiangwanApi({
    request: async () => ({
      attempt_id: CREDENTIAL_JTI,
      order_id: ORDER_ID,
      attempt_status: "unknown",
      hold_expires_at: "2026-09-15T09:10:00Z",
      next_action: "payment_confirmation_pending",
      payment_parameters: { package: "prepay_id=unsafe" },
    }),
  });
  await assert.rejects(
    () => pending.createWeChatPrepayAttempt(ORDER_ID, 2, 8800, OPERATION_ID),
    (error) => error.invalidResponse === true,
  );
});

test("payment retry indicator must belong to a pending Order and verified pending query", async () => {
  const allowed = createXiangwanApi({
    request: async () => ({
      order_id: ORDER_ID,
      payment_status: "pending",
      query_status: "pending",
      confirmation_pending: true,
      retry_payment_allowed: true,
      next_action: "retry_payment_query",
    }),
  });
  assert.equal((await allowed.queryWeChatPayment(ORDER_ID)).retry_payment_allowed, true);
  const crossed = createXiangwanApi({
    request: async () => ({
      order_id: ORDER_ID,
      payment_status: "unknown",
      query_status: "unknown",
      confirmation_pending: true,
      retry_payment_allowed: true,
      next_action: "retry_payment_query",
    }),
  });
  await assert.rejects(
    () => crossed.queryWeChatPayment(ORDER_ID),
    (error) => error.invalidResponse === true,
  );
});

test("client rejects malformed identifiers before dispatch", async () => {
  let calls = 0;
  const api = createXiangwanApi({
    request: async () => {
      calls += 1;
      return {};
    },
  });
  await assert.rejects(() => api.getSessionDetail("not-a-session"), /活动场次 无效/);
  await assert.rejects(() => api.getPerson("not-a-person"), /社区人物 无效/);
  await assert.rejects(() => api.getInstanceSessions("not-an-instance"), /活动期次 无效/);
  await assert.rejects(() => api.getSeriesSessions("not-a-series"), /活动系列 无效/);
  await assert.rejects(() => api.getPublicReview("not-an-instance"), /活动期次 无效/);
  await assert.rejects(() => api.getMyOrderDetail("not-an-order"), /订单 无效/);
  await assert.rejects(() => api.getMyRegistrationDetail("not-a-registration"), /报名记录 无效/);
  await assert.rejects(() => api.issueCheckinCredential("not-a-registration"), /报名记录 无效/);
  await assert.rejects(() => api.cancelRegistration("not-a-registration"), /报名记录 无效/);
  await assert.rejects(() => api.setSeriesFavorite("not-a-series", true), /活动系列 无效/);
  await assert.rejects(() => api.setSeriesFavorite(SERIES_ID, "true"), /收藏状态无效/);
  assert.equal(calls, 0);
});

test("check-in credential issue is an exact empty POST without an idempotency key", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return {
        registration_id: REGISTRATION_ID,
        series_id: SERIES_ID,
        instance_id: INSTANCE_ID,
        session_id: SESSION_ID,
        credential_jti: CREDENTIAL_JTI,
      };
    },
  });

  await api.issueCheckinCredential(REGISTRATION_ID);

  assert.deepEqual(calls, [
    {
      url: `/api/v1/xiangwan/registrations/${REGISTRATION_ID}/checkin-credentials`,
      method: "POST",
      header: { "Content-Type": "" },
    },
  ]);
});

test("check-in credential issue rejects a cross-registration success response", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      registration_id: SESSION_ID,
      series_id: SERIES_ID,
      instance_id: INSTANCE_ID,
      session_id: SESSION_ID,
      credential_jti: CREDENTIAL_JTI,
    }),
  });

  await assert.rejects(
    () => api.issueCheckinCredential(REGISTRATION_ID),
    (error) => error.invalidResponse === true,
  );
});

test("registration cancellation is an exact empty POST without client-owned facts", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return {
        registration_id: REGISTRATION_ID,
        session_id: SESSION_ID,
      };
    },
  });

  await api.cancelRegistration(REGISTRATION_ID);

  assert.deepEqual(calls, [
    {
      url: `/api/v1/xiangwan/registrations/${REGISTRATION_ID}/cancellations`,
      method: "POST",
      header: { "Content-Type": "" },
    },
  ]);
});

test("registration cancellation rejects a cross-registration success response", async () => {
  const api = createXiangwanApi({
    request: async () => ({
      registration_id: SESSION_ID,
      session_id: SESSION_ID,
    }),
  });

  await assert.rejects(
    () => api.cancelRegistration(REGISTRATION_ID),
    (error) => error.invalidResponse === true,
  );
});

test("client rejects cross-resource and incomplete success responses", async () => {
  const api = createXiangwanApi({
    request: async ({ url }) => {
      if (url.includes("/registrations")) {
        return { registration_id: REGISTRATION_ID, session_id: "not-the-requested-session" };
      }
      return {
        series_id: SERIES_ID,
        instance_id: INSTANCE_ID,
        session_id: REGISTRATION_ID,
      };
    },
  });
  await assert.rejects(
    () => api.getSessionDetail(SESSION_ID),
    (error) => {
      assert.equal(error.invalidResponse, true);
      return true;
    },
  );
  await assert.rejects(
    () =>
      api.createRegistration(
        SESSION_ID,
        { price_cents: 0, privacy_policy_version: "privacy-v1" },
        OPERATION_ID,
      ),
    (error) => {
      assert.equal(error.invalidResponse, true);
      return true;
    },
  );
});

test("my profile read and nickname command use exact owner routes", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      if (options.url === "/api/v1/xiangwan/me/profile") {
        return {
          nickname: "小林",
          avatar_url: "/api/v1/xiangwan/avatars/a1.png",
          principal_profile_etag: "etag-7",
        };
      }
      return {
        nickname: "新昵称",
        principal_profile_etag: "etag-8",
      };
    },
  });

  const profile = await api.getMyProfile();
  assert.equal(profile.nickname, "小林");
  assert.equal(profile.avatar_url, "/api/v1/xiangwan/avatars/a1.png");
  assert.equal(profile.principal_profile_etag, "etag-7");

  const updated = await api.updateNickname("  新昵称  ", profile.principal_profile_etag);
  assert.deepEqual(updated, { nickname: "新昵称", principalProfileETag: "etag-8" });

  assert.deepEqual(calls, [
    {
      url: "/api/v1/xiangwan/me/profile",
      header: { "Content-Type": "" },
    },
    {
      url: "/api/v1/xiangwan/me/profile/nickname",
      method: "PATCH",
      data: { nickname: "新昵称", principal_profile_etag: "etag-7" },
    },
  ]);
});

test("my profile read rejects wrong-typed contract fields", async () => {
  for (const payload of [
    { nickname: 42 },
    { avatar_url: 42 },
    { avatarUrl: 42 },
    { principal_profile_etag: 42 },
    { principalProfileETag: 42 },
    null,
    [],
  ]) {
    const api = createXiangwanApi({ request: async () => payload });
    await assert.rejects(
      () => api.getMyProfile(),
      (error) => {
        assert.equal(error.invalidResponse, true);
        return true;
      },
    );
  }
});

test("my profile commands tolerate camelCase compatibility fields", async () => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return options.method === "PATCH"
        ? { nickname: "新昵称", principalProfileETag: "etag-camel" }
        : {
            nickname: "小林",
            avatarUrl: "/api/v1/xiangwan/avatars/a1.png",
            principalProfileETag: "etag-7",
          };
    },
  });
  const profile = await api.getMyProfile();
  assert.equal(profile.avatarUrl, "/api/v1/xiangwan/avatars/a1.png");
  const updated = await api.updateNickname("新昵称", "etag-7");
  assert.deepEqual(updated, { nickname: "新昵称", principalProfileETag: "etag-camel" });
  assert.equal(calls.length, 2);
});

test("nickname command rejects blank and over-length values before dispatch", async () => {
  let calls = 0;
  const api = createXiangwanApi({
    request: async () => {
      calls += 1;
      return null;
    },
  });
  await assert.rejects(() => api.updateNickname("   "), /昵称需为 1–64 个字符/);
  await assert.rejects(() => api.updateNickname(""), /昵称需为 1–64 个字符/);
  await assert.rejects(() => api.updateNickname("🙂".repeat(65)), /昵称需为 1–64 个字符/);
  await assert.rejects(() => api.updateNickname(null), /昵称需为 1–64 个字符/);
  assert.equal(calls, 0);
});

test("avatar upload posts the multipart image field and tolerates avatar_url/url", async () => {
  const uploads = [];
  const api = createXiangwanApi({
    request: async () => {
      throw new Error("avatar upload must not use the JSON request pipeline");
    },
    uploadFile: async (options) => {
      uploads.push(options);
      return { url: "/api/v1/xiangwan/avatars/b2.png" };
    },
  });

  const result = await api.uploadAvatar("wxfile://tmp/avatar.png");
  assert.equal(result.url, "/api/v1/xiangwan/avatars/b2.png");
  assert.deepEqual(uploads, [
    { url: "/api/v1/xiangwan/me/avatar", filePath: "wxfile://tmp/avatar.png", name: "image" },
  ]);
});

test("avatar upload tolerates the camelCase avatarUrl compatibility field", async () => {
  const api = createXiangwanApi({
    uploadFile: async () => ({ avatarUrl: "/api/v1/xiangwan/avatars/camel.png" }),
  });
  const result = await api.uploadAvatar("wxfile://tmp/avatar.png");
  assert.equal(result.avatarUrl, "/api/v1/xiangwan/avatars/camel.png");
});

test("avatar upload rejects empty files and responses without any url field", async () => {
  let uploads = 0;
  const api = createXiangwanApi({
    uploadFile: async () => {
      uploads += 1;
      return { uploaded: true };
    },
  });
  await assert.rejects(() => api.uploadAvatar("  "), /头像文件无效/);
  assert.equal(uploads, 0);
  await assert.rejects(
    () => api.uploadAvatar("wxfile://tmp/avatar.png"),
    (error) => {
      assert.equal(error.invalidResponse, true);
      return true;
    },
  );
  assert.equal(uploads, 1);
});

test("questionnaire prefill is owner-routed and rejects crossed session responses", async () => {
  let url;
  const result = {
    session_id: SESSION_ID,
    questionnaire_version_id: INSTANCE_ID,
    answers: [{ field_id: SERIES_ID, values: ["回答"] }],
  };
  const api = createXiangwanApi({
    request: async (options) => {
      url = options.url;
      return result;
    },
  });
  assert.equal((await api.getQuestionnairePrefill(SESSION_ID)).answers.length, 1);
  assert.equal(url, "/api/v1/xiangwan/me/questionnaire-prefill/" + SESSION_ID);
  result.session_id = REGISTRATION_ID;
  await assert.rejects(api.getQuestionnairePrefill(SESSION_ID));
});

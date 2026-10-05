"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { capacityText, projectHomePage, projectSessionDetail } = require("./model");

test("home projection keeps one card per Session and detail-only CTA copy", () => {
  const page = projectHomePage({
    community_name: "享玩 AI",
    hero_mode: "image",
    hero_eyebrow: "天津 AI 共创社区",
    hero_subtitle: "线下深度交流 AI 社区",
    hero_image_url: "https://assets.example.com/hero.jpg",
    hero_image_alt: "享玩社区活动现场",
    open_registration_session_count: 3,
    available_quick_tags: [{ code: "newcomer", label: "新人友好" }],
    cards: [
      {
        session_id: "session-1",
        instance_title: "共创夜",
        session_title: "第一场",
        activity_type: "ai_roundtable",
        quick_tag_codes: ["newcomer"],
        session_start_at: "2026-09-20T11:00:00Z",
        session_end_at: "2026-09-20T14:00:00Z",
        price_cents: 0,
        delivery_mode: "offline",
        area: "hexi",
        venue_name: "海河实验室",
        heat_count: 26,
        current_favorite_users: 2,
        favorite_avatars: ["https://thirdwx.qlogo.cn/avatar/1"],
        display: { state: "open", sellable_capacity: 8 },
        cta_label: "view_details",
      },
    ],
  });
  assert.equal(page.cards.length, 1);
  assert.equal(page.heroMode, "image");
  assert.equal(page.heroImageUrl, "https://assets.example.com/hero.jpg");
  assert.equal(page.openCount, 3);
  assert.equal(page.cards[0].ctaLabel, "查看详情");
  assert.equal(page.cards[0].title, "共创夜");
  assert.equal(page.cards[0].instanceTitle, "共创夜");
  assert.equal(page.cards[0].sessionTitle, "第一场");
  assert.deepEqual(page.cards[0].tags, ["新人友好"]);
  assert.equal(page.cards[0].remainingText, "剩余 8 个名额");
  assert.equal(page.cards[0].heatCount, 26);
  assert.equal(page.cards[0].heatText, "26 人想去");
  assert.equal(page.cards[0].wantText, "2 人想去");
  assert.deepEqual(page.cards[0].favoriteAvatars, ["https://thirdwx.qlogo.cn/avatar/1"]);
  assert.equal(page.cards[0].scheduleDateText, "09月20日 周日");
  assert.equal(page.cards[0].scheduleTimeText, "19:00–22:00");
});

test("home projection accepts camelCase avatar fields only through the public Xiangwan media paths", () => {
  const page = projectHomePage(
    {
      cards: [
        {
          session_id: "session-legacy",
          participantAvatars: [
            "/api/v1/xiangwan/avatars/legacy/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002",
            "/api/v1/auth/principals/00000000-0000-0000-0000-000000000001/avatar/00000000-0000-0000-0000-000000000002",
          ],
          favoriteAvatars: ["/api/v1/xiangwan/avatars/legacy/not-a-uuid/not-a-uuid"],
        },
      ],
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.deepEqual(page.cards[0].participantAvatars, [
    "https://api.example.com/api/v1/xiangwan/avatars/legacy/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002",
  ]);
  assert.deepEqual(page.cards[0].favoriteAvatars, []);
});

test("home projection preserves intentionally empty optional brand copy", () => {
  const page = projectHomePage({
    community_name: "",
    brand_intro: "",
    hero_eyebrow: "",
    hero_subtitle: "",
    cards: [],
  });

  assert.equal(page.communityName, "");
  assert.equal(page.brandIntro, "");
  assert.equal(page.heroEyebrow, "");
});

test("home card projection joins cover against the API origin and passes foreign https avatars through", () => {
  const page = projectHomePage(
    {
      cards: [
        {
          session_id: "session-1",
          cover_image_url: "/api/v1/xiangwan/covers/cover-1.webp",
          participant_avatars: [
            "/api/v1/xiangwan/avatars/a-1.webp",
            "/api/v1/xiangwan/avatars/a-2.webp",
            "/api/v1/xiangwan/avatars/a-3.webp",
            "/api/v1/xiangwan/avatars/a-4.webp",
          ],
        },
        { session_id: "session-2", cover_image_url: "", participant_avatars: [] },
        {
          session_id: "session-3",
          cover_image_url: "https://evil.example/cover.webp",
          participant_avatars: [
            "https://thirdwx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTLOZ4lJyZ8fGg/132",
            "http://evil.example/avatar.webp",
            "/api/v1/xiangwan/private/a.webp",
            42,
            "/api/v1/xiangwan/avatars/ok.webp",
          ],
        },
      ],
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  const [withMedia, withoutMedia, mixed] = page.cards;
  assert.equal(
    withMedia.coverImageUrl,
    "https://api.example.com/api/v1/xiangwan/covers/cover-1.webp",
  );
  assert.deepEqual(withMedia.participantAvatars, [
    "https://api.example.com/api/v1/xiangwan/avatars/a-1.webp",
    "https://api.example.com/api/v1/xiangwan/avatars/a-2.webp",
    "https://api.example.com/api/v1/xiangwan/avatars/a-3.webp",
  ]);
  assert.equal(withoutMedia.coverImageUrl, "");
  assert.deepEqual(withoutMedia.participantAvatars, []);
  // 封面仍走严格模式:跨源绝对 URL 拒绝;头像放行微信 CDN 等 https 外链,
  // 但 http 绝对地址与非白名单相对路径依旧拒绝。
  assert.equal(mixed.coverImageUrl, "");
  assert.deepEqual(mixed.participantAvatars, [
    "https://thirdwx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTLOZ4lJyZ8fGg/132",
    "https://api.example.com/api/v1/xiangwan/avatars/ok.webp",
  ]);
});

test("home projection fails closed when a retained open card crosses its trusted boundary", () => {
  const page = projectHomePage(
    {
      cards: [
        {
          session_id: "session-1",
          registration_start_at: "2026-09-10T07:00:00Z",
          registration_end_at: "2026-09-19T07:00:00Z",
          session_start_at: "2026-09-20T07:00:00Z",
          session_end_at: "2026-09-21T07:00:00Z",
          display: { state: "open", sellable_capacity: 2 },
          cta_label: "register_now",
        },
      ],
    },
    { now: Date.parse("2026-09-21T08:00:00Z") },
  );
  assert.equal(page.cards[0].state, "ended");
  assert.equal(page.cards[0].stateLabel, "活动已结束");
  assert.equal(page.cards[0].ctaLabel, "活动已结束");
});

test("home card media stays empty without an API origin", () => {
  const page = projectHomePage({
    cards: [
      {
        session_id: "session-1",
        cover_image_url: "/api/v1/xiangwan/covers/cover-1.webp",
        participant_avatars: ["/api/v1/xiangwan/avatars/a-1.webp"],
      },
    ],
  });
  assert.equal(page.cards[0].coverImageUrl, "");
  assert.deepEqual(page.cards[0].participantAvatars, []);
});

test("detail projection joins the cover image against the API origin", () => {
  const detail = projectSessionDetail(
    {
      session_id: "session-1",
      cover_image_url: "/api/v1/xiangwan/covers/cover-1.webp",
      delivery: {},
      display: {},
      cta: {},
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.equal(detail.coverImageUrl, "https://api.example.com/api/v1/xiangwan/covers/cover-1.webp");
  const withoutCover = projectSessionDetail({ delivery: {}, display: {}, cta: {} });
  assert.equal(withoutCover.coverImageUrl, "");
});

test("capacity wording reserves remaining slots for open Sessions", () => {
  for (const state of ["open", "open_low_stock", "open_need_group"]) {
    assert.equal(
      capacityText({
        state,
        capacity: 12,
        confirmed_registration_count: 4,
        sellable_capacity: 8,
      }),
      "剩余 8 个名额",
    );
  }
  for (const state of [
    "cancelled",
    "recurring_gap",
    "ended",
    "in_progress",
    "not_open",
    "closed",
    "full",
    "temporarily_locked_full",
  ]) {
    assert.equal(
      capacityText({
        state,
        capacity: 12,
        confirmed_registration_count: 4,
        sellable_capacity: 8,
      }),
      "已确认 4 / 12 人",
    );
  }
  assert.equal(capacityText({ state: "ended", sellable_capacity: 8 }), "名额状态以活动方为准");
});

test("detail projection exposes the prototype registration summary facts", () => {
  const detail = projectSessionDetail({
    display: {
      state: "open",
      capacity: 20,
      confirmed_registration_count: 1,
      needed_to_reach_group_minimum: 3,
    },
    delivery: {},
    cta: {},
  });
  assert.equal(detail.registrationCountText, "已报名 1 人 / 限额 20 人");
  assert.equal(detail.groupMinimumText, "成团还需 3 人");
});

test("detail title uses the Instance with the Session as a secondary line", () => {
  const detail = projectSessionDetail({
    instance_title: "第 12 期 · AI 共创夜",
    session_title: "9 月 26 日晚场",
    delivery: {},
    display: {},
    cta: {},
  });
  assert.equal(detail.title, "第 12 期 · AI 共创夜");
  assert.equal(detail.sessionTitle, "9 月 26 日晚场");
});

test("detail projection renders configured quick tag labels and falls back to stable codes", () => {
  const detail = projectSessionDetail({
    activity_type: "custom",
    quick_tag_codes: ["newcomer", "unknown_tag"],
    quick_tags: [{ code: "newcomer", label: "新人友好" }],
    delivery: { mode: "offline" },
    display: {},
    cta: {},
  });
  assert.deepEqual(detail.tagItems, ["#线下", "#自定义活动", "#新人友好", "#unknown_tag"]);
});

test("detail projection opens paid registration only with the public payment capability", () => {
  const raw = {
    session_id: "session-1",
    price_cents: 9900,
    delivery: {},
    display: { state: "open", sellable_capacity: 3 },
    cta: { action: "start_registration", label: "register_now", enabled: true },
  };
  const detail = projectSessionDetail(raw);
  assert.equal(detail.registrationReady, false);
  assert.equal(detail.paidRegistrationPending, true);
  const enabled = projectSessionDetail(raw, { wechatPaymentAvailable: true });
  assert.equal(enabled.registrationReady, true);
  assert.equal(enabled.paidRegistrationPending, false);
  assert.equal(enabled.ctaLabel, "报名并支付");
});

test("detail projection closes an open CTA after its trusted registration window", () => {
  const detail = projectSessionDetail(
    {
      session_id: "session-1",
      registration_start_at: "2026-09-10T07:00:00Z",
      registration_end_at: "2026-09-19T07:00:00Z",
      session_start_at: "2026-09-20T07:01:00Z",
      session_end_at: "2026-09-21T07:01:00Z",
      price_cents: 0,
      display: { state: "open", sellable_capacity: 3 },
      cta: { action: "start_registration", label: "register_now", enabled: true },
    },
    { now: Date.parse("2026-09-19T08:00:00Z") },
  );

  assert.equal(detail.state, "closed");
  assert.equal(detail.stateLabel, "报名已结束");
  assert.equal(detail.ctaLabel, "报名已结束");
  assert.equal(detail.ctaEnabled, false);
  assert.equal(detail.registrationReady, false);
});

test("detail projection advances an open CTA to in-progress after session start", () => {
  const detail = projectSessionDetail(
    {
      registration_start_at: "2026-09-10T07:00:00Z",
      registration_end_at: "2026-09-19T07:00:00Z",
      session_start_at: "2026-09-20T07:01:00Z",
      session_end_at: "2026-09-21T07:01:00Z",
      price_cents: 0,
      display: { state: "open", sellable_capacity: 3 },
      cta: { action: "start_registration", label: "register_now", enabled: true },
    },
    { now: Date.parse("2026-09-20T08:00:00Z") },
  );

  assert.equal(detail.state, "in_progress");
  assert.equal(detail.stateLabel, "活动进行中");
  assert.equal(detail.ctaLabel, "活动进行中");
  assert.equal(detail.registrationReady, false);
});

test("detail projection preserves canonical history and offline map coordinates", () => {
  const detail = projectSessionDetail({
    series_id: "series-1",
    session_id: "session-1",
    successful_published_instance_count: 4,
    historical_registration_count: 28,
    price_cents: 0,
    delivery: {
      mode: "offline",
      venue_name: "海河实验室",
      address: "天津市河西区创新路 18 号",
      longitude: 117.2,
      latitude: 39.1,
    },
    display: {},
    cta: {},
  });

  assert.equal(detail.seriesId, "series-1");
  assert.equal(detail.successfulPublishedInstanceCount, 4);
  assert.equal(detail.historicalRegistrationCount, 28);
  assert.equal(detail.historyText, "已举办 4 期 · 累计 28 人参加");
  assert.equal(detail.longitude, 117.2);
  assert.equal(detail.latitude, 39.1);
  assert.equal(detail.canOpenLocation, true);
});

test("detail projection disables malformed prices and non-registration actions", () => {
  const invalidPrice = projectSessionDetail({
    price_cents: "free",
    delivery: {},
    display: {},
    cta: { action: "start_registration", label: "register_now", enabled: true },
  });
  const wrongAction = projectSessionDetail({
    price_cents: 0,
    delivery: {},
    display: {},
    cta: { action: "none", label: "register_now", enabled: true },
  });
  assert.equal(invalidPrice.ctaEnabled, false);
  assert.equal(wrongAction.ctaEnabled, false);
});

test("detail projection defaults the new content sections when the contract omits them", () => {
  const detail = projectSessionDetail({ delivery: {}, display: {}, cta: {} });
  assert.deepEqual(detail.contentBlocks, []);
  assert.deepEqual(detail.leaders, []);
  assert.equal(detail.previousReview, null);
});

test("detail projection orders content blocks, drops invalid entries and joins image urls", () => {
  const detail = projectSessionDetail(
    {
      session_id: "session-1",
      delivery: {},
      display: {},
      cta: {},
      content_blocks: [
        { type: "text", title: " 活动亮点 ", body: "  深度共创  " },
        { type: "text", title: "仅标题" },
        { type: "image", url: "/api/v1/xiangwan/covers/c-1.webp", caption: "现场" },
        { type: "image", url: "https://api.example.com/api/v1/xiangwan/covers/c-2.webp" },
        { type: "image", url: "https://evil.example/api/v1/xiangwan/covers/c-3.webp" },
        { type: "image", caption: "缺 url" },
        { type: "text", title: " ", body: "" },
        { type: "video", url: "/api/v1/xiangwan/covers/v.mp4" },
        null,
        "garbage",
      ],
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.deepEqual(detail.contentBlocks, [
    { type: "text", title: "活动亮点", body: "深度共创" },
    { type: "text", title: "仅标题", body: "" },
    {
      type: "image",
      imageUrl: "https://api.example.com/api/v1/xiangwan/covers/c-1.webp",
      caption: "现场",
    },
    {
      type: "image",
      imageUrl: "https://api.example.com/api/v1/xiangwan/covers/c-2.webp",
      caption: "",
    },
  ]);
});

test("detail projection keeps named leaders and falls back to initial avatars", () => {
  const detail = projectSessionDetail(
    {
      delivery: {},
      display: {},
      cta: {},
      leaders: [
        {
          display_name: " 陆远 ",
          role_label: "主持人",
          headline: "连续创业者",
          avatar_url: "/api/v1/xiangwan/avatars/lu.webp",
        },
        {
          display_name: "陈曦",
          role_label: "分享嘉宾",
          avatar_url: "https://thirdwx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTLOZ4lJyZ8fGg/132",
        },
        { display_name: "王芳", role_label: "邀约嘉宾", avatar_url: "" },
        { display_name: "  ", role_label: "空白姓名" },
        { role_label: "缺失姓名" },
        null,
      ],
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.deepEqual(detail.leaders, [
    {
      displayName: "陆远",
      roleLabel: "主持人",
      headline: "连续创业者",
      avatarUrl: "https://api.example.com/api/v1/xiangwan/avatars/lu.webp",
      avatarText: "陆",
    },
    {
      displayName: "陈曦",
      roleLabel: "分享嘉宾",
      headline: "",
      avatarUrl: "https://thirdwx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTLOZ4lJyZ8fGg/132",
      avatarText: "陈",
    },
    {
      displayName: "王芳",
      roleLabel: "邀约嘉宾",
      headline: "",
      avatarUrl: "",
      avatarText: "王",
    },
  ]);
});

test("detail projection joins previous review media, caps at three and requires an instance id", () => {
  const detail = projectSessionDetail(
    {
      delivery: {},
      display: {},
      cta: {},
      previous_review: {
        instance_id: " instance-9 ",
        session_id: "session-8",
        title: "第 8 期",
        completed_at: "2026-09-06T12:00:00Z",
        image_urls: [
          "/api/v1/xiangwan/media/aa/bb",
          "https://api.example.com/api/v1/xiangwan/media/cc/dd",
          "https://evil.example/api/v1/xiangwan/media/ee/ff",
          "/api/v1/xiangwan/media/00/11",
          "/api/v1/xiangwan/media/22/33",
        ],
      },
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.deepEqual(detail.previousReview, {
    instanceId: "instance-9",
    sessionId: "session-8",
    title: "第 8 期",
    completedAt: "09-06 20:00",
    imageUrls: [
      "https://api.example.com/api/v1/xiangwan/media/aa/bb",
      "https://api.example.com/api/v1/xiangwan/media/cc/dd",
      "https://api.example.com/api/v1/xiangwan/media/00/11",
    ],
  });

  const withoutInstance = projectSessionDetail(
    {
      delivery: {},
      display: {},
      cta: {},
      previous_review: { title: "缺 instance id", image_urls: ["/api/v1/xiangwan/media/aa/bb"] },
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.equal(withoutInstance.previousReview, null);

  const linkOnly = projectSessionDetail(
    {
      delivery: {},
      display: {},
      cta: {},
      previous_review: {
        instance_id: "instance-link-only",
        title: "第 7 期",
        completed_at: "2026-09-01T12:00:00Z",
        image_urls: [],
      },
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.deepEqual(linkOnly.previousReview, {
    instanceId: "instance-link-only",
    title: "第 7 期",
    completedAt: "09-01 20:00",
    imageUrls: [],
  });
});

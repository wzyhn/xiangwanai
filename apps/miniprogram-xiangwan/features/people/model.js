"use strict";

const { formatDateTime } = require("../../utils/format");

function normalizeText(value) {
  return String(value || "").trim();
}

function summarizeText(value, maximumRunes = 96) {
  const normalized = normalizeText(value);
  const runes = Array.from(normalized);
  if (runes.length <= maximumRunes) return normalized;
  return `${runes.slice(0, maximumRunes).join("")}…`;
}

function projectPerson(person = {}) {
  const introduction = normalizeText(person.introduction);
  const displayName = normalizeText(person.display_name);
  return {
    peopleId: normalizeText(person.people_id),
    displayName,
    avatarText: Array.from(displayName)[0] || "享",
    headline: normalizeText(person.headline),
    introduction,
    introductionSummary: summarizeText(introduction),
    hasIntroduction: Boolean(introduction),
    publishedAt: formatDateTime(person.published_at),
  };
}

function projectPeoplePage(page = {}) {
  return {
    items: (Array.isArray(page.items) ? page.items : []).map(projectPerson),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
    asOf: page.as_of ? formatDateTime(page.as_of) : "",
  };
}

function projectPersonDetail(detail = {}) {
  return projectPerson(detail.person);
}

module.exports = {
  projectPeoplePage,
  projectPerson,
  projectPersonDetail,
  summarizeText,
};

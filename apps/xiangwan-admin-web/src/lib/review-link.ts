export type ReviewLinkDraft = {
  added: boolean;
  enabled: boolean;
  title: string;
  subtitle: string;
  url: string;
};

export function reviewLinkRequest(draft: ReviewLinkDraft) {
  if (!draft.added) return undefined;
  if (!draft.title.trim()) throw new Error("已添加的资料需要填写显示名称");
  return {
    enabled: draft.enabled,
    title: draft.title.trim(),
    subtitle: draft.subtitle.trim(),
    url: draft.url.trim(),
  };
}

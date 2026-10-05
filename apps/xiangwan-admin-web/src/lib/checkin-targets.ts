import { api } from "@/lib/api";
import type { CheckinTarget, Page } from "@/lib/types";

const checkinTargetPageSize = 100;

export async function listAllCheckinTargets(): Promise<CheckinTarget[]> {
  const targets: CheckinTarget[] = [];
  for (let page = 1; ; page += 1) {
    const result = await api<Page<CheckinTarget>>(
      `/checkin-targets?page=${page}&page_size=${checkinTargetPageSize}`,
    );
    targets.push(...result.items);
    if (result.items.length === 0 || targets.length >= result.total) return targets;
  }
}

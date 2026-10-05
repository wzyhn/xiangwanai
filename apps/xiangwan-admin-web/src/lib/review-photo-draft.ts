export type ReviewPhotoDraft = {
  id: number;
  url: string;
  enabled: boolean;
};

/**
 * Return only selected, non-empty photo URLs in the exact editor order.
 * Published Content is immutable, so this is the last safe point to choose
 * and sort the photo set before it enters the write chain.
 */
export function selectedReviewPhotoURLs(rows: ReviewPhotoDraft[]): string[] {
  return rows
    .filter((row) => row.enabled)
    .map((row) => row.url.trim())
    .filter(Boolean);
}

export function moveReviewPhoto(
  rows: ReviewPhotoDraft[],
  index: number,
  direction: -1 | 1,
): ReviewPhotoDraft[] {
  const target = index + direction;
  if (index < 0 || index >= rows.length || target < 0 || target >= rows.length) {
    return rows;
  }
  const next = rows.slice();
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

export function removeReviewPhoto(
  rows: ReviewPhotoDraft[],
  index: number,
): ReviewPhotoDraft[] {
  if (index < 0 || index >= rows.length) return rows;
  return rows.filter((_row, rowIndex) => rowIndex !== index);
}

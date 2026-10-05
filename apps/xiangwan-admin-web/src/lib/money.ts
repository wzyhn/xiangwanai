export function formatCentsAsYuan(cents: string): string {
  if (!/^\d+$/.test(cents)) return "金额待核对";
  const amount = BigInt(cents);
  return `¥${amount / 100n}.${String(amount % 100n).padStart(2, "0")}`;
}

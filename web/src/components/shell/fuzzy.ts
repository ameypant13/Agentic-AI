/**
 * Small fuzzy matcher for the command palette. Returns a score (higher is better) or -1 for no match.
 * Every query word must match the text, either as a substring (best, earlier is better) or as an
 * in-order subsequence (worse, tighter is better).
 */
export function fuzzyScore(query: string, text: string): number {
  const q = query.trim().toLowerCase();
  if (!q) return 0;
  const t = text.toLowerCase();
  let total = 0;
  for (const word of q.split(/\s+/)) {
    const s = wordScore(word, t);
    if (s < 0) return -1;
    total += s;
  }
  return total;
}

function wordScore(w: string, t: string): number {
  const at = t.indexOf(w);
  if (at === 0) return 100 + w.length;
  if (at > 0) {
    const boundary = /[\s\-_./·:]/.test(t[at - 1]);
    return (boundary ? 80 : 60) + w.length - Math.min(at, 20) / 10;
  }
  // subsequence
  let ti = 0;
  let first = -1;
  let last = -1;
  for (const ch of w) {
    const i = t.indexOf(ch, ti);
    if (i < 0) return -1;
    if (first < 0) first = i;
    last = i;
    ti = i + 1;
  }
  const spread = last - first + 1 - w.length;
  return Math.max(1, 40 - spread);
}

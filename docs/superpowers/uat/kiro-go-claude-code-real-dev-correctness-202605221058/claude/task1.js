export function alpha(values) {
  const counts = {};
  for (const value of values) {
    if (typeof value === 'string' && value.length > 0) {
      counts[value] = (counts[value] ?? 0) + 1;
    }
  }
  return counts;
}
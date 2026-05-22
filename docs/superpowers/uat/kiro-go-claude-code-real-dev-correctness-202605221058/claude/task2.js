export function sumEven(values) {
  return values.filter((n) => n % 2 === 0).reduce((a, b) => a + b, 0);
}
import type { Chain, Server } from "./types";

export const labelOf = (x: Server | Chain) => ("hops" in x ? (x.name || x.tag || "цепочка") : (x.name || x.server));

// "1 хоп", "2 хопа", "5 хопов", "21 хоп".
export function hopsLabel(n: number): string {
  const d = n % 10, dd = n % 100;
  const w = d === 1 && dd !== 11 ? "хоп" : d >= 2 && d <= 4 && (dd < 12 || dd > 14) ? "хопа" : "хопов";
  return `${n} ${w}`;
}

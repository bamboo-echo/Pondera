import type { PluginMarketplaceEntryView } from "../generated/desktopContract.generated";

// Marketplace filtering is shared by the panel and its tests. Category, domain
// and search compose with AND semantics; domains themselves are aggregated from
// the entries, so a new index domain needs no frontend change.

export type MarketplaceCategory =
  | "all"
  | "recommended"
  | "dev"
  | "productivity"
  | "design"
  | "research"
  | "content"
  | "other";

export const ALL_DOMAINS = "all";

export interface MarketplaceFilter {
  query: string;
  category: MarketplaceCategory;
  domain: string;
}

export interface MarketplaceDomainOption {
  domain: string;
  count: number;
}

// Long-tail domains stay out of the chip row: a chip is only offered once at
// least two entries declare it.
export const MIN_DOMAIN_CHIP_COUNT = 2;

export function marketplaceDomainOptions(
  entries: readonly PluginMarketplaceEntryView[],
  minCount = MIN_DOMAIN_CHIP_COUNT,
): MarketplaceDomainOption[] {
  const counts = new Map<string, number>();
  for (const entry of entries) {
    const domain = (entry.domain ?? "").trim();
    if (!domain) continue;
    counts.set(domain, (counts.get(domain) ?? 0) + 1);
  }
  return Array.from(counts, ([domain, count]) => ({ domain, count }))
    .filter((option) => option.count >= minCount)
    .sort((a, b) => b.count - a.count || a.domain.localeCompare(b.domain, "zh"));
}

export function filterMarketplaceEntries(
  entries: readonly PluginMarketplaceEntryView[],
  filter: MarketplaceFilter,
): PluginMarketplaceEntryView[] {
  const query = filter.query.trim().toLowerCase();
  return entries.filter((entry) => {
    if (
      query &&
      ![entry.name, entry.description, entry.author, entry.source, entry.domain]
        .filter(Boolean)
        .join(" ")
        .toLowerCase()
        .includes(query)
    ) {
      return false;
    }
    if (filter.category === "recommended" && !entry.featured) return false;
    if (filter.category !== "all" && filter.category !== "recommended" && entry.category !== filter.category) {
      return false;
    }
    if (filter.domain !== ALL_DOMAINS && (entry.domain ?? "").trim() !== filter.domain) return false;
    return true;
  });
}

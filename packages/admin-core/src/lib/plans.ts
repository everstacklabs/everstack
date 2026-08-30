import { formatBytes } from "./utils";
import plansConfig from "../../../../pkg/plans/plans.json";

// Type definitions for billing and plans

export type BillingPeriod = "monthly" | "yearly";

export interface PlanFeature {
  name: string;
  enabled: boolean;
}

export interface PlanUsageLimit {
  type: string;
  value: number;
  subText?: string;
}

export interface PerSeatPricing {
  monthly: string;
  yearly: string;
  subText?: string;
}

export interface Plan {
  tier: string;
  name: string;
  description?: string;
  trial_duration_days?: number;
  pricing: {
    monthly: string;
    yearly: string;
    discounted?: string;
    suggested?: string;
    per_seat?: PerSeatPricing;
  };
  highlight: boolean;
  seat_limit: number; // Max users allowed (0 = unlimited)
  instance_limit?: number; // Max instances allowed (0 = unlimited)
  features: PlanFeature[];
  usage_limits: PlanUsageLimit[];
}

// API response type for billing plans endpoint
export interface PlansApiResponse {
  synced: boolean;
  plans?: unknown;
  plans_config?: Record<string, Plan>;
}

// Formatted plan limits for display
export interface FormattedPlanLimits {
  rpm: string;
  tokens: string;
  requests: string;
}

// Plan features with formatted limits
export interface PlanFeatures {
  name: string;
  limits: FormattedPlanLimits;
}

const canonicalPlans = plansConfig.plans as Record<string, Plan>;

export function getCanonicalPlan(tier: string): Plan | undefined {
  return canonicalPlans[tier.toLowerCase()];
}

export function getCanonicalPlans(): Plan[] {
  const orderedTiers = ["free", "basic", "pro", "enterprise"];
  return orderedTiers
    .map((tier) => canonicalPlans[tier])
    .filter((plan): plan is Plan => Boolean(plan));
}

// Fallback plan limits when the API is not available, derived from the
// canonical build-time plans data so display values cannot drift.
export const FALLBACK_PLAN_LIMITS: Record<string, PlanFeatures> =
  Object.fromEntries(
    Object.entries(canonicalPlans).map(([tier, plan]) => [
      tier,
      { name: plan.name, limits: getPlanLimits(plan) },
    ]),
  );

// Plan tier ranking for upgrade/downgrade detection
export const PLAN_RANK: Record<string, number> = {
  free: 0,
  basic: 1,
  pro: 2,
  enterprise: 3,
};

// Plan accent colors + default CTA labels. Mirror of the landing
// page's planMeta (apps/landing/src/lib/plans.ts) so the in-product
// Plans tab uses the same brand palette.
export const PLAN_META: Record<string, { accent: string; cta: string }> = {
  free: { accent: "#06b6d4", cta: "Get started" },
  basic: { accent: "#3b82f6", cta: "Upgrade" },
  pro: { accent: "#a78bfa", cta: "Upgrade" },
  enterprise: { accent: "#34d399", cta: "Contact sales" },
};

// Seat-count copy. Mirrors the landing page's formatSeats so cards
// across pricing-preview and the in-product Plans tab read the same.
export function formatSeatsLabel(seatLimit: number): string {
  if (seatLimit === 0) return "Unlimited seats";
  if (seatLimit === 1) return "1 seat";
  return `${seatLimit} seats included`;
}

// Helper function to get human-readable feature descriptions.
// Programmer names from plans.json get mapped to copy that's safe to
// show on a public pricing card. Anything not in the map falls
// through to a Title-Case fallback so an unknown flag still renders
// something readable rather than `persistent_troopers`.
export function getFeatureDescription(feature: PlanFeature): string {
  const descriptions: Record<string, string> = {
    core_api: "AI Gateway & platform API",
    persistent_troopers: "Always-on agent instances",
    persistent_agents: "Always-on agents",
    channel_bindings: "Slack, Discord & Telegram connections",
    agent_spawning: "Multi-agent delegation",
    evaluations: "Evaluations & experiments",
    alerts: "Alerts & notifications",
    sandbox_firecracker: "Firecracker-isolated sandboxes",
    sandbox_kubernetes: "Kubernetes sandboxes",
    browser_headed: "Live browser viewport",
    memory_external: "External vector memory",
    advanced_analytics: "Advanced analytics",
    custom_integrations: "Custom integrations",
    sso: "SSO & SAML",
    audit_logs: "Audit logs",
  };
  const known = descriptions[feature.name];
  if (known) return known;
  // Fallback: snake_case → Title Case
  return feature.name
    .split("_")
    .map((w) => (w.length > 0 ? w[0]!.toUpperCase() + w.slice(1) : w))
    .join(" ");
}

// Human-readable labels and tooltips for usage limit types
const LIMIT_LABELS: Record<string, { label: string; tooltip: string }> = {
  RPM: { label: "Requests/min", tooltip: "Maximum API requests per minute" },
  TOKENS: {
    label: "Tokens",
    tooltip: "Total LLM tokens (input + output) per billing period",
  },
  REQUESTS: {
    label: "Requests",
    tooltip: "Total API requests per billing period",
  },
  STORAGE_BYTES: {
    label: "Storage",
    tooltip:
      "Object storage for datasets, artifacts, uploads, and evaluation results",
  },
  DATASET_ITEMS: {
    label: "Dataset items",
    tooltip: "Total items across all datasets",
  },
  EVAL_RUNS_MONTHLY: {
    label: "Eval runs/month",
    tooltip: "Evaluation runs per calendar month",
  },
  ANNOTATION_QUEUES: {
    label: "Annotation queues",
    tooltip: "Active annotation queues for human feedback",
  },
  AGENTS: { label: "Agents", tooltip: "Total agent definitions" },
  PERSISTENT_AGENTS: {
    label: "Always-on agents",
    tooltip: "Agents with a persistent runtime and sandbox",
  },
  PERSISTENT_TROOPERS: {
    label: "Persistent instances",
    tooltip: "Long-running instance processes",
  },
  CONCURRENT_RUNNING: {
    label: "Concurrent agent runs",
    tooltip: "Maximum agents running simultaneously",
  },
  CONCURRENT_SANDBOXES: {
    label: "Concurrent sandboxes",
    tooltip:
      "Maximum sandboxes with compute allocated at the same time per instance",
  },
  CONCURRENT_BROWSERS: {
    label: "Concurrent browser sessions",
    tooltip:
      "Maximum tenant-isolated Chromium sessions running at the same time",
  },
  BROWSER_SESSION_MAX_SECONDS: {
    label: "Maximum browser session",
    tooltip: "Maximum duration of one hosted browser session",
  },
  SANDBOX_MEMORY_MB: {
    label: "Sandbox memory",
    tooltip: "Maximum memory per sandbox instance (MB)",
  },
  MESSAGES_MONTHLY: {
    label: "Channel messages/month",
    tooltip: "Channel messages per billing period",
  },
  CHANNELS: {
    label: "Connected channels",
    tooltip: "Configured platform connections (a Slack workspace, a Discord bot, …)",
  },
  CHANNEL_BINDINGS: {
    label: "Agent channel connections",
    tooltip: "Active channel integrations (Slack, Discord, etc.)",
  },
  SPAWN_DEPTH: {
    label: "Delegation depth",
    tooltip: "Maximum agent delegation depth",
  },
  SESSION_RETENTION_DAYS: {
    label: "Days of session history",
    tooltip: "Days sessions are retained before expiry",
  },
};

export function getLimitLabel(type: string): string {
  return LIMIT_LABELS[type]?.label ?? type.toLowerCase().replace(/_/g, " ");
}

export function getLimitTooltip(type: string): string {
  return LIMIT_LABELS[type]?.tooltip ?? "";
}

// Helper to format plan limit values for display
function formatPlanLimitValue(value: number): string {
  if (value === -1) return "Unlimited";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(0)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(0)}k`;
  return value.toLocaleString();
}

// Helper function to format usage limits
export function formatUsageLimit(limit: PlanUsageLimit): string {
  const label = getLimitLabel(limit.type);
  if (limit.value === -1) {
    return `Unlimited ${label.toLowerCase()}`;
  }
  if (limit.type === "STORAGE_BYTES") {
    return `${formatBytes(limit.value)} ${label.toLowerCase()}`;
  }
  if (limit.type === "BROWSER_SESSION_MAX_SECONDS") {
    const hours = limit.value / 3600;
    const duration =
      Number.isInteger(hours) && hours >= 1
        ? `${hours} ${hours === 1 ? "hour" : "hours"}`
        : `${limit.value / 60} minutes`;
    return `${duration} ${label.toLowerCase()}`;
  }
  return `${formatPlanLimitValue(limit.value)} ${label.toLowerCase()}`;
}

// Get formatted limits from a plan's usage_limits array
export function getPlanLimits(plan: Plan | undefined): FormattedPlanLimits {
  if (!plan) return { rpm: "N/A", tokens: "N/A", requests: "N/A" };

  const limits: FormattedPlanLimits = {
    rpm: "N/A",
    tokens: "N/A",
    requests: "N/A",
  };

  for (const limit of plan.usage_limits || []) {
    const formatted = formatPlanLimitValue(limit.value);
    switch (limit.type) {
      case "RPM":
        limits.rpm = limit.value === -1 ? "Unlimited" : `${formatted} RPM`;
        break;
      case "TOKENS":
        limits.tokens =
          limit.value === -1 ? "Unlimited" : `${formatted} tokens/mo`;
        break;
      case "REQUESTS":
        limits.requests =
          limit.value === -1 ? "Unlimited" : `${formatted} requests/mo`;
        break;
    }
  }

  return limits;
}

// Get plan features with fallback to canonical build-time values
export function getPlanFeatures(
  tier: string,
  plansConfig: Record<string, Plan> | null,
): PlanFeatures {
  const normalizedTier = tier.toLowerCase();

  // Try to get from API data first
  if (plansConfig && plansConfig[normalizedTier]) {
    const plan = plansConfig[normalizedTier];
    return {
      name: plan.name,
      limits: getPlanLimits(plan),
    };
  }

  // Fall back to the canonical build-time plan data.
  return FALLBACK_PLAN_LIMITS[normalizedTier] || {
    name: tier.charAt(0).toUpperCase() + tier.slice(1),
    limits: { rpm: "N/A", tokens: "N/A", requests: "N/A" },
  };
}

// Check if changing from current tier to target tier is a downgrade
export function isDowngrade(currentTier: string, targetTier: string): boolean {
  const currentRank = PLAN_RANK[currentTier.toLowerCase()] ?? 0;
  const targetRank = PLAN_RANK[targetTier.toLowerCase()] ?? 0;
  return targetRank < currentRank;
}

// Check if changing from current tier to target tier is an upgrade
export function isUpgrade(currentTier: string, targetTier: string): boolean {
  const currentRank = PLAN_RANK[currentTier.toLowerCase()] ?? 0;
  const targetRank = PLAN_RANK[targetTier.toLowerCase()] ?? 0;
  return targetRank > currentRank;
}

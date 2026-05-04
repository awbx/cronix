import { CronExpressionParser } from "cron-parser";

const SHORTCUTS: Record<string, string> = {
  "@hourly": "0 * * * *",
  "@daily": "0 0 * * *",
  "@midnight": "0 0 * * *",
  "@weekly": "0 0 * * 0",
  "@monthly": "0 0 1 * *",
  "@yearly": "0 0 1 1 *",
  "@annually": "0 0 1 1 *",
};

const EVERY_DURATION = /^@every\s+(?<n>\d+)(?<unit>s|m|h)$/i;

/**
 * Validate (but do not normalize) a schedule expression.
 *
 * Returns null on success; otherwise an error message.
 *
 * The five-field cron expression and the documented shortcuts (D-004) are
 * accepted. `@every <duration>` is accepted only with whole-second
 * resolution where the resulting interval is at least one minute (the v1
 * resolution floor — see Limitation 3).
 */
export function validateSchedule(expr: string): string | null {
  if (typeof expr !== "string" || expr.length === 0) {
    return "schedule must be a non-empty string";
  }
  const trimmed = expr.trim();
  if (trimmed in SHORTCUTS) return null;

  const everyMatch = trimmed.match(EVERY_DURATION);
  if (everyMatch?.groups) {
    const n = Number(everyMatch.groups.n);
    const unit = everyMatch.groups.unit?.toLowerCase();
    if (!Number.isInteger(n) || n <= 0) return "@every duration must be a positive integer";
    const seconds = unit === "s" ? n : unit === "m" ? n * 60 : n * 3600;
    if (seconds < 60) return "@every duration must be at least 60 seconds (v1 resolution floor)";
    return null;
  }

  const fields = trimmed.split(/\s+/);
  if (fields.length !== 5) {
    return `schedule must be 5 cron fields or a documented shortcut, got ${fields.length} field(s)`;
  }
  try {
    CronExpressionParser.parse(trimmed);
    return null;
  } catch (e) {
    return `invalid cron expression: ${(e as Error).message}`;
  }
}

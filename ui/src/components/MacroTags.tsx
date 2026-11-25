import { cx } from "@/cva.config";
import type { KeySequence } from "@hooks/stores";
import { MAX_TAG_LENGTH, MAX_TAGS_PER_MACRO } from "@/constants/macros";
import { m } from "@localizations/messages.js";

// Tags group macros ("windows", "macos", ...) so lists can be filtered.
// The KVM keeps them lower case and without duplicates; the same is done here.

// eslint-disable-next-line react-refresh/only-export-components
export function normalizeTags(tags: string[]): string[] {
  const out: string[] = [];
  for (const raw of tags) {
    const tag = raw.trim().toLowerCase().slice(0, MAX_TAG_LENGTH);
    if (tag && !out.includes(tag)) out.push(tag);
  }
  return out.slice(0, MAX_TAGS_PER_MACRO);
}

// "windows, macos" -> ["windows", "macos"]
// eslint-disable-next-line react-refresh/only-export-components
export function parseTags(text: string): string[] {
  return normalizeTags(text.split(","));
}

// Every tag in use, sorted.
// eslint-disable-next-line react-refresh/only-export-components
export function allMacroTags(macros: KeySequence[]): string[] {
  return [...new Set(macros.flatMap(macro => macro.tags ?? []))].sort((a, b) => a.localeCompare(b));
}

// Whether a macro passes the tag filter (null: all) and the search text
// (name or tag).
// eslint-disable-next-line react-refresh/only-export-components
export function macroMatches(macro: KeySequence, tag: string | null, query = ""): boolean {
  if (tag && !(macro.tags ?? []).includes(tag)) return false;
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return macro.name.toLowerCase().includes(q) || (macro.tags ?? []).some(t => t.includes(q));
}

export function TagChip({
  tag,
  active,
  onClick,
  size = "sm",
}: {
  tag: string;
  active?: boolean;
  onClick?: () => void;
  size?: "xs" | "sm";
}) {
  const className = cx(
    "inline-flex items-center rounded-full border font-medium whitespace-nowrap transition-colors",
    size === "xs" ? "px-1.5 py-0 text-[10px]" : "px-2 py-0.5 text-xs",
    active
      ? "border-blue-700 bg-blue-700 text-white dark:border-blue-500 dark:bg-blue-600"
      : "border-slate-800/20 bg-white text-slate-700 dark:border-slate-300/20 dark:bg-slate-800 dark:text-slate-300",
    onClick && !active && "cursor-pointer hover:bg-blue-50 dark:hover:bg-slate-700",
    onClick && active && "cursor-pointer",
  );
  if (!onClick) return <span className={className}>{tag}</span>;
  return (
    <button type="button" aria-pressed={active} className={className} onClick={onClick}>
      {tag}
    </button>
  );
}

// "All" and one chip per tag; clicking the chosen tag again shows all.
export function MacroTagFilter({
  tags,
  value,
  onChange,
  size = "sm",
}: {
  tags: string[];
  value: string | null;
  onChange: (tag: string | null) => void;
  size?: "xs" | "sm";
}) {
  if (tags.length === 0) return null;
  return (
    <div
      className="flex flex-wrap items-center gap-1"
      role="group"
      aria-label={m.macro_tags_filter()}
    >
      <TagChip
        tag={m.macro_tags_all()}
        active={value === null}
        size={size}
        onClick={() => onChange(null)}
      />
      {tags.map(tag => (
        <TagChip
          key={tag}
          tag={tag}
          size={size}
          active={value === tag}
          onClick={() => onChange(value === tag ? null : tag)}
        />
      ))}
    </div>
  );
}

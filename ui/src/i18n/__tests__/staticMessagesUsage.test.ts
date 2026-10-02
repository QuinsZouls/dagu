// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Catalog hygiene guard for the static message catalog (staticMessages.ts)
 * and the keyed catalog (messages.ts). See PLAN.md §2 decision D6.
 *
 * Leg (a) — missing keys: every literal key passed to I18nText/I18nTemplate
 *   (`text=`), translateStatic(...) or ts(...) must exist in
 *   staticMessages.en; every literal key passed to t(...) must exist in
 *   messages.en. Dynamic arguments (variables, template interpolation,
 *   ternaries) are intentionally not resolved — only literals are checked.
 *   A missing key renders the raw English string in every locale, so tsc
 *   cannot catch it (lookup keys are plain strings).
 *
 * Leg (b) — orphan keys: every key in staticMessages.en must appear as a
 *   string literal somewhere in production source (ui/src minus i18n/,
 *   minus the generated api/v1/schema.ts, minus __tests__). Test files are
 *   deliberately not counted: they match rendered text, they never consume
 *   the catalog, and several of them assert the ABSENCE of strings (e.g.
 *   "License Required"). Keys that never appear verbatim in source —
 *   template-built keys and legacy copy with no remaining producer — are
 *   listed explicitly in ALLOWLIST below.
 */
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';
import { messages } from '../messages';
import { staticMessages } from '../staticMessages';

const CWD = process.cwd();
const SRC = existsSync(join(CWD, 'src/i18n/staticMessages.ts'))
  ? join(CWD, 'src')
  : join(CWD, 'ui/src');

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, name.name);
    if (name.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

/** Production sources that consume the catalogs. */
function isProdSource(p: string): boolean {
  if (!/\.(ts|tsx)$/.test(p)) return false;
  if (p.includes('/i18n/')) return false;
  if (p.endsWith(join('api', 'v1', 'schema.ts'))) return false;
  if (p.includes('__tests__')) return false;
  return true;
}

function unescape(s: string): string {
  return s.replace(/\\(.)/g, (_m, c: string) =>
    c === 'n' ? '\n' : c === 't' ? '\t' : c
  );
}

/** Extract every single/double-quoted (and interpolation-free template)
 * string literal. Source is scanned line by line: TS string literals never
 * span lines, and line-scoped matching keeps stray apostrophes in JSX text
 * from swallowing neighbouring literals. */
const STRING_LITERAL =
  /'((?:\\.|[^'\\])*)'|"((?:\\.|[^"\\])*)"|`((?:\\.|[^`\\$])*)`/g;
function collectLiterals(src: string, into: Set<string>): void {
  for (const line of src.split('\n')) {
    STRING_LITERAL.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = STRING_LITERAL.exec(line))) {
      const value = unescape(m[1] ?? m[2] ?? m[3] ?? '');
      if (value) into.add(value);
    }
  }
}

type Site = { key: string; site: string };

/** Literal `text=` arguments of <I18nText> / <I18nTemplate> tags. */
function collectTagTextLiterals(src: string, file: string): Site[] {
  const out: Site[] = [];
  for (const tag of ['<I18nText', '<I18nTemplate']) {
    let idx = src.indexOf(tag);
    while (idx !== -1) {
      // Scan the tag span: quote-aware, brace-depth-aware, stop at `>`.
      let i = idx + tag.length;
      let depth = 0;
      let quote = '';
      let end = src.length;
      while (i < src.length) {
        const c = src[i];
        if (quote) {
          if (c === '\\') {
            i += 2;
            continue;
          }
          if (c === quote) quote = '';
        } else if (c === "'" || c === '"' || c === '`') {
          quote = c;
        } else if (c === '{') {
          depth++;
        } else if (c === '}') {
          depth--;
        } else if (depth === 0 && c === '>') {
          end = i;
          break;
        }
        i++;
      }
      const span = src.slice(idx, end);
      const m =
        /text\s*=\s*(?:\{\s*)?(?:"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)')/.exec(
          span
        );
      if (m) {
        const line = src.slice(0, idx).split('\n').length;
        out.push({
          key: unescape(m[1] ?? m[2] ?? ''),
          site: `${file}:${line}`,
        });
      }
      idx = src.indexOf(tag, idx + tag.length);
    }
  }
  return out;
}

const enStatic = staticMessages.en as Record<string, string>;
const enMessages = messages.en as Record<string, string>;

const prodFiles = walk(SRC).filter(isProdSource);

const prodLiterals = new Set<string>();
const missingStatic: Site[] = [];
const missingMessages: Site[] = [];

for (const f of prodFiles) {
  const src = readFileSync(f, 'utf8');
  const file = relative(SRC, f);
  collectLiterals(src, prodLiterals);

  for (const site of collectTagTextLiterals(src, file)) {
    if (!(site.key in enStatic)) missingStatic.push(site);
  }

  let m: RegExpExecArray | null;
  const tsRe = /\bts\(\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/g;
  while ((m = tsRe.exec(src))) {
    const key = unescape(m[2] ?? '');
    if (!(key in enStatic)) {
      missingStatic.push({
        key,
        site: `${file}:${src.slice(0, m.index).split('\n').length}`,
      });
    }
  }
  const staticRe = /\btranslateStatic\(\s*[^,()]+,\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/g;
  while ((m = staticRe.exec(src))) {
    const key = unescape(m[2] ?? '');
    if (!(key in enStatic)) {
      missingStatic.push({
        key,
        site: `${file}:${src.slice(0, m.index).split('\n').length}`,
      });
    }
  }
  const tRe = /(?<![\w.$])t\(\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/g;
  while ((m = tRe.exec(src))) {
    const key = unescape(m[2] ?? '');
    if (!(key in enMessages)) {
      missingMessages.push({
        key,
        site: `${file}:${src.slice(0, m.index).split('\n').length}`,
      });
    }
  }
}

/**
 * Keys in staticMessages.en that never appear as a plain literal in
 * production source. Two honest categories live side by side:
 *
 * 1. Dynamically built — the key only exists as the *result* of a template
 *    literal (e.g. `` ts(`Delete {count} ${suffix}`) `` in
 *    features/dag-runs/components/common/DAGRunBatchActions.tsx, or
 *    <I18nText text={`${size} lines`} /> in
 *    features/dags/components/dag-execution/LogPageSizeSelect.tsx) or is
 *    dispatched through a variable prop (e.g. priority labels "High"/"Low").
 *
 * 2. Legacy copy with no remaining producer. T9 may only prune keys
 *    matching /[Ll]icense/ (see PLAN.md §4 card T9), so these stay in the
 *    catalog and in this baseline until a dedicated cleanup card prunes
 *    them.
 */
const ALLOWLIST = new Set<string>([
  ', and',
  '! Please',
  '? This action cannot be undone.',
  '? This cannot be undone.',
  '. Are you sure?',
  '. Discard changes?',
  '. Password reset is available for all admins.',
  '. Please',
  '· Run',
  '” will be removed for everyone with access to this workspace scope. Workflows are not affected.',
  '). To deactivate, remove the environment variable and restart Dagu.',
  '1. Scope',
  '100 lines',
  '1000 lines',
  '10000 lines',
  '2. Send notifications',
  '3. Effective rules',
  '500 lines',
  '5000 lines',
  'Activate',
  'Activating...',
  'Add',
  'Add "',
  'Add another route',
  'Add route',
  'Allow callers to select an approved profile with',
  'Are you sure you want to',
  'Are you sure you want to delete',
  'at sign-in.',
  'Available Tools (',
  'channel',
  'Choose how requests authenticate to this webhook. If you enable HMAC, callers must send',
  'Commands (',
  'community',
  'Community Edition',
  'Community installs can manage up to 2 API keys.',
  'computed from the exact signature input shown below. Requests with',
  'Configure Workspace',
  'Create and test notification channels before using them in routes.',
  'Dagu uses the most specific configured scope: DAG, then workspace, then Global.',
  'DAGU-XXXX-XXXX-XXXX-XXXX',
  'Deactivate',
  'Deactivating...',
  'Default for every DAG unless a workspace or DAG is configured.',
  'Default for every DAG unless workspace or DAG settings are configured.',
  'Delete {count} Run',
  'Delete {count} Runs',
  'Delete Channel',
  'Discard local changes to',
  'Do you want to',
  'Do you want to dequeue',
  'Each channel can have one route per scope. Edit its events above, or add another channel.',
  'Email Delivery',
  'entries',
  'env var.',
  'event',
  'Expires',
  'expires in {count} day',
  'expires in {count} days',
  'expires today',
  'Features',
  'from',
  'from {scope}',
  'from sync tracking? Files remain in the remote repository.',
  'from sync tracking? This does not delete the file from the remote repository.',
  'from the remote repository and sync state. This action cannot be undone.',
  'from the remote repository, local disk, and sync state. This action cannot be undone.',
  'Global rules apply by default. Workspace and DAG settings override them only when configured.',
  'Grace Period',
  'has expired. Features will be disabled on',
  'High',
  'in the DAG YAML. It can also be inherited from',
  'Individual secrets that DAGs reference with',
  'Inherit Global',
  'Interactive shell connection to local server as',
  'is available as',
  'item',
  'items? This cannot be undone.',
  'lines',
  'Loaded',
  'Low',
  'managed by',
  'Managed by',
  'Medium',
  'missing item',
  'Modified',
  'more',
  'more step',
  'Multiple executions:',
  'Need a new Slack, email, webhook, or Telegram destination?',
  'No enabled route currently sends notifications.',
  'No runs on',
  'of',
  'or',
  'or containing a',
  'Override Global with workspace-specific rules.',
  'Overrides Global for {workspace}.',
  'Overrides Global for DAGs in this workspace.',
  'Page {page} of {total}',
  'page to activate one.',
  'Plan',
  'Remove',
  'renew',
  'renew now',
  'renew to avoid disruption',
  'Reschedule {count} Run',
  'Reschedule {count} Runs',
  'Reset Password for',
  'Resolved during a run (',
  'Retry {count} Run',
  'Retry {count} Runs',
  'Role and workspace access are',
  'Routes are disabled for this scope.',
  'routing unless you set a DAG override.',
  'Run one of the example workflows from the ',
  'Running Tasks (',
  'Searching',
  'Select a workspace to configure this.',
  'selected DAG run',
  'send to',
  'send to {channel}',
  'Showing',
  'Sibling Properties (',
  'sign',
  'Sign the exact input shown in the HMAC examples with your secret and send the hex digest in',
  'soon',
  'Start a workflow from the ',
  'Step details',
  'Supports tokens such as',
  'sync item',
  'tasks settled',
  'This DAG inherits',
  'This DAG uses',
  'this webhook?',
  'This will re-execute',
  'This will remove',
  'This workspace',
  'This workspace currently inherits Global rules.',
  'This workspace currently inherits Global rules. Workspace routes are ignored until Configure Workspace is selected.',
  'to a new path.',
  'to avoid disruption',
  'to expose selected request headers as',
  'to see activity here.',
  'to update',
  'trial',
  'Untracked',
  'Update “',
  'Update available: v',
  'updated by',
  'upgrade',
  'upgrade now',
  'upgrade to avoid disruption',
  'Use operational events',
  'Use the Global rules for this workspace.',
  'Use your webhook HMAC secret as',
  'User management features (create, edit, delete) require a',
  'Uses Global until configured.',
  'When any selected event happens',
  'Wiki pages under',
  'wikilink appear here.',
  'Workspace access is',
  'You have unsaved changes in',
  'Your Dagu',
]);

const formatSites = (sites: Site[]) =>
  sites.map((s) => `  ${s.key}  <- ${s.site}`).join('\n');

describe('static message catalog usage', () => {
  it('every literal key used via I18nText/I18nTemplate/translateStatic/ts exists in staticMessages.en', () => {
    expect(
      missingStatic,
      `keys used in ui/src but missing from staticMessages.en:\n${formatSites(
        missingStatic
      )}`
    ).toEqual([]);
  });

  it('every literal key used via t() exists in messages.en', () => {
    expect(
      missingMessages,
      `keys used in ui/src but missing from messages.en:\n${formatSites(
        missingMessages
      )}`
    ).toEqual([]);
  });

  it('every key in staticMessages.en is referenced by production source (or allowlisted)', () => {
    const unexpected = Object.keys(enStatic)
      .filter((k) => !prodLiterals.has(k) && !ALLOWLIST.has(k))
      .sort();
    expect(
      unexpected,
      `orphaned static keys (not a literal in ui/src, not in ALLOWLIST):\n  ${unexpected.join(
        '\n  '
      )}`
    ).toEqual([]);
  });

  it('allowlist stays in sync with the catalog', () => {
    const stale = [...ALLOWLIST]
      .filter((k) => k in enStatic ? prodLiterals.has(k) : true)
      .sort();
    expect(
      stale,
      `stale ALLOWLIST entries (now referenced, or no longer catalog keys):\n  ${stale.join(
        '\n  '
      )}`
    ).toEqual([]);
  });
});

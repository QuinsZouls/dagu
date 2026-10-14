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
  '! Please',
  '). To deactivate, remove the environment variable and restart Dagu.',
  ', and',
  '. Are you sure?',
  '. Discard changes?',
  '. Password reset is available for all admins.',
  '. Please',
  '1. Scope',
  '100 lines',
  '1000 lines',
  '10000 lines',
  '2. Send notifications',
  '3. Effective rules',
  '500 lines',
  '5000 lines',
  '? This action cannot be undone.',
  '? This cannot be undone.',
  'Activate',
  'Activating...',
  'Activation failed',
  'Add',
  'Add "',
  'Add another route',
  'Add route',
  'Allow callers to select an approved profile with',
  'Approve this server in Dagu Console. Check that the console shows this code:',
  'Are you sure you want to',
  'Are you sure you want to delete',
  'Audit logs',
  'Available Tools (',
  'Change',
  'Check now',
  'Checking…',
  'Choose how requests authenticate to this webhook. If you enable HMAC, callers must send',
  'Choose what this server reports',
  'Choose what to report',
  'Commands (',
  'Community',
  'Community Edition',
  'Community installs can manage up to 2 API keys.',
  'Connect for free',
  'Connect to Dagu Console',
  'Connected to Dagu Console.',
  'Connected to {workspace}.',
  'Connected via Dagu Console',
  'Connections are configured once, then selected by Global, workspace, or DAG incident routing.',
  'Control what teammates can view and change.',
  'Copy server ID',
  'Could not cancel the request. Try again.',
  'Could not change what this server reports.',
  'Could not connect to Dagu Console',
  'Could not dismiss the notice.',
  'Could not start connecting to Dagu Console.',
  'Create API keys beyond the Community limit of two.',
  'Create and test notification channels before using them in routes.',
  'DAGU-XXXX-XXXX-XXXX-XXXX',
  'Dagu Console could not be reached. Try again later.',
  'Dagu Console shows this server\'s health on its Servers page. With run status, it also emails the workspace\'s owners and admins when a workflow fails.',
  'Dagu Console shows this server\'s runs, failures, and trends, and emails you when a workflow fails or the server stops reporting. You choose what the server reports once it connects.',
  'Dagu uses the most specific configured scope: workflow, then workspace, then Global.',
  'Danger zone',
  'Deactivate',
  'Deactivating...',
  'Deactivation failed',
  'Default for every workflow unless a workspace or workflow is configured.',
  'Default for every workflow unless workspace or workflow settings are configured.',
  'Delete {count} Run',
  'Delete {count} Runs',
  'Discard local changes to',
  'Disconnect',
  'Disconnect this server',
  'Disconnected here, but Dagu Console could not be reached. Disconnect this server in Dagu Console to free its slot.',
  'Disconnected. Running in community mode.',
  'Disconnecting frees this server’s slot in Dagu Console and returns it to community mode.',
  'Disconnecting...',
  'Do you want to',
  'Do you want to dequeue',
  'Each channel can have one route per scope. Edit its events above, or add another channel.',
  'Email Delivery',
  'Enter custom DAG-Run ID',
  'Existing keys remain active. Revoke a key to create another, or move to Team or Pro to remove the limit.',
  'Existing open incidents still resolve when the DAG recovers.',
  'Expired',
  'Expires',
  'Expires in {count} day',
  'Expires in {count} days',
  'Expires in {time}',
  'Expires today',
  'Features',
  'Free',
  'Global rules apply by default. Workspace and workflow settings override them only when configured.',
  'Grace period',
  'Health and run status',
  'Health and run status (recommended)',
  'Health only',
  'High',
  'Incident routing',
  'Included',
  'Included in Team and Pro',
  'Individual secrets that DAGs reference with',
  'Inherit Global',
  'Interactive shell connection to local server as',
  'Last check-in',
  'Last report',
  'Latest status',
  'Loaded',
  'Low',
  'Manage in Dagu Console',
  'Manage plan & features',
  'Managed by',
  'Medium',
  'Modified',
  'Monitor this server for free',
  'Monitor this server from Dagu Console',
  'More API keys',
  'Multiple executions:',
  'Need a new Slack, email, webhook, or Telegram destination?',
  'Needs attention',
  'Never sent: logs, outputs, parameters, environment, step commands, DAG YAML, the values of workflow secrets, and error message text.',
  'No DAG-runs in the selected time range produced artifact files. Adjust the date range or filters.',
  'No enabled route currently sends notifications.',
  'No runs on',
  'Not connected',
  'Not included',
  'Not now',
  'Not reporting',
  'Not reporting since {time}',
  'Not yet',
  'Open Dagu Console',
  'Open and resolve incidents when workflows fail.',
  'Open feature',
  'Override Global with workspace-specific rules.',
  'Overrides Global for workflows in this workspace.',
  'Overrides Global for {workspace}.',
  'Page {page} of {total}',
  'Partial Success',
  'Password reset remains available to administrators.',
  'Plan',
  'Plan & features',
  'Producing step is not a dependency',
  'Remove',
  'Remove this DAG override and inherit workspace or Global notification rules?',
  'Reports',
  'Reschedule {count} Run',
  'Reschedule {count} Runs',
  'Reset Password for',
  'Resolved during a run (',
  'Retry {count} Run',
  'Retry {count} Runs',
  'Role and workspace access are',
  'Role-based access',
  'Routes are disabled for this scope.',
  'Run history paused until {time}',
  'Run one of the example workflows from the ',
  'Running Tasks (',
  'Searching',
  'Select a workspace to configure this.',
  'Sends the Dagu version, OS, architecture, process start time, and service counts: schedulers holding the scheduler lock, and registered coordinators.',
  'Sends the health fields, plus the status of each top-level DAG run that finishes, with its failed step names (up to 50) and how it was triggered, and the list of runs queued, running, or waiting. Each run is sent as its DAG name, run ID, attempt ID, status, trigger, and queued, started, and finished times. No parameters, logs, or outputs.',
  'Server',
  'Server ID',
  'Set by an environment variable',
  'Set in configuration',
  'Setup guide',
  'Showing',
  'Sibling Properties (',
  'Sign in through your organization’s identity provider.',
  'Sign the exact input shown in the HMAC examples with your secret and send the hex digest in',
  'Single sign-on',
  'Start a workflow from the ',
  'Start free trial',
  'Step details',
  'Supports tokens such as',
  'The Free plan supports up to 2 API keys.',
  'The last report this server sent to Dagu Console. The heartbeat secret is hidden.',
  'This DAG inherits',
  'This DAG uses',
  'This will re-execute',
  'This will remove',
  'This workspace',
  'This workspace currently inherits Global rules.',
  'This workspace currently inherits Global rules. Workspace routes are ignored until Configure Workspace is selected.',
  'Trace who changed or ran workflows.',
  'Trial',
  'Turn off',
  'Turn on',
  'Turning off…',
  'Untracked',
  'Update available: v',
  'Update “',
  'Use operational events',
  'Use the Global rules for this workspace.',
  'Use your webhook HMAC secret as',
  'User management features (create, edit, delete) require a',
  'Uses Global until configured.',
  'View last report',
  'View plans',
  'Waiting for approval',
  'What to report',
  'When any selected event happens',
  'Wiki pages under',
  'Workspace access is',
  'You have unsaved changes in',
  'Your Dagu',
  'Your browser blocked the new tab. Open Dagu Console to continue.',
  'at sign-in.',
  'channel',
  'cloud.report in the server configuration sets this. Change it there and restart Dagu.',
  'community',
  'computed from the exact signature input shown below. Requests with',
  'contact your administrator',
  'entries',
  'env var.',
  'event',
  'expires in {count} day',
  'expires in {count} days',
  'expires today',
  'from',
  'from sync tracking? Files remain in the remote repository.',
  'from sync tracking? This does not delete the file from the remote repository.',
  'from the remote repository and sync state. This action cannot be undone.',
  'from the remote repository, local disk, and sync state. This action cannot be undone.',
  'from {scope}',
  'has expired. Features will be disabled on',
  'in the DAG YAML. It can also be inherited from',
  'is available as',
  'item',
  'items? This cannot be undone.',
  'lines',
  'managed by',
  'missing item',
  'more',
  'more step',
  'of',
  'or',
  'or containing a',
  'page to activate one.',
  'renew',
  'renew now',
  'renew to avoid disruption',
  'routing unless you set a DAG override.',
  'selected DAG run',
  'send to',
  'send to {channel}',
  'sign',
  'soon',
  'sync item',
  'tasks settled',
  'this webhook?',
  'to a new path.',
  'to avoid disruption',
  'to expose selected request headers as',
  'to see activity here.',
  'to update',
  'trial',
  'updated by',
  'upgrade',
  'upgrade now',
  'upgrade to avoid disruption',
  'wikilink appear here.',
  '{level}, last report {time}',
  '{plan} plan',
  '· Run',
  '” will be removed for everyone with access to this workspace scope. Workflows are not affected.',
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

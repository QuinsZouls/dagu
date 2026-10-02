// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Route-level pin for the community feature behavior of the license hooks in
// App.tsx (finding M7): menu.test.tsx and home/index.test.tsx mock
// useHasFeature, so they cannot detect a change in ROUTE gating. These tests
// render the real App/router with the real useLicense.ts.
//
// Two gates are pinned here:
//  - LicensedRoute (useHasFeature): community mode unlocks a feature ONLY
//    when the operator opted it in via the features list.
//  - ActiveLicenseDeveloperElement (useLicense directly): community mode must
//    NEVER unlock it, no matter which features are listed.
import { render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Config, LicenseStatus } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import App from '../App';

const { clientMock, clientGetMock, useQueryMock } = vi.hoisted(() => {
  const clientGetMock = vi.fn();
  return {
    clientGetMock,
    useQueryMock: vi.fn(),
    clientMock: {
      GET: clientGetMock,
      POST: vi.fn(),
      DELETE: vi.fn(),
    },
  };
});

vi.hoisted(() => {
  vi.stubGlobal('getConfig', () => ({
    apiURL: '/api/v1',
    basePath: '/',
    version: 'test',
  }));
});

vi.mock('@/hooks/api', () => ({
  useClient: () => clientMock,
  useQuery: useQueryMock,
}));

vi.mock('../layouts/Layout', () => ({
  default: ({ children }: { children: React.ReactNode }) => (
    <main>{children}</main>
  ),
}));

// Page stubs (same harness as App.test.tsx): only the ROUTE-LEVEL gating of
// LicensedRoute / ActiveLicenseDeveloperElement is under test here, the page
// internals themselves are covered by their own test suites.
vi.mock('../pages/administration', () => ({
  default: () => <h1>Administration</h1>,
}));
vi.mock('../pages/api-keys', () => ({ default: () => <h1>API Keys</h1> }));
vi.mock('../pages/api-docs', () => ({ default: () => <h1>API Docs</h1> }));
vi.mock('../pages/audit-logs', () => ({
  default: () => <h1>Audit Logs</h1>,
}));
vi.mock('../pages/base-config', () => ({
  default: () => <h1>Base Config</h1>,
}));
vi.mock('../pages/dag-runs', () => ({ default: () => <h1>DAG Runs</h1> }));
vi.mock('../pages/dag-runs/dag-run', () => ({
  default: () => <h1>DAG Run Details</h1>,
}));
vi.mock('../pages/dags', () => ({ default: () => <h1>DAGs</h1> }));
vi.mock('../pages/dags/dag', () => ({ default: () => <h1>DAG Details</h1> }));
vi.mock('../pages/wiki', () => ({ default: () => <h1>Wiki</h1> }));
vi.mock('../pages/event-logs', () => ({
  default: () => <h1>Event Logs</h1>,
}));
vi.mock('../pages/git-sync', () => ({ default: () => <h1>Git Sync</h1> }));
vi.mock('../pages/home', () => ({ default: () => <h1>Home</h1> }));
vi.mock('../pages/incident-policies', () => ({
  default: () => <h1>Incident Routing</h1>,
}));
vi.mock('../pages/incident-providers', () => ({
  default: () => <h1>Incident Connections</h1>,
}));
vi.mock('../pages/incidents', () => ({ default: () => <h1>Incidents</h1> }));
vi.mock('../pages/integrations', () => ({
  default: () => <h1>Integrations</h1>,
}));
vi.mock('../pages/license', () => ({ default: () => <h1>License</h1> }));
vi.mock('../pages/login', () => ({ default: () => <h1>Login</h1> }));
vi.mock('../pages/notification-channels', () => ({
  default: () => <h1>Notification Channels</h1>,
}));
vi.mock('../pages/notification-rules', () => ({
  default: () => <h1>Notification Rules</h1>,
}));
vi.mock('../pages/notifications', () => ({
  default: () => <h1>Notifications</h1>,
}));
vi.mock('../pages/overview', () => ({ default: () => <h1>Overview</h1> }));
vi.mock('../pages/views', () => ({ default: () => <h1>View</h1> }));
vi.mock('../pages/profiles', () => ({
  default: () => <h1>Profiles &amp; Secrets</h1>,
}));
vi.mock('../pages/queues', () => ({ default: () => <h1>Queues</h1> }));
vi.mock('../pages/queues/queue', () => ({
  default: () => <h1>Queue Details</h1>,
}));
vi.mock('../pages/search', () => ({ default: () => <h1>Search</h1> }));
vi.mock('../pages/setup', () => ({ default: () => <h1>Setup</h1> }));
vi.mock('../pages/artifacts', () => ({ default: () => <h1>Artifacts</h1> }));
vi.mock('../pages/system-status', () => ({
  default: () => <h1>System Status</h1>,
}));
vi.mock('../pages/terminal', () => ({ default: () => <h1>Terminal</h1> }));
vi.mock('../pages/remote-nodes', () => ({
  default: () => <h1>Remote Nodes</h1>,
}));
vi.mock('../pages/users', () => ({ default: () => <h1>Users</h1> }));
vi.mock('../pages/webhooks', () => ({ default: () => <h1>Webhooks</h1> }));

function makeConfig(overrides: Partial<Config> = {}): Config {
  return {
    apiURL: '/api/v1',
    basePath: '/',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: 'local',
    initialWorkspaces: [],
    authMode: 'none',
    setupRequired: false,
    oidcEnabled: false,
    oidcButtonLabel: '',
    proxyEnabled: false,
    proxyButtonLabel: '',
    terminalEnabled: true,
    gitSyncEnabled: true,
    updateAvailable: false,
    latestVersion: '',
    permissions: {
      writeDags: true,
      runDags: true,
    },
    license: {
      valid: false,
      plan: 'community',
      expiry: '',
      features: [],
      gracePeriod: false,
      community: true,
      source: 'test',
      warningCode: '',
    },
    paths: {
      dagsDir: '',
      logDir: '',
      suspendFlagsDir: '',
      adminLogsDir: '',
      baseConfig: '',
      dagRunsDir: '',
      queueDir: '',
      procDir: '',
      serviceRegistryDir: '',
      configFileUsed: '',
      gitSyncDir: '',
      auditLogsDir: '',
      wikiDir: '',
      docsDir: '',
    },
    ...overrides,
  };
}

// Community mode (no Pro license): the server only lists a feature here when
// the operator opted in via DAGU_LICENSE_COMMUNITY_FEATURES, so a non-empty
// features array is the opt-in signal (same convention as the users page tests).
function makeCommunityLicense(features: string[]): LicenseStatus {
  return {
    valid: false,
    plan: '',
    expiry: '',
    features,
    gracePeriod: false,
    community: true,
    source: 'test',
    warningCode: '',
  };
}

function renderAt(path: string, config = makeConfig()) {
  window.history.pushState({}, '', path);
  return render(<App config={config} />);
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  clientGetMock.mockReset();
  clientGetMock.mockResolvedValue({ data: { workspaces: [] } });
  useQueryMock.mockReset();
  useQueryMock.mockReturnValue({ data: undefined });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        remoteNodes: [],
        type: 'object',
        properties: {},
      })
    )
  );
});

describe('LicensedRoute community feature gating', () => {
  it('unlocks /audit-logs in community mode when the operator opted in to audit', async () => {
    renderAt(
      '/audit-logs',
      makeConfig({ license: makeCommunityLicense(['audit']) })
    );

    expect(
      await screen.findByRole('heading', { name: 'Audit Logs' })
    ).toBeVisible();
    expect(
      screen.queryByRole('heading', { name: 'License Required' })
    ).not.toBeInTheDocument();
  });

  it('keeps non-opted and active-license routes locked in community mode', async () => {
    // (A) ActiveLicenseDeveloperElement routes (the incidents trio in App.tsx;
    // /api-keys itself is AdminElement-only and has no license fallback) must
    // stay locked in community mode regardless of the community feature list.
    const first = renderAt(
      '/incidents',
      makeConfig({ license: makeCommunityLicense(['rbac', 'audit', 'sso']) })
    );

    expect(
      await screen.findByRole('heading', { name: 'License Required' })
    ).toBeVisible();
    expect(
      screen.queryByRole('heading', { name: 'Incidents' })
    ).not.toBeInTheDocument();
    first.unmount();

    // (B) LicensedRoute still enforces the features check: other community
    // features (rbac, sso) must NOT over-unlock the audit route.
    renderAt(
      '/audit-logs',
      makeConfig({ license: makeCommunityLicense(['rbac', 'sso']) })
    );

    expect(
      await screen.findByRole('heading', { name: 'License Required' })
    ).toBeVisible();
    expect(
      screen.queryByRole('heading', { name: 'Audit Logs' })
    ).not.toBeInTheDocument();
  });
});

// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import AdministrationPage from '..';

const config = {
  authMode: 'builtin',
  terminalEnabled: true,
} as Config;

function renderPage(configOverride: Partial<Config> = {}) {
  const setTitle = vi.fn();

  render(
    <MemoryRouter>
      <ConfigContext.Provider value={{ ...config, ...configOverride }}>
        <AppBarContext.Provider value={{ setTitle } as never}>
          <AdministrationPage />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );

  return { setTitle };
}

describe('AdministrationPage', () => {
  it('renders administration links by section', () => {
    const { setTitle } = renderPage();

    expect(
      screen.getByRole('heading', { name: /administration/i })
    ).toBeVisible();

    // Section headings.
    expect(screen.getByRole('heading', { name: 'Access' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Security' })).toBeVisible();
    expect(
      screen.getByRole('heading', { name: 'Infrastructure' })
    ).toBeVisible();

    // Access section.
    expect(screen.getByRole('link', { name: /users/i })).toHaveAttribute(
      'href',
      '/users'
    );
    expect(screen.getByRole('link', { name: /api keys/i })).toHaveAttribute(
      'href',
      '/api-keys'
    );
    expect(screen.getByText('Manage accounts and roles.')).toBeVisible();
    expect(
      screen.getByText('Issue access tokens for automation.')
    ).toBeVisible();

    // Security section.
    expect(
      screen.getByRole('link', { name: /profiles/i })
    ).toHaveAttribute('href', '/profiles');
    expect(
      screen.getByText('Manage environment profiles and DAG secret refs.')
    ).toBeVisible();

    // Infrastructure section.
    expect(screen.getByRole('link', { name: /remote nodes/i })).toHaveAttribute(
      'href',
      '/remote-nodes'
    );
    expect(
      screen.getByText('Configure distributed execution targets.')
    ).toBeVisible();
    expect(screen.getByRole('link', { name: /terminal/i })).toHaveAttribute(
      'href',
      '/terminal'
    );
    expect(screen.getByText('Open a server-side shell.')).toBeVisible();

    // The license/entitlement card was removed by the license-free refactor
    // (ruling R1: no license surface may remain) — assert it stays gone.
    expect(
      screen.queryByText(/license|entitlement|review plan/i)
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: /license|entitlement|plan/i })
    ).not.toBeInTheDocument();

    expect(setTitle).toHaveBeenCalledWith('Administration');
  });
});

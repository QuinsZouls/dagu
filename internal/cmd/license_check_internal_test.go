// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"io"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommunityLicenseStatusLine covers the exact line `dagu license check`
// prints while running without a license: the upstream wording is kept when
// no community feature is opted in, and the enabled features are listed
// otherwise.
func TestCommunityLicenseStatusLine(t *testing.T) {
	tests := []struct {
		name     string
		features []string
		want     string
	}{
		{
			name:     "NoFeaturesKeepsUpstreamLine",
			features: nil,
			want:     "License: Community mode (no license)",
		},
		{
			name:     "EmptyListKeepsUpstreamLine",
			features: []string{},
			want:     "License: Community mode (no license)",
		},
		{
			name:     "SingleFeature",
			features: []string{"rbac"},
			want:     "License: Community mode (no license; community features: rbac)",
		},
		{
			name:     "MultipleFeaturesInConfigOrder",
			features: []string{"rbac", "sso"},
			want:     "License: Community mode (no license; community features: rbac, sso)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, communityLicenseStatusLine(tc.features))
		})
	}
}

// captureStdout collects everything fn writes to the process stdout and
// returns it as a string. `dagu license check` prints with fmt.Println (not
// through the cobra writer), so this is the only way to observe its output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(out)
}

// TestLicenseCheckCommand_CommunityFeatures executes the real
// `dagu license check` command against a config that opts into community
// features and asserts the printed status line. It proves the whole wiring:
// config → license.ManagerConfig.CommunityFeatures → StatusFor → output.
func TestLicenseCheckCommand_CommunityFeatures(t *testing.T) {
	// Never let a pre-activated license in the ambient environment turn this
	// into a licensed-mode run.
	t.Setenv("DAGU_LICENSE", "")
	t.Setenv("DAGU_LICENSE_KEY", "")

	th := test.SetupCommand(t)

	cfgFile := th.Config.Paths.ConfigFileUsed
	require.NotEmpty(t, cfgFile)

	// The helper config file has no license section; add the opt-in to the
	// file the command will load.
	current, err := os.ReadFile(cfgFile)
	require.NoError(t, err)
	licenseSection := "\nlicense:\n  community_features:\n    - rbac\n    - sso\n"
	require.NoError(t, os.WriteFile(cfgFile, append(current, licenseSection...), 0o600))

	root := &cobra.Command{Use: "root"}
	root.AddCommand(License())
	root.SetArgs([]string{"license", "check", "--config", cfgFile})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	out := captureStdout(t, func() {
		require.NoError(t, root.ExecuteContext(th.Context))
	})

	assert.Contains(t, out,
		"License: Community mode (no license; community features: rbac, sso)")
}

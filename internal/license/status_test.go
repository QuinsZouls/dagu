// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusFor(t *testing.T) {
	t.Parallel()

	t.Run("nil checker reports community status", func(t *testing.T) {
		t.Parallel()

		status := StatusFor(nil)

		assert.True(t, status.Community)
		assert.False(t, status.Valid)
		assert.Empty(t, status.Features)
	})

	t.Run("active license includes its public status", func(t *testing.T) {
		t.Parallel()

		manager := NewTestManager(FeatureAudit, FeatureRBAC)
		status := StatusFor(manager.Checker())

		assert.False(t, status.Community)
		assert.True(t, status.Valid)
		assert.Equal(t, "pro", status.Plan)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC}, status.Features)
		assert.WithinDuration(t, status.Expiry.Add(gracePeriod), status.GraceEndsAt, time.Second)
	})

	t.Run("community state with configured features projects them", func(t *testing.T) {
		t.Parallel()

		s := newState([]string{FeatureRBAC, FeatureSSO})
		status := StatusFor(s)

		assert.True(t, status.Community)
		assert.False(t, status.Valid)
		assert.Equal(t, "", status.Plan)
		assert.Equal(t, []string{FeatureRBAC, FeatureSSO}, status.Features)
	})

	t.Run("projected community features are a defensive copy", func(t *testing.T) {
		t.Parallel()

		s := newState([]string{FeatureRBAC})
		status := StatusFor(s)
		require.NotEmpty(t, status.Features)
		status.Features[0] = "mutated"
		assert.Equal(t, []string{FeatureRBAC}, s.CommunityFeatures())
	})

	t.Run("community features ignored while claims are loaded", func(t *testing.T) {
		t.Parallel()

		s := newState([]string{FeatureRBAC})
		s.Update(validClaims(), "tok")
		status := StatusFor(s)

		assert.False(t, status.Community)
		assert.True(t, status.Valid)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC, FeatureSSO}, status.Features)
	})

	t.Run("checker without the optional interface reports no features", func(t *testing.T) {
		t.Parallel()

		status := StatusFor(bareChecker{})

		assert.True(t, status.Community)
		assert.False(t, status.Valid)
		assert.Equal(t, []string{}, status.Features)
	})
}

// bareChecker is a minimal Checker implementation that does NOT expose
// CommunityFeatures, used to verify StatusFor's optional interface assertion
// keeps working with existing fakes.
type bareChecker struct{}

var _ Checker = bareChecker{}

func (bareChecker) IsFeatureEnabled(string) bool { return false }
func (bareChecker) Plan() string                 { return "" }
func (bareChecker) IsGracePeriod() bool          { return false }
func (bareChecker) IsCommunity() bool            { return true }
func (bareChecker) Claims() *LicenseClaims       { return nil }
func (bareChecker) WarningCode() string          { return "" }

func TestManagerStatusIncludesFailureAndSource(t *testing.T) {
	t.Parallel()

	manager := NewExpiredTestManager(FeatureAudit)
	manager.setSource(SourceFileJWT)

	status := manager.Status()

	require.False(t, status.Community)
	assert.False(t, status.Valid)
	assert.False(t, status.GracePeriod)
	assert.Equal(t, SourceFileJWT, status.Source)
	assert.Equal(t, licenseExpiredFailure, status.Failure)
}

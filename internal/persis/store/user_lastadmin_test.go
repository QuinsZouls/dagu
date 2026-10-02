// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
)

// These tests pin the last-active-admin invariant that the store enforces
// ATOMICALLY with the write (inside s.mu): the check used to live in the
// HTTP handler as a check-then-act pre-check (TOCTOU).

// TestUserPatchEnsuringActiveAdmin_DemotingSoleAdminFails proves a role
// demotion of the sole active admin is refused AND leaves the record
// untouched on re-read.
func TestUserPatchEnsuringActiveAdmin_DemotingSoleAdminFails(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	sole := newUser("sole-admin")
	require.NoError(t, s.Create(ctx, sole))

	viewer := auth.RoleViewer
	_, err := s.PatchEnsuringActiveAdmin(ctx, sole.ID, auth.UserPatch{Role: &viewer})
	assert.ErrorIs(t, err, auth.ErrLastActiveAdmin)

	// Refusal left the target unchanged.
	got, err := s.GetByID(ctx, sole.ID)
	require.NoError(t, err)
	assert.Equal(t, auth.RoleAdmin, got.Role)
	assert.False(t, got.IsDisabled)
}

// TestUserPatchEnsuringActiveAdmin_DisablingSoleAdminFails: disabling is
// also "losing admin status" for the invariant (active = admin && !disabled).
func TestUserPatchEnsuringActiveAdmin_DisablingSoleAdminFails(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	sole := newUser("sole-disable")
	require.NoError(t, s.Create(ctx, sole))

	disabled := true
	_, err := s.PatchEnsuringActiveAdmin(ctx, sole.ID, auth.UserPatch{IsDisabled: &disabled})
	assert.ErrorIs(t, err, auth.ErrLastActiveAdmin)

	got, err := s.GetByID(ctx, sole.ID)
	require.NoError(t, err)
	assert.False(t, got.IsDisabled)
}

// TestUserDeleteEnsuringActiveAdmin_SoleActiveAdminFails: the delete is
// refused AND the user is still listed afterwards.
func TestUserDeleteEnsuringActiveAdmin_SoleActiveAdminFails(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	sole := newUser("sole-delete")
	require.NoError(t, s.Create(ctx, sole))

	err := s.DeleteEnsuringActiveAdmin(ctx, sole.ID)
	assert.ErrorIs(t, err, auth.ErrLastActiveAdmin)

	got, err := s.GetByID(ctx, sole.ID)
	require.NoError(t, err, "refused delete must keep the user")
	assert.Equal(t, sole.ID, got.ID)

	users, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "sole-delete", users[0].Username)
}

// TestUserPatchEnsuringActiveAdmin_UsernameOnlyPatchIsAllowed: a patch that
// does not touch Role/IsDisabled never takes admin status away, so the sole
// admin can still be renamed (fast path, no scan).
func TestUserPatchEnsuringActiveAdmin_UsernameOnlyPatchIsAllowed(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	sole := newUser("sole-rename")
	require.NoError(t, s.Create(ctx, sole))

	newName := "sole-renamed"
	updated, err := s.PatchEnsuringActiveAdmin(ctx, sole.ID, auth.UserPatch{Username: &newName})
	require.NoError(t, err)
	assert.Equal(t, "sole-renamed", updated.Username)
	assert.Equal(t, auth.RoleAdmin, updated.Role)

	// The rename is visible and the old username is released.
	got, err := s.GetByUsername(ctx, "sole-renamed")
	require.NoError(t, err)
	assert.Equal(t, sole.ID, got.ID)
}

// TestUserLastActiveAdminGuard_NonAdminTargetsSucceed: while exactly one
// active admin exists, mutating or deleting a NON-admin target must not
// report ErrLastActiveAdmin.
func TestUserLastActiveAdminGuard_NonAdminTargetsSucceed(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	admin := newUser("keeper-admin")
	require.NoError(t, s.Create(ctx, admin))

	dev := newUser("plain-dev")
	dev.Role = auth.RoleDeveloper
	require.NoError(t, s.Create(ctx, dev))

	// Patch (demotion) of the non-admin target succeeds.
	viewer := auth.RoleViewer
	updated, err := s.PatchEnsuringActiveAdmin(ctx, dev.ID, auth.UserPatch{Role: &viewer})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, updated.Role)

	// Disable of the non-admin target succeeds.
	disabled := true
	_, err = s.PatchEnsuringActiveAdmin(ctx, dev.ID, auth.UserPatch{IsDisabled: &disabled})
	require.NoError(t, err)

	// Delete of the non-admin target succeeds.
	require.NoError(t, s.DeleteEnsuringActiveAdmin(ctx, dev.ID))

	// The remaining active admin is intact.
	got, err := s.GetByID(ctx, admin.ID)
	require.NoError(t, err)
	assert.Equal(t, auth.RoleAdmin, got.Role)
	assert.False(t, got.IsDisabled)
}

// TestUserLastActiveAdminGuard_SecondActiveAdminUnblocksMutation: with a
// second active admin present, both the demotion and the delete of the
// first one go through.
func TestUserLastActiveAdminGuard_SecondActiveAdminUnblocksMutation(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	a := newUser("admin-a")
	require.NoError(t, s.Create(ctx, a))
	b := newUser("admin-b")
	require.NoError(t, s.Create(ctx, b))

	viewer := auth.RoleViewer
	updated, err := s.PatchEnsuringActiveAdmin(ctx, a.ID, auth.UserPatch{Role: &viewer})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, updated.Role)

	// B is now the sole active admin: deleting B is refused, deleting the
	// already-demoted A is fine.
	require.ErrorIs(t, s.DeleteEnsuringActiveAdmin(ctx, b.ID), auth.ErrLastActiveAdmin)
	require.NoError(t, s.DeleteEnsuringActiveAdmin(ctx, a.ID))
}

// TestUserLastActiveAdminGuard_ZeroAdminStoreLeavesNonAdminsAlone: the
// invariant only constrains mutations against a CURRENTLY active admin.
// A store with no active admin at all (degenerate state) must still allow
// role changes on non-admin targets — exactly what the former
// handler-level pre-check allowed.
func TestUserLastActiveAdminGuard_ZeroAdminStoreLeavesNonAdminsAlone(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	dev := newUser("no-admin-dev")
	dev.Role = auth.RoleDeveloper
	require.NoError(t, s.Create(ctx, dev))

	viewer := auth.RoleViewer
	updated, err := s.PatchEnsuringActiveAdmin(ctx, dev.ID, auth.UserPatch{Role: &viewer})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, updated.Role)

	disabled := true
	_, err = s.PatchEnsuringActiveAdmin(ctx, dev.ID, auth.UserPatch{IsDisabled: &disabled})
	require.NoError(t, err)

	require.NoError(t, s.DeleteEnsuringActiveAdmin(ctx, dev.ID))
}

// TestUserPatchWithoutGuardStillDemotesSoleAdmin: the exported plain
// Patch/Delete keep their original (unconditional) behavior — ZERO change
// for existing callers; only the Ensuring* variants enforce the invariant.
func TestUserPatchWithoutGuardStillDemotesSoleAdmin(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	sole := newUser("unguarded")
	require.NoError(t, s.Create(ctx, sole))

	viewer := auth.RoleViewer
	updated, err := s.Patch(ctx, sole.ID, auth.UserPatch{Role: &viewer})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, updated.Role)

	require.NoError(t, s.Delete(ctx, sole.ID))
	_, err = s.GetByID(ctx, sole.ID)
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
}

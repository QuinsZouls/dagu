// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// communityFeaturesServer creates a builtin-auth test server whose license
// manager runs in community mode with the given community features configured
// (license.ManagerConfig.CommunityFeatures). The harness never calls
// Manager.Start, so no license is ever discovered and the manager state stays
// claims == nil, exactly like a real deployment without a license. Passing no
// features reproduces the upstream default where every licensed feature is
// disabled without a license.
func communityFeaturesServer(t *testing.T, features ...string) test.Server {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	manager := license.NewManager(license.ManagerConfig{
		CommunityFeatures: features,
	}, pub, nil, nil)

	return test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "community-multi-user-jwt-secret"
			cfg.Server.Auth.Builtin.Token.TTL = time.Hour
		}),
		test.WithServerOptions(frontend.WithLicenseManager(manager)),
	)
}

// communitySetupAdmin runs the first-run setup endpoint and returns the admin
// JWT token.
func communitySetupAdmin(t *testing.T, server test.Server) string {
	t.Helper()
	resp := server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var result api.LoginResponse
	resp.Unmarshal(t, &result)
	require.NotEmpty(t, result.Token)
	return result.Token
}

// TestCommunityMultiUser_RBACEnabled proves the end-to-end community
// multi-user flow when rbac is opted in via license.community_features: the
// admin created by first-run setup can create, inspect, modify, disable and
// delete users — the exact calls that returned 403 "User management requires
// a Dagu Pro license" before the community features landed.
func TestCommunityMultiUser_RBACEnabled(t *testing.T) {
	t.Parallel()

	server := communityFeaturesServer(t, license.FeatureRBAC)
	adminToken := communitySetupAdmin(t, server)

	// a+b. Create alice (developer): this was the 403-before case — the core
	// assertion of this test.
	createResp := server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "alice",
		Password: "alicepass123",
		Role:     api.UserRoleDeveloper,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	var created api.UserResponse
	createResp.Unmarshal(t, &created)
	require.Equal(t, "alice", created.User.Username)
	require.Equal(t, api.UserRoleDeveloper, created.User.Role)
	require.NotEmpty(t, created.User.Id)

	// c. List users: exactly admin + alice, and no password material in the
	// raw JSON body.
	usersResp := server.Client().Get("/api/v1/users").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)

	var list api.UsersListResponse
	usersResp.Unmarshal(t, &list)
	require.Len(t, list.Users, 2)
	require.ElementsMatch(t, []string{"admin", "alice"},
		[]string{list.Users[0].Username, list.Users[1].Username})
	require.NotContains(t, strings.ToLower(usersResp.Body), "password")

	// d. alice logs in with the password from her creation and /auth/me
	// reflects her identity and role.
	aliceToken := loginAndGetToken(t, server, "alice", "alicepass123")
	meResp := server.Client().Get("/api/v1/auth/me").
		WithBearerToken(aliceToken).
		ExpectStatus(http.StatusOK).Send(t)

	var me api.UserResponse
	meResp.Unmarshal(t, &me)
	require.Equal(t, "alice", me.User.Username)
	require.Equal(t, api.UserRoleDeveloper, me.User.Role)

	// e. Demote to viewer: PATCH succeeds and a fresh login sees the new role.
	viewerRole := api.UserRoleViewer
	server.Client().Patch("/api/v1/users/"+created.User.Id, api.UpdateUserRequest{
		Role: &viewerRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	aliceToken = loginAndGetToken(t, server, "alice", "alicepass123")
	meResp = server.Client().Get("/api/v1/auth/me").
		WithBearerToken(aliceToken).
		ExpectStatus(http.StatusOK).Send(t)
	meResp.Unmarshal(t, &me)
	require.Equal(t, api.UserRoleViewer, me.User.Role)

	// f. Disable alice: this step only asserts that a FRESH login is
	// refused while the account is disabled. The GetUserFromToken claim
	// (an already-issued token stops authenticating) is pinned separately
	// by the "live-session invalidation is pinned" vector in
	// TestCommunityMultiUser_LastAdminGuards (/auth/me with an old token).
	isDisabled := true
	server.Client().Patch("/api/v1/users/"+created.User.Id, api.UpdateUserRequest{
		IsDisabled: &isDisabled,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	disabledResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "alice",
		Password: "alicepass123",
	}).ExpectStatus(http.StatusUnauthorized).Send(t)

	var disabledErr api.Error
	disabledResp.Unmarshal(t, &disabledErr)
	require.Equal(t, api.ErrorCodeUnauthorized, disabledErr.Code)
	require.Contains(t, disabledErr.Message, "disabled")

	// g. Re-enable: PATCH succeeds and login works again.
	isDisabled = false
	server.Client().Patch("/api/v1/users/"+created.User.Id, api.UpdateUserRequest{
		IsDisabled: &isDisabled,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	loginAndGetToken(t, server, "alice", "alicepass123")

	// h. Delete alice: 204 and the list shrinks back to the admin only.
	server.Client().Delete("/api/v1/users/" + created.User.Id).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusNoContent).Send(t)

	usersResp = server.Client().Get("/api/v1/users").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)
	usersResp.Unmarshal(t, &list)
	require.Len(t, list.Users, 1)
	require.Equal(t, "admin", list.Users[0].Username)

	// i. License status stays honest: community mode with rbac projected from
	// the community features, no license validity claimed.
	statusResp := server.Client().Get("/api/v1/license/status").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)

	var status api.LicenseStatusResponse
	statusResp.Unmarshal(t, &status)
	require.True(t, status.Community)
	require.False(t, status.Valid)
	require.Contains(t, status.Features, license.FeatureRBAC)
}

// TestCommunityMultiUser_NoFeatures proves the upstream default is preserved
// byte-for-byte when a license manager is configured without any community
// features: user creation is refused with the same 403 envelope as before the
// community features landed.
func TestCommunityMultiUser_NoFeatures(t *testing.T) {
	t.Parallel()

	server := communityFeaturesServer(t) // no CommunityFeatures ⇒ upstream default
	adminToken := communitySetupAdmin(t, server)

	resp := server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "alice",
		Password: "alicepass123",
		Role:     api.UserRoleDeveloper,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusForbidden).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	require.Equal(t, api.ErrorCodeForbidden, errResp.Code)
	require.Equal(t, "User management requires a Dagu Pro license", errResp.Message)

	// The manager reports honest community status with no projected features.
	statusResp := server.Client().Get("/api/v1/license/status").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)

	var status api.LicenseStatusResponse
	statusResp.Unmarshal(t, &status)
	require.True(t, status.Community)
	require.False(t, status.Valid)
	require.Empty(t, status.Features)
}

// TestCommunityMultiUser_SSOFeatureDoesNotEnableUserCRUD proves per-feature
// granularity: opting into sso does NOT unlock the rbac-gated user CRUD calls.
func TestCommunityMultiUser_SSOFeatureDoesNotEnableUserCRUD(t *testing.T) {
	t.Parallel()

	server := communityFeaturesServer(t, license.FeatureSSO)
	adminToken := communitySetupAdmin(t, server)

	// The sso feature really is enabled in community mode...
	statusResp := server.Client().Get("/api/v1/license/status").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)

	var status api.LicenseStatusResponse
	statusResp.Unmarshal(t, &status)
	require.True(t, status.Community)
	require.Contains(t, status.Features, license.FeatureSSO)

	// ...the non-rbac-gated list endpoint still works...
	usersResp := server.Client().Get("/api/v1/users").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)

	var list api.UsersListResponse
	usersResp.Unmarshal(t, &list)
	require.Len(t, list.Users, 1)
	require.Equal(t, "admin", list.Users[0].Username)

	// ...but creating a user requires an explicit rbac opt-in...
	createResp := server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "alice",
		Password: "alicepass123",
		Role:     api.UserRoleDeveloper,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusForbidden).Send(t)

	var errResp api.Error
	createResp.Unmarshal(t, &errResp)
	require.Equal(t, api.ErrorCodeForbidden, errResp.Code)
	require.Equal(t, "User management requires a Dagu Pro license", errResp.Message)

	// ...and so does every other rbac-gated mutation of an existing user.
	viewerRole := api.UserRoleViewer
	patchResp := server.Client().Patch("/api/v1/users/"+list.Users[0].Id, api.UpdateUserRequest{
		Role: &viewerRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusForbidden).Send(t)

	patchResp.Unmarshal(t, &errResp)
	require.Equal(t, api.ErrorCodeForbidden, errResp.Code)
	require.Equal(t, "User management requires a Dagu Pro license", errResp.Message)
}

// TestCommunityMultiUser_LastAdminGuards proves the single M3 invariant:
// after any user mutation at least one ACTIVE (not disabled) user with role
// admin must remain in the store. First-run setup is one-shot, so losing the
// last active admin permanently locks user management out of the UI/API.
//
// Covered vectors:
//   - sole admin demotes itself (self-demote was NOT blocked before M3)
//   - demote/disable/delete a second admin while the first one still exists
//   - self-disable keeps its pre-existing message (error precedence)
//   - self-delete reports the self-delete message, not the last-admin one
//   - developer targets are unaffected (they were never active admins)
//   - a service-account API key (synthetic admin principal that matches no
//     stored user) cannot demote/delete the last stored admin
//   - a token issued before a disable/delete stops authenticating (/auth/me
//     → 401) — live-session invalidation is pinned
//   - a compound {role: admin, isDisabled: true} patch on the sole active
//     admin is refused with the last-active-admin message
//   - a compound {role: viewer, username: <taken>} patch on the sole active
//     admin returns 409 (username collision is checked before the invariant)
func TestCommunityMultiUser_LastAdminGuards(t *testing.T) {
	t.Parallel()

	server := communityFeaturesServer(t, license.FeatureRBAC)
	adminToken := communitySetupAdmin(t, server)

	listUsers := func(token string) api.UsersListResponse {
		t.Helper()
		resp := server.Client().Get("/api/v1/users").
			WithBearerToken(token).
			ExpectStatus(http.StatusOK).Send(t)
		var list api.UsersListResponse
		resp.Unmarshal(t, &list)
		return list
	}

	expectLastActiveAdmin403 := func(resp *test.Response) {
		t.Helper()
		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		require.Equal(t, api.ErrorCodeForbidden, errResp.Code)
		require.Equal(t, "Cannot remove the last active admin", errResp.Message)
	}

	expectMeRole := func(token string, want api.UserRole) {
		t.Helper()
		resp := server.Client().Get("/api/v1/auth/me").
			WithBearerToken(token).
			ExpectStatus(http.StatusOK).Send(t)
		var me api.UserResponse
		resp.Unmarshal(t, &me)
		require.Equal(t, want, me.User.Role)
	}

	// --- 1. Sole admin demotes itself: must be refused. ------------------
	users := listUsers(adminToken)
	require.Len(t, users.Users, 1)
	require.Equal(t, "admin", users.Users[0].Username)
	soleAdminID := users.Users[0].Id

	viewerRole := api.UserRoleViewer
	resp := server.Client().Patch("/api/v1/users/"+soleAdminID, api.UpdateUserRequest{
		Role: &viewerRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusForbidden).Send(t)
	expectLastActiveAdmin403(resp)

	// The refusal left the account untouched: admin can still log in.
	expectMeRole(adminToken, api.UserRoleAdmin)

	// --- 2. Second admin exists but is demoted ⇒ self-demote still locked.
	createResp := server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "adminb",
		Password: "adminbpass1",
		Role:     api.UserRoleAdmin,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	var createdB api.UserResponse
	createResp.Unmarshal(t, &createdB)
	require.Equal(t, api.UserRoleAdmin, createdB.User.Role)
	adminBID := createdB.User.Id

	developerRole := api.UserRoleDeveloper
	server.Client().Patch("/api/v1/users/"+adminBID, api.UpdateUserRequest{
		Role: &developerRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// B is a developer now, so A is the only active admin again.
	resp = server.Client().Patch("/api/v1/users/"+soleAdminID, api.UpdateUserRequest{
		Role: &viewerRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusForbidden).Send(t)
	expectLastActiveAdmin403(resp)
	expectMeRole(adminToken, api.UserRoleAdmin)

	// --- 3. Promote B back, then disable/delete B: A remains admin. ------
	adminRole := api.UserRoleAdmin
	server.Client().Patch("/api/v1/users/"+adminBID, api.UpdateUserRequest{
		Role: &adminRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// Live-session invalidation: a token issued before the account is
	// disabled must die with the account.
	bSessionToken := loginAndGetToken(t, server, "adminb", "adminbpass1")

	isDisabled := true
	server.Client().Patch("/api/v1/users/"+adminBID, api.UpdateUserRequest{
		IsDisabled: &isDisabled,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// The old token is refused while the account is disabled...
	server.Client().Get("/api/v1/auth/me").
		WithBearerToken(bSessionToken).
		ExpectStatus(http.StatusUnauthorized).Send(t)

	server.Client().Delete("/api/v1/users/" + adminBID).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusNoContent).Send(t)

	// ...and stays refused after the account is gone entirely.
	server.Client().Get("/api/v1/auth/me").
		WithBearerToken(bSessionToken).
		ExpectStatus(http.StatusUnauthorized).Send(t)

	users = listUsers(adminToken)
	require.Len(t, users.Users, 1)
	require.Equal(t, "admin", users.Users[0].Username)

	// --- 4. Self-disable keeps the pre-existing message (precedence). ----
	selfDisableResp := server.Client().Patch("/api/v1/users/"+soleAdminID, api.UpdateUserRequest{
		IsDisabled: &isDisabled,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusForbidden).Send(t)
	var selfDisableErr api.Error
	selfDisableResp.Unmarshal(t, &selfDisableErr)
	require.Equal(t, api.ErrorCodeForbidden, selfDisableErr.Code)
	require.Equal(t, "Cannot disable your own account", selfDisableErr.Message)

	// Self-delete precedence: deleting your own id must report the
	// self-delete message, NOT the last-active-admin message, even though
	// the target is also the sole active admin.
	selfDeleteResp := server.Client().Delete("/api/v1/users/" + soleAdminID).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusForbidden).Send(t)
	var selfDeleteErr api.Error
	selfDeleteResp.Unmarshal(t, &selfDeleteErr)
	require.Equal(t, api.ErrorCodeForbidden, selfDeleteErr.Code)
	require.Equal(t, "Cannot delete your own account", selfDeleteErr.Message)

	// --- 5. Non-admin targets are unaffected. ----------------------------
	createResp = server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "devc",
		Password: "devcpass123",
		Role:     api.UserRoleDeveloper,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	var createdC api.UserResponse
	createResp.Unmarshal(t, &createdC)
	require.Equal(t, api.UserRoleDeveloper, createdC.User.Role)

	// role demotion of a never-admin user
	server.Client().Patch("/api/v1/users/"+createdC.User.Id, api.UpdateUserRequest{
		Role: &viewerRole,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// disable / re-enable of a never-admin user
	server.Client().Patch("/api/v1/users/"+createdC.User.Id, api.UpdateUserRequest{
		IsDisabled: &isDisabled,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	isDisabled = false
	server.Client().Patch("/api/v1/users/"+createdC.User.Id, api.UpdateUserRequest{
		IsDisabled: &isDisabled,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// --- 6. Service-account API key cannot demote the last stored admin. --
	keyResp := server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "svc-admin",
		Role: api.UserRoleAdmin,
		AllowedSurfaces: []api.CreateAPIKeyRequestAllowedSurfaces{
			api.CreateAPIKeyRequestAllowedSurfacesRestApi,
			api.CreateAPIKeyRequestAllowedSurfacesMcp,
		},
		AttributionClass: api.CreateAPIKeyRequestAttributionClassServiceAccount,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	var createdKey api.CreateAPIKeyResponse
	keyResp.Unmarshal(t, &createdKey)
	require.NotEmpty(t, createdKey.Key)
	require.True(t, strings.HasPrefix(createdKey.Key, "dagu_"),
		"API key must carry the dagu_ prefix, got %q", createdKey.Key)

	// The key authenticates as an admin principal whose ID matches no stored
	// user, so it must not be treated as "the remaining admin".
	resp = server.Client().Patch("/api/v1/users/"+soleAdminID, api.UpdateUserRequest{
		Role: &viewerRole,
	}).WithBearerToken(createdKey.Key).ExpectStatus(http.StatusForbidden).Send(t)
	expectLastActiveAdmin403(resp)

	// The same principal cannot DELETE the sole stored active admin either,
	// and the refused delete leaves the user in place.
	resp = server.Client().Delete("/api/v1/users/" + soleAdminID).
		WithBearerToken(createdKey.Key).
		ExpectStatus(http.StatusForbidden).Send(t)
	expectLastActiveAdmin403(resp)

	users = listUsers(adminToken)
	require.Len(t, users.Users, 2)
	foundSoleAdmin := false
	for _, u := range users.Users {
		if u.Id == soleAdminID {
			foundSoleAdmin = true
			require.Equal(t, api.UserRoleAdmin, u.Role)
		}
	}
	require.True(t, foundSoleAdmin, "sole admin must survive the refused delete")

	// A compound patch that both keeps the role AND disables still loses
	// active-admin status, so it must be refused as well.
	disableSelfAsAdmin := true
	resp = server.Client().Patch("/api/v1/users/"+soleAdminID, api.UpdateUserRequest{
		Role:       &adminRole,
		IsDisabled: &disableSelfAsAdmin,
	}).WithBearerToken(createdKey.Key).ExpectStatus(http.StatusForbidden).Send(t)
	expectLastActiveAdmin403(resp)

	// --- 7. Username collision wins over the last-admin invariant. -------
	// This pins the precedence introduced in ae867201: the username-collision
	// check in patchLocked runs BEFORE the last-active-admin invariant, so a
	// compound patch that both demotes the sole active admin and takes an
	// already-taken username returns 409 (validation) instead of 403
	// (invariant). Validation-before-invariant ordering is intentional.
	takenUsername := "takenshell"
	createResp = server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: takenUsername,
		Password: "takenshell1",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	var createdV api.UserResponse
	createResp.Unmarshal(t, &createdV)
	require.Equal(t, api.UserRoleViewer, createdV.User.Role)

	conflictResp := server.Client().Patch("/api/v1/users/"+soleAdminID, api.UpdateUserRequest{
		Role:     &viewerRole,
		Username: &takenUsername,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusConflict).Send(t)
	var conflictErr api.Error
	conflictResp.Unmarshal(t, &conflictErr)
	require.Equal(t, api.ErrorCodeAlreadyExists, conflictErr.Code)
	require.Equal(t, "Username already exists", conflictErr.Message)

	// The refused patch left the target untouched: the sole admin is still
	// an active admin.
	users = listUsers(adminToken)
	foundSoleAdmin = false
	for _, u := range users.Users {
		if u.Id == soleAdminID {
			foundSoleAdmin = true
			require.Equal(t, api.UserRoleAdmin, u.Role)
			require.False(t, u.IsDisabled != nil && *u.IsDisabled,
				"sole admin must stay enabled after the refused patch")
		}
	}
	require.True(t, foundSoleAdmin, "sole admin must survive the refused patch")

	// Sanity: the stored admin survived every refused mutation.
	expectMeRole(adminToken, api.UserRoleAdmin)
}

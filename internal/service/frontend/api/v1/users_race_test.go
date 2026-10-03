// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// raceDo performs a request with net/http directly (harness-free). It never
// calls t.FailNow, so it is safe to use from goroutines.
func raceDo(srv test.Server, method, path, token string, body any) (int, string, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		rdr = bytes.NewReader(b)
	}
	url := fmt.Sprintf("http://%s:%d%s", srv.Config.Server.Host, srv.Config.Server.Port, path)
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return 0, "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw), nil
}

// raceJSON performs a sequential request and unmarshals the raw body into v.
// Only use outside goroutines (it uses require).
func raceJSON(t *testing.T, srv test.Server, method, path, token string, body any, v any) (int, string) {
	t.Helper()
	st, raw, err := raceDo(srv, method, path, token, body)
	require.NoError(t, err)
	if v != nil {
		require.NoError(t, json.Unmarshal([]byte(raw), v), "body: %s", raw)
	}
	return st, raw
}

const raceLastAdminMsg = "Cannot remove the last active admin"

// TestCommunityMultiUser_ConcurrentDemotionKeepsOneAdmin is the race
// regression test for the TOCTOU that the handler-level
// preservesLastActiveAdmin pre-check used to have: two admins demoting EACH
// OTHER concurrently could both pass the check-then-act pre-check and leave
// ZERO active admins (a permanent lockout — first-run setup is one-shot).
//
// Phase A (the review scenario): stored admins A and B demote each other
// concurrently with a close(start) barrier. Every round must end with
// exactly one accepted (200) demotion, one 403, and exactly one active
// admin in the listing (NEVER 0). The 403 body is either the guard message
// or "Insufficient permissions": when the scheduler fully serializes the
// pair, the losing request arrives after its own principal was already
// demoted, so it is refused at the permission layer — an outcome that also
// preserves the invariant.
//
// Phase B (deterministic message pinning): the same concurrent demotion is
// issued by two service-account API keys, principals that can never be
// demoted themselves (they match no stored user). Permission checks always
// pass, so the store guard is what refuses the second write — the 403 MUST
// carry the last-active-admin message every single round. This phase fails
// deterministically if the invariant is moved out of the store lock again.
func TestCommunityMultiUser_ConcurrentDemotionKeepsOneAdmin(t *testing.T) {
	t.Parallel()

	srv := rbacTestServer(t)
	tokenA := communitySetupAdmin(t, srv) // "admin" / "adminpass1"

	// Create the second admin and give it a live session.
	st, raw := raceJSON(t, srv, http.MethodPost, "/api/v1/users", tokenA, map[string]any{
		"username": "adminb", "password": "adminbpass1", "role": "admin",
	}, nil)
	require.Equal(t, http.StatusCreated, st, raw)
	var created api.UserResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &created))
	bID := created.User.Id
	require.NotEmpty(t, bID)
	tokenB := loginAndGetToken(t, srv, "adminb", "adminbpass1")

	// Resolve A's id from the listing.
	_, rawList := raceJSON(t, srv, http.MethodGet, "/api/v1/users", tokenA, nil, nil)
	var list api.UsersListResponse
	require.NoError(t, json.Unmarshal([]byte(rawList), &list))
	aID := ""
	for _, u := range list.Users {
		if u.Username == "admin" {
			aID = u.Id
		}
	}
	require.NotEmpty(t, aID)

	// Two service-account API keys: admin principals immune to demotion,
	// used by phase B to pin the guard's refusal message deterministically.
	newRaceKey := func(name string) string {
		t.Helper()
		st, raw := raceJSON(t, srv, http.MethodPost, "/api/v1/api-keys", tokenA,
			api.CreateAPIKeyRequest{
				Name:             name,
				Role:             api.UserRoleAdmin,
				AllowedSurfaces:  []api.CreateAPIKeyRequestAllowedSurfaces{api.CreateAPIKeyRequestAllowedSurfacesRestApi},
				AttributionClass: api.CreateAPIKeyRequestAttributionClassServiceAccount,
			}, nil)
		require.Equal(t, http.StatusCreated, st, raw)
		var resp api.CreateAPIKeyResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &resp))
		require.NotEmpty(t, resp.Key)
		return resp.Key
	}
	key1 := newRaceKey("race-key-1")
	key2 := newRaceKey("race-key-2")

	// countActiveAdmins lists users with the given token and returns how many
	// stored users are active admins (role admin and not disabled).
	countActiveAdmins := func(token string) int {
		t.Helper()
		st, raw, err := raceDo(srv, http.MethodGet, "/api/v1/users", token, nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, st, raw)
		var l api.UsersListResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &l), "body: %s", raw)
		n := 0
		for _, u := range l.Users {
			disabled := u.IsDisabled != nil && *u.IsDisabled
			if u.Role == api.UserRoleAdmin && !disabled {
				n++
			}
		}
		return n
	}

	// patchRole is a sequential (harness-free) PATCH used for re-arming only.
	patchRole := func(token, id, role string) (int, string) {
		t.Helper()
		st, raw, err := raceDo(srv, http.MethodPatch, "/api/v1/users/"+id, token,
			map[string]any{"role": role})
		require.NoError(t, err)
		return st, raw
	}

	// rearmBoth promotes BOTH accounts to admin using a token that is known
	// to belong to a currently active admin, then proves the precondition.
	rearmBoth := func(armToken string, round int) {
		t.Helper()
		for _, id := range []string{aID, bID} {
			st, raw := patchRole(armToken, id, string(api.UserRoleAdmin))
			require.Equal(t, http.StatusOK, st, "re-arm round %d id %s: %s", round, id, raw)
		}
		require.Equal(t, 2, countActiveAdmins(armToken),
			"round %d: both accounts must be active admins before the race", round)
	}

	type outcome struct {
		refusedMsg string
		okCount    int
		okWho      string
		refusedWho string
	}
	runRaceRound := func(round int, caller1, caller2, target1, target2, who1, who2 string) outcome {
		t.Helper()
		start := make(chan struct{})
		type result struct {
			who    string
			status int
			body   string
			err    error
		}
		results := make(chan result, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		fire := func(caller, target, who string) {
			defer wg.Done()
			<-start
			st, raw, err := raceDo(srv, http.MethodPatch, "/api/v1/users/"+target, caller,
				map[string]any{"role": "viewer"})
			results <- result{who, st, raw, err}
		}
		go fire(caller1, target1, who1)
		go fire(caller2, target2, who2)
		close(start)
		wg.Wait()
		close(results)

		var out outcome
		for r := range results {
			require.NoError(t, r.err, "round %d: %s request failed", round, r.who)
			switch r.status {
			case http.StatusOK:
				out.okCount++
				out.okWho = r.who
			case http.StatusForbidden:
				var e api.Error
				require.NoError(t, json.Unmarshal([]byte(r.body), &e), "round %d: %s", round, r.body)
				out.refusedMsg = e.Message
				out.refusedWho = r.who
			default:
				t.Fatalf("round %d: %s returned unexpected status %d: %s",
					round, r.who, r.status, r.body)
			}
		}
		return out
	}

	// ---- Phase A: stored admins demote each other (review scenario). ----
	const iterations = 15
	survivorToken := tokenA // iteration 0: both admins already
	lastAdminRefusals := 0
	for i := range iterations {
		rearmBoth(survivorToken, i)

		out := runRaceRound(i, tokenA, tokenB, bID, aID, "A_demotes_B", "B_demotes_A")

		require.Equal(t, 1, out.okCount,
			"phase A round %d: exactly one demotion must be accepted (TOCTOU regression if 2)", i)
		// The refusal is either the store guard or — when the pair fully
		// serialized and the loser lost admin first — the permission layer.
		require.Contains(t, []string{raceLastAdminMsg, "Insufficient permissions"},
			out.refusedMsg, "phase A round %d: unexpected 403 body", i)
		if out.refusedMsg == raceLastAdminMsg {
			lastAdminRefusals++
		}

		// The winner's token is the survivor (its own account was not touched).
		if out.okWho == "A_demotes_B" {
			survivorToken = tokenA
		} else {
			survivorToken = tokenB
		}
		// The invariant holds: EXACTLY one active admin remains (never 0),
		// verified against the listing.
		require.Equal(t, 1, countActiveAdmins(survivorToken),
			"phase A round %d: exactly one active admin must remain after the race", i)
	}
	t.Logf("phase A: %d/%d rounds refused with the last-active-admin message "+
		"(the rest serialized into an insufficient-permissions refusal; all kept one admin)",
		lastAdminRefusals, iterations)

	// ---- Phase B: demotions fired by demotion-immune API-key principals. --
	// Both permission checks always pass, so the second write always reaches
	// the in-lock guard: one 200, one 403, and the 403 MUST be the
	// last-active-admin message — every round.
	for i := range iterations {
		rearmBoth(survivorToken, i)

		out := runRaceRound(i, key1, key2, aID, bID, "K1_demotes_A", "K2_demotes_B")

		require.Equal(t, 1, out.okCount,
			"phase B round %d: exactly one demotion must be accepted (TOCTOU regression if 2)", i)
		require.Equal(t, raceLastAdminMsg, out.refusedMsg,
			"phase B round %d: the store guard must refuse the second write with the "+
				"last-active-admin message (got from %s)", i, out.refusedWho)

		// The refused request targeted the admin that SURVIVED; its token
		// (of the surviving stored admin) lists users and re-arms the next
		// round.
		if out.refusedWho == "K1_demotes_A" {
			survivorToken = tokenA
		} else {
			survivorToken = tokenB
		}
		require.Equal(t, 1, countActiveAdmins(survivorToken),
			"phase B round %d: exactly one active admin must remain after the race", i)
	}
}

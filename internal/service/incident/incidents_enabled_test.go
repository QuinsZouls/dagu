// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incident

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	incidentmodel "github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
)

// TestIncidentDestinationsAndDeliveryAreAlwaysActive is the plan's R-2
// proof: the incident service has no enable/disable concept at all, so on a
// service constructed with no feature gating whatsoever (this package has
// zero references to the removed gating), creating a provider + policy must
// yield live destinations and real delivery through every path that
// previously carried the `!incidentsAllowed()` guard
// (NotificationDestinations, NotificationDestinationsForEvent and
// FlushNotificationBatch).
func TestIncidentDestinationsAndDeliveryAreAlwaysActive(t *testing.T) {
	var requests []map[string]any
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		defer func() { _ = req.Body.Close() }()
		var payload map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		requests = append(requests, payload)
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}

	store := newMemoryStore(t)
	provider := store.saveProvider(t, "PagerDuty", incidentmodel.ProviderPagerDuty)
	policySet := store.savePolicySet(t, &incidentmodel.PolicySet{
		Scope:   incidentmodel.PolicyScopeGlobal,
		Enabled: true,
		Policies: []incidentmodel.Policy{{
			ProviderID:          provider.ID,
			Enabled:             true,
			ResolveOnRecovery:   true,
			DedupKeyTemplate:    "dagu:{{dag.name}}",
			MessageTemplate:     "Dagu {{dag.name}} failed",
			DescriptionTemplate: "Run {{run.id}} failed",
		}},
	})
	svc := New(store, WithHTTPClient(client))

	// Guard 1 (was: `!s.incidentsAllowed() || s.store == nil`):
	// the policy destination must be reported.
	destinations := svc.NotificationDestinations()
	require.Len(t, destinations, 1)
	assert.Equal(t, policyDestinationID(policySet, policySet.Policies[0].ID), destinations[0])

	// Guard 2 (was: `!s.incidentsAllowed() || !incidentEventSupported(event)`):
	// a failure event must resolve to a live destination.
	failure := failedEvent("daily", "run-1")
	eventDestinations := svc.NotificationDestinationsForEvent(failure)
	require.Len(t, eventDestinations, 1)

	// Guard 3 (was: `if !s.incidentsAllowed() { return true }`):
	// flushing must actually deliver, not no-op.
	require.True(t, svc.FlushNotificationBatch(context.Background(), eventDestinations[0], chatbridge.NotificationBatch{
		Events: []chatbridge.NotificationEvent{failure},
	}, false))
	require.Len(t, requests, 1)
	assert.Equal(t, "trigger", requests[0]["event_action"])
	assert.Equal(t, provider.PagerDuty.RoutingKey, requests[0]["routing_key"])
}

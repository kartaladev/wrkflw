package persistence_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/persistence"
)

func TestWarnUnsafeConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		profile persistence.DeploymentProfile
		assert  func(t *testing.T, logged string)
	}{
		"fully safe profile warns nothing": {
			profile: persistence.DeploymentProfile{
				HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.Empty(t, strings.TrimSpace(logged), "no warnings for a safe profile")
			},
		},
		"multi-replica call-links without lease warns": {
			profile: persistence.DeploymentProfile{
				MultiReplica: true, CallLinksEnabled: true, CallLinkLeaseWired: false,
				HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, logged, persistence.WarnMsgCallLinkLease)
			},
		},
		"lease wired suppresses the call-link warning": {
			profile: persistence.DeploymentProfile{
				MultiReplica: true, CallLinksEnabled: true, CallLinkLeaseWired: true,
				HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.NotContains(t, logged, persistence.WarnMsgCallLinkLease)
			},
		},
		"missing history cap and pruning both warn": {
			profile: persistence.DeploymentProfile{},
			assert: func(t *testing.T, logged string) {
				assert.Contains(t, logged, persistence.WarnMsgHistoryCap)
				assert.Contains(t, logged, persistence.WarnMsgPruning)
			},
		},
		"call-links disabled suppresses call-link lease warning even in multi-replica": {
			profile: persistence.DeploymentProfile{
				MultiReplica: true, CallLinksEnabled: false, HistoryCapSet: true, PruningScheduled: true,
			},
			assert: func(t *testing.T, logged string) {
				assert.NotContains(t, logged, persistence.WarnMsgCallLinkLease)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			persistence.WarnUnsafeConfig(logger, tc.profile)
			tc.assert(t, buf.String())
		})
	}
}

func TestWarnUnsafeConfig_NilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		persistence.WarnUnsafeConfig(nil, persistence.DeploymentProfile{})
	})
}

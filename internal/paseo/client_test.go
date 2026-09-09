package paseo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

const testDaemonOwner = "1000@test-host"

func TestДоменныйКлиентПолучаетПровереннуюСредуБезWireДеталей(t *testing.T) {
	client := newFakeClient(t)
	setCompatibleEnvironment(t, nil)

	environment, err := client.CheckCompatibility(context.Background())
	if err != nil {
		t.Fatalf("проверить совместимость: %v", err)
	}
	if environment.ServerID().String() != "srv_test123" {
		t.Fatalf("неожиданный serverId: %q", environment.ServerID().String())
	}
}

func newFakeClient(t *testing.T) *Client {
	t.Helper()
	adapter := newFakeAdapter(t, adapterConfig{
		timeout:     time.Second,
		stdoutLimit: 64 << 10,
		stderrLimit: 64 << 10,
	})
	return newClient(adapter, testDaemonOwner)
}

func setCompatibleEnvironment(t *testing.T, mutate func(map[string]any)) {
	t.Helper()
	t.Setenv("FAKE_PASEO_VERSION", paseocli.ActiveContract().CLIVersion())
	t.Setenv("FAKE_PASEO_STATUS", statusJSON(t, mutate))
}

func statusJSON(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	status := map[string]any{
		"serverId":        "srv_test123",
		"localDaemon":     "running",
		"connectedDaemon": "reachable",
		"home":            "/tmp/paseo-home",
		"listen":          "127.0.0.1:6767",
		"relay":           "disabled",
		"hostname":        "test-host",
		"pid":             42,
		"startedAt":       "2026-09-07T08:11:35.910Z",
		"owner":           testDaemonOwner,
		"logPath":         "/tmp/paseo-home/daemon.log",
		"daemonNode":      "/usr/bin/node",
		"cliNode":         "/usr/bin/node",
		"cliVersion":      paseocli.ActiveContract().CLIVersion(),
		"daemonVersion":   paseocli.ActiveContract().DaemonVersion(),
		"desktopManaged":  false,
		"providers": []map[string]any{
			{
				"label":   "Codex",
				"path":    "available",
				"version": nil,
				"source":  "daemon",
			},
		},
	}
	if mutate != nil {
		mutate(status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("собрать JSON статуса: %v", err)
	}
	return string(encoded)
}

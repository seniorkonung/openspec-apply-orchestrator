package paseo

import (
	"context"
	"strings"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

func TestNewRuntimeОднойОперациейПроверяетСредуИФормируетСсылку(t *testing.T) {
	newFakeAdapter(t, defaultAdapterConfig())
	owner, err := currentDaemonOwner()
	if err != nil {
		t.Fatalf("определить владельца daemon: %v", err)
	}
	_, hostname, ok := strings.Cut(owner, "@")
	if !ok {
		t.Fatalf("неожиданный владелец daemon: %q", owner)
	}
	t.Setenv("FAKE_PASEO_VERSION", paseocli.ActiveContract().CLIVersion())
	t.Setenv("FAKE_PASEO_STATUS", statusJSON(t, func(status map[string]any) {
		status["owner"] = owner
		status["hostname"] = hostname
	}))
	t.Setenv("FAKE_PASEO_WORKSPACES", "[]")

	runtime, err := NewRuntime(context.Background())
	if err != nil {
		t.Fatalf("открыть проверенный runtime: %v", err)
	}
	if runtime.ServerID() != "srv_test123" {
		t.Fatalf("неожиданный serverId runtime: %q", runtime.ServerID())
	}

	change, err := orchestrator.NewChangeKey("change-1")
	if err != nil {
		t.Fatalf("создать ключ change: %v", err)
	}
	workspaces, err := runtime.FindActiveWorkspace(context.Background(), change, t.TempDir())
	if err != nil {
		t.Fatalf("прочитать workspace через runtime: %v", err)
	}
	if _, ok := workspaces.(orchestrator.NoManagedWorkspace); !ok {
		t.Fatalf("ожидалось отсутствие workspace, получено %T", workspaces)
	}

	session, err := orchestrator.NewSessionID("session/one")
	if err != nil {
		t.Fatalf("создать ID сессии: %v", err)
	}
	if got := runtime.SessionLink(session); got != "paseo://h/srv_test123/agent/session%2Fone" {
		t.Fatalf("неожиданная ссылка сессии: %q", got)
	}
}

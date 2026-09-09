package paseocli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const activeFixtureDirectory = "v0.8.0-beta.1"

func TestFixturesАктивногоКонтрактаPaseo080ПроходятЗначимуюПроверку(t *testing.T) {
	contract := ActiveContract()
	version := strings.TrimSpace(string(readContractFixture(t, activeFixtureDirectory, "version.txt")))
	if !contract.MatchesCLIVersion(version) {
		t.Fatalf("fixture версии %q не соответствует активному контракту", version)
	}

	status, err := decodeDaemonStatus(readContractFixture(t, activeFixtureDirectory, "status.json"))
	if err != nil {
		t.Fatalf("проверить fixture status: %v", err)
	}
	if !contract.MatchesCLIVersion(status.cliVersion) || status.daemonVersion == nil ||
		!contract.MatchesDaemonVersion(*status.daemonVersion) {
		t.Fatalf("status не подтверждает точный активный выпуск: %#v", status)
	}

	providers, err := decodeProviderCatalog(readContractFixture(t, activeFixtureDirectory, "provider-ls.json"))
	if err != nil {
		t.Fatalf("проверить fixture provider ls: %v", err)
	}
	if len(providers) != 2 || providers[0].ID() != "codex" || !providers[0].Available() {
		t.Fatalf("неожиданная проекция provider ls: %#v", providers)
	}

	models, err := decodeModelCatalog(readContractFixture(t, activeFixtureDirectory, "provider-models-codex.json"))
	if err != nil {
		t.Fatalf("проверить fixture provider models: %v", err)
	}
	if len(models) == 0 || models[0].ID() != "gpt-6-astra" ||
		!slices.Contains(models[0].Reasoning(), "ultra") {
		t.Fatalf("неожиданная проекция provider models: %#v", models)
	}

	workspaces, err := decodeActiveWorkspaces(readContractFixture(t, activeFixtureDirectory, "workspace-ls.json"))
	if err != nil {
		t.Fatalf("проверить fixture workspace ls: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].ID() != "wks_fixture" {
		t.Fatalf("неожиданная проекция workspace ls: %#v", workspaces)
	}
	createdWorkspace, err := decodeCreatedWorkspace(readContractFixture(t, activeFixtureDirectory, "workspace-create.json"))
	if err != nil {
		t.Fatalf("проверить fixture workspace create: %v", err)
	}
	if createdWorkspace.ID() != "wks_fixture" || createdWorkspace.CWD() != "/tmp/paseo-fixture/workspace" {
		t.Fatalf("неожиданная проекция workspace create: %#v", createdWorkspace)
	}

	sessions, err := decodeListedSessions(readContractFixture(t, activeFixtureDirectory, "session-ls.json"))
	if err != nil {
		t.Fatalf("проверить fixture ls: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID() != "agent-fixture" || sessions[0].State() != SessionIdle {
		t.Fatalf("неожиданная проекция ls: %#v", sessions)
	}
	inspection, err := decodeSessionInspection(
		readContractFixture(t, activeFixtureDirectory, "session-inspect.json"),
		"agent-fixture",
	)
	if err != nil {
		t.Fatalf("проверить fixture inspect: %v", err)
	}
	if inspection.ID() != "agent-fixture" || inspection.State() != SessionIdle || inspection.Archived() {
		t.Fatalf("неожиданная проекция inspect: %#v", inspection)
	}
	waitEvent, err := decodeWaitEvent(
		readContractFixture(t, activeFixtureDirectory, "session-wait.json"),
		"agent-fixture",
	)
	if err != nil {
		t.Fatalf("проверить fixture wait: %v", err)
	}
	if waitEvent != WaitEventIdle {
		t.Fatalf("неожиданная проекция wait: %v", waitEvent)
	}
	createdSession, err := decodeCreatedSession(readContractFixture(t, activeFixtureDirectory, "session-run.json"))
	if err != nil {
		t.Fatalf("проверить fixture run: %v", err)
	}
	if createdSession.ID() != "agent-fixture" || createdSession.CWD() != "/tmp/paseo-fixture/workspace" {
		t.Fatalf("неожиданная проекция run: %#v", createdSession)
	}
	if err := decodeArchivedSession(
		readContractFixture(t, activeFixtureDirectory, "session-archive.json"),
		"agent-fixture",
	); err != nil {
		t.Fatalf("проверить fixture archive: %v", err)
	}
}

func TestFixturePaseo072БольшеНеСовместимаСАктивнымКонтрактом(t *testing.T) {
	status, err := decodeDaemonStatus(readContractFixture(t, "incompatible", "status-v0.7.2.json"))
	if err != nil {
		t.Fatalf("прочитать fixture прежнего выпуска: %v", err)
	}
	if ActiveContract().MatchesCLIVersion(status.cliVersion) {
		t.Fatal("активный контракт принял CLI Paseo 0.7.2")
	}
	if status.daemonVersion == nil || ActiveContract().MatchesDaemonVersion(*status.daemonVersion) {
		t.Fatal("активный контракт принял daemon Paseo 0.7.2")
	}
}

func TestFixtureСоЗначимымИзменениемInspectОтклоняется(t *testing.T) {
	_, err := decodeSessionInspection(
		readContractFixture(t, "incompatible", "session-inspect-missing-pending-permissions.json"),
		"agent-fixture",
	)
	if !errors.Is(err, ErrUnexpectedJSON) {
		t.Fatalf("ожидался отказ значимого wire-контракта, получено %v", err)
	}
}

func readContractFixture(t *testing.T, directory, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", directory, name))
	if err != nil {
		t.Fatalf("прочитать fixture %s/%s: %v", directory, name, err)
	}
	return content
}

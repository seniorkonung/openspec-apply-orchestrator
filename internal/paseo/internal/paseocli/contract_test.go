package paseocli

import "testing"

func TestАктивныйКонтрактТочноОпределяетВерсииИСистемныйПолныйДоступ(t *testing.T) {
	contract := ActiveContract()

	if contract.CLIVersion() != "0.7.2" || !contract.MatchesCLIVersion("0.7.2") {
		t.Fatalf("активный контракт не требует точную версию CLI: %q", contract.CLIVersion())
	}
	if contract.DaemonVersion() != "0.7.2" || !contract.MatchesDaemonVersion("0.7.2") {
		t.Fatalf("активный контракт не требует точную версию daemon: %q", contract.DaemonVersion())
	}

	mode, supported := contract.FullAccessMode("codex")
	if !supported {
		t.Fatal("активный контракт не содержит системную семантику codex")
	}
	if mode.ID() != "full-access" || mode.approvalPolicy != "never" ||
		mode.sandbox != "danger-full-access" {
		t.Fatalf("неверная системная семантика полного доступа: %#v", mode)
	}
}

func TestАктивныйКонтрактНеВыбираетДругойВыпускProviderИлиDefault(t *testing.T) {
	contract := ActiveContract()

	for _, version := range []string{"0.7.1", "0.8.0", "^0.7.2", "default"} {
		if contract.MatchesCLIVersion(version) || contract.MatchesDaemonVersion(version) {
			t.Fatalf("активный контракт принял другой выпуск %q", version)
		}
	}
	for _, provider := range []string{"", "codex-work", "oa-integration", "default"} {
		if mode, supported := contract.FullAccessMode(provider); supported {
			t.Fatalf("активный контракт выбрал неожиданный provider %q и режим %q", provider, mode.ID())
		}
	}
}

func TestКонтрактБезПолнойСистемнойСемантикиНеСтановитсяАктивным(t *testing.T) {
	contract := Contract{
		cliVersion:    "0.7.2",
		daemonVersion: "0.7.2",
		fullAccess: FullAccessMode{
			provider: "codex",
			id:       "full-access",
			sandbox:  "danger-full-access",
		},
	}

	if contract.IsActive() || contract.MatchesCLIVersion("0.7.2") ||
		contract.MatchesDaemonVersion("0.7.2") {
		t.Fatal("неполный контракт был принят как совместимый")
	}
	if _, supported := contract.FullAccessMode("codex"); supported {
		t.Fatal("контракт без approval policy выдал режим полного доступа")
	}
}

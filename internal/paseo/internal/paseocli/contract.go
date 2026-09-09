package paseocli

// Contract описывает единственный активный production-контракт Paseo.
// Нулевое и любое отличающееся значение не являются совместимыми.
type Contract struct {
	cliVersion    string
	daemonVersion string
	fullAccess    FullAccessMode
}

// FullAccessMode хранит доказанную системную семантику встроенного provider.
type FullAccessMode struct {
	provider       string
	id             string
	approvalPolicy string
	sandbox        string
}

// Источники активного контракта:
// https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/protocol/src/provider-manifest.ts
// https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/server/src/server/agent/providers/codex-app-server-agent.ts
var activeContract = Contract{
	cliVersion:    "0.8.0-beta.1",
	daemonVersion: "0.8.0-beta.1",
	fullAccess: FullAccessMode{
		provider:       "codex",
		id:             "full-access",
		approvalPolicy: "never",
		sandbox:        "danger-full-access",
	},
}

func ActiveContract() Contract {
	return activeContract
}

func (contract Contract) CLIVersion() string {
	return contract.cliVersion
}

func (contract Contract) DaemonVersion() string {
	return contract.daemonVersion
}

func (contract Contract) IsActive() bool {
	return contract.valid() && contract == activeContract
}

func (contract Contract) MatchesCLIVersion(version string) bool {
	return contract.IsActive() && version == contract.cliVersion
}

func (contract Contract) MatchesDaemonVersion(version string) bool {
	return contract.IsActive() && version == contract.daemonVersion
}

func (contract Contract) FullAccessMode(provider string) (FullAccessMode, bool) {
	if !contract.IsActive() || provider != contract.fullAccess.provider {
		return FullAccessMode{}, false
	}
	return contract.fullAccess, true
}

func (contract Contract) valid() bool {
	return contract.cliVersion != "" && contract.daemonVersion != "" && contract.fullAccess.valid()
}

func (mode FullAccessMode) ID() string {
	return mode.id
}

func (mode FullAccessMode) valid() bool {
	return mode.provider != "" && mode.id != "" && mode.approvalPolicy != "" && mode.sandbox != ""
}

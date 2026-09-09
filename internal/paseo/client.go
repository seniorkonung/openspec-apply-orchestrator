package paseo

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strings"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

const maxPaseoVersionLength = 64

var paseoVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type Version struct {
	value string
}

func (version Version) String() string {
	return version.value
}

type ServerID struct {
	value string
}

func (id ServerID) String() string {
	return id.value
}

type CompatibleEnvironment struct {
	serverID ServerID
	contract paseocli.Contract
}

func (environment CompatibleEnvironment) ServerID() ServerID {
	return environment.serverID
}

func (environment CompatibleEnvironment) Version() Version {
	return Version{value: environment.contract.CLIVersion()}
}

type Client struct {
	runner        *runner
	expectedOwner string
}

func NewClient() (*Client, error) {
	runner, err := newRunner(defaultRunnerConfig())
	if err != nil {
		return nil, err
	}
	owner, err := currentDaemonOwner()
	if err != nil {
		return nil, err
	}
	return newClient(runner, owner), nil
}

func newClient(runner *runner, expectedOwner string) *Client {
	return &Client{runner: runner, expectedOwner: expectedOwner}
}

func (client *Client) Version(ctx context.Context) (Version, error) {
	output, err := client.runner.run(ctx, command{name: "version", args: []string{"--version"}})
	if err != nil {
		return Version{}, err
	}
	raw := string(output)
	if raw == "" {
		return Version{}, ErrEmptyOutput
	}
	value := strings.TrimSuffix(raw, "\n")
	value = strings.TrimSuffix(value, "\r")
	if !validPaseoVersion(value) {
		return Version{}, ErrUnexpectedVersionOutput
	}
	return Version{value: value}, nil
}

func (client *Client) CheckCompatibility(ctx context.Context) (CompatibleEnvironment, error) {
	contract := paseocli.ActiveContract()
	version, err := client.Version(ctx)
	if err != nil {
		return CompatibleEnvironment{}, err
	}
	if !contract.MatchesCLIVersion(version.String()) {
		return CompatibleEnvironment{}, fmt.Errorf("%w: обнаружена %q, требуется %q", ErrIncompatibleCLIVersion, version, contract.CLIVersion())
	}

	status, err := client.Status(ctx)
	if err != nil {
		return CompatibleEnvironment{}, err
	}
	if status.cliVersion.String() != version.String() {
		return CompatibleEnvironment{}, fmt.Errorf("%w: --version=%q, status=%q", ErrInconsistentCLIVersion, version, status.cliVersion)
	}
	if status.localState != localDaemonRunning {
		return CompatibleEnvironment{}, fmt.Errorf("%w: состояние %s", ErrDaemonNotLocal, status.localState)
	}
	if status.connectionState != daemonReachable {
		return CompatibleEnvironment{}, fmt.Errorf("%w: состояние %s", ErrDaemonUnavailable, status.connectionState)
	}
	if status.owner == nil || *status.owner != client.expectedOwner {
		return CompatibleEnvironment{}, ErrDaemonOwnerMismatch
	}
	if status.daemonVersion == nil {
		return CompatibleEnvironment{}, fmt.Errorf("%w: версия не сообщена", ErrIncompatibleDaemonVersion)
	}
	if !contract.MatchesDaemonVersion(status.daemonVersion.String()) {
		return CompatibleEnvironment{}, fmt.Errorf(
			"%w: обнаружена %q, требуется %q",
			ErrIncompatibleDaemonVersion,
			status.daemonVersion,
			contract.DaemonVersion(),
		)
	}

	return CompatibleEnvironment{serverID: status.serverID, contract: contract}, nil
}

func validPaseoVersion(value string) bool {
	return len(value) <= maxPaseoVersionLength && paseoVersionPattern.MatchString(value)
}

func currentDaemonOwner() (string, error) {
	currentUser, err := user.Current()
	if err != nil || currentUser.Uid == "" {
		return "", fmt.Errorf("%w: uid", ErrCurrentIdentity)
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "", fmt.Errorf("%w: hostname", ErrCurrentIdentity)
	}
	return currentUser.Uid + "@" + hostname, nil
}

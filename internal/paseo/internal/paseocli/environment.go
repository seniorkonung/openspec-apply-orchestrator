package paseocli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const maxPaseoVersionLength = 64

var paseoVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type CompatibleEnvironment struct {
	serverID string
	contract Contract
}

func (environment CompatibleEnvironment) ServerID() string {
	return environment.serverID
}

func (environment CompatibleEnvironment) IsCompatible() bool {
	return validIdentifierValue(environment.serverID) && environment.contract.IsActive()
}

func (environment CompatibleEnvironment) FullAccessMode(provider string) (FullAccessMode, bool) {
	if !environment.IsCompatible() {
		return FullAccessMode{}, false
	}
	return environment.contract.FullAccessMode(provider)
}

func (adapter *Adapter) CheckEnvironment(
	ctx context.Context,
	expectedOwner string,
) (CompatibleEnvironment, error) {
	contract := ActiveContract()
	version, err := adapter.readVersion(ctx)
	if err != nil {
		return CompatibleEnvironment{}, err
	}
	if !contract.MatchesCLIVersion(version) {
		return CompatibleEnvironment{}, fmt.Errorf(
			"%w: обнаружена %q, требуется %q",
			ErrIncompatibleCLIVersion,
			version,
			contract.CLIVersion(),
		)
	}

	status, err := adapter.readStatus(ctx)
	if err != nil {
		return CompatibleEnvironment{}, err
	}
	if status.cliVersion != version {
		return CompatibleEnvironment{}, fmt.Errorf(
			"%w: --version=%q, status=%q",
			ErrInconsistentCLIVersion,
			version,
			status.cliVersion,
		)
	}
	if status.localState != localDaemonRunning {
		return CompatibleEnvironment{}, fmt.Errorf("%w: состояние %s", ErrDaemonNotLocal, status.localState)
	}
	if status.connectionState != daemonReachable {
		return CompatibleEnvironment{}, fmt.Errorf(
			"%w: состояние %s",
			ErrDaemonUnavailable,
			status.connectionState,
		)
	}
	if status.owner == nil || *status.owner != expectedOwner {
		return CompatibleEnvironment{}, ErrDaemonOwnerMismatch
	}
	if status.daemonVersion == nil {
		return CompatibleEnvironment{}, fmt.Errorf("%w: версия не сообщена", ErrIncompatibleDaemonVersion)
	}
	if !contract.MatchesDaemonVersion(*status.daemonVersion) {
		return CompatibleEnvironment{}, fmt.Errorf(
			"%w: обнаружена %q, требуется %q",
			ErrIncompatibleDaemonVersion,
			*status.daemonVersion,
			contract.DaemonVersion(),
		)
	}

	return CompatibleEnvironment{serverID: status.serverID, contract: contract}, nil
}

func (adapter *Adapter) readVersion(ctx context.Context) (string, error) {
	output, err := adapter.Run(ctx, Invocation{Name: "version", Arguments: []string{"--version"}})
	if err != nil {
		return "", err
	}
	raw := string(output)
	if raw == "" {
		return "", ErrEmptyOutput
	}
	version := strings.TrimSuffix(raw, "\n")
	version = strings.TrimSuffix(version, "\r")
	if !validPaseoVersion(version) {
		return "", ErrUnexpectedVersionOutput
	}
	return version, nil
}

func (adapter *Adapter) readStatus(ctx context.Context) (daemonStatus, error) {
	output, err := adapter.Run(ctx, Invocation{
		Name:      "status",
		Arguments: []string{"status", "--json"},
	})
	if err != nil {
		return daemonStatus{}, err
	}
	return decodeDaemonStatus(output)
}

type localDaemonState uint8

const (
	localDaemonRunning localDaemonState = iota + 1
	localDaemonStopped
	localDaemonStalePID
	localDaemonUnresponsive
)

func (state localDaemonState) String() string {
	switch state {
	case localDaemonRunning:
		return "running"
	case localDaemonStopped:
		return "stopped"
	case localDaemonStalePID:
		return "stale_pid"
	case localDaemonUnresponsive:
		return "unresponsive"
	default:
		return "unknown"
	}
}

type daemonConnectionState uint8

const (
	daemonReachable daemonConnectionState = iota + 1
	daemonUnreachable
	daemonNotProbed
	daemonAuthRequired
	daemonAuthFailed
)

func (state daemonConnectionState) String() string {
	switch state {
	case daemonReachable:
		return "reachable"
	case daemonUnreachable:
		return "unreachable"
	case daemonNotProbed:
		return "not_probed"
	case daemonAuthRequired:
		return "auth_required"
	case daemonAuthFailed:
		return "auth_failed"
	default:
		return "unknown"
	}
}

type daemonStatus struct {
	serverID        string
	localState      localDaemonState
	connectionState daemonConnectionState
	owner           *string
	cliVersion      string
	daemonVersion   *string
}

type requiredValue[T any] struct {
	value   T
	present bool
}

func (field *requiredValue[T]) UnmarshalJSON(data []byte) error {
	field.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("обязательное поле не может быть null")
	}
	return json.Unmarshal(data, &field.value)
}

type requiredNullable[T any] struct {
	value   *T
	present bool
}

func (field *requiredNullable[T]) UnmarshalJSON(data []byte) error {
	field.present = true
	return json.Unmarshal(data, &field.value)
}

type optionalValue[T any] struct {
	value   T
	present bool
}

func (field *optionalValue[T]) UnmarshalJSON(data []byte) error {
	field.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("необязательное поле не может быть null")
	}
	return json.Unmarshal(data, &field.value)
}

// Схема соответствует JSON Paseo CLI 0.8.0-beta.1 и намеренно отклоняет новые поля.
// Источник: https://github.com/getpaseo/paseo/blob/v0.8.0-beta.1/packages/cli/src/commands/daemon/status.ts
type rawStatusJSON struct {
	ServerID        requiredNullable[string]      `json:"serverId"`
	LocalDaemon     requiredValue[string]         `json:"localDaemon"`
	ConnectedDaemon requiredValue[string]         `json:"connectedDaemon"`
	Home            requiredValue[string]         `json:"home"`
	Listen          requiredValue[string]         `json:"listen"`
	Relay           requiredValue[string]         `json:"relay"`
	Hostname        requiredNullable[string]      `json:"hostname"`
	PID             requiredNullable[int]         `json:"pid"`
	StartedAt       requiredNullable[string]      `json:"startedAt"`
	Owner           requiredNullable[string]      `json:"owner"`
	LogPath         requiredValue[string]         `json:"logPath"`
	DaemonNode      requiredValue[string]         `json:"daemonNode"`
	CLINode         requiredValue[string]         `json:"cliNode"`
	CLIVersion      requiredValue[string]         `json:"cliVersion"`
	DaemonVersion   requiredNullable[string]      `json:"daemonVersion"`
	DesktopManaged  requiredValue[bool]           `json:"desktopManaged"`
	Providers       requiredValue[[]providerJSON] `json:"providers"`
	Note            optionalValue[string]         `json:"note"`
}

type providerJSON struct {
	Label   requiredValue[string]    `json:"label"`
	Path    requiredNullable[string] `json:"path"`
	Version requiredNullable[string] `json:"version"`
	Source  optionalValue[string]    `json:"source"`
}

func decodeDaemonStatus(output []byte) (daemonStatus, error) {
	var raw rawStatusJSON
	if err := decodeStrictJSON(output, &raw); err != nil {
		return daemonStatus{}, err
	}
	if err := validateRequiredStatusFields(raw); err != nil {
		return daemonStatus{}, err
	}

	serverID, err := parseServerID(raw.ServerID.value)
	if err != nil {
		return daemonStatus{}, err
	}
	localState, err := parseLocalDaemonState(raw.LocalDaemon.value)
	if err != nil {
		return daemonStatus{}, err
	}
	connectionState, err := parseConnectionState(raw.ConnectedDaemon.value)
	if err != nil {
		return daemonStatus{}, err
	}
	if err := validateStatusDetails(raw); err != nil {
		return daemonStatus{}, err
	}

	return daemonStatus{
		serverID:        serverID,
		localState:      localState,
		connectionState: connectionState,
		owner:           raw.Owner.value,
		cliVersion:      raw.CLIVersion.value,
		daemonVersion:   raw.DaemonVersion.value,
	}, nil
}

func decodeStrictJSON(output []byte, target any) error {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return ErrEmptyOutput
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return classifyJSONError(err, len(trimmed))
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return classifyJSONError(err, len(trimmed))
		}
		return ErrUnexpectedJSON
	}
	return nil
}

func classifyJSONError(err error, length int) error {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncatedJSON
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) && syntaxError.Offset >= int64(length) {
		return ErrTruncatedJSON
	}
	return ErrUnexpectedJSON
}

func validateRequiredStatusFields(raw rawStatusJSON) error {
	present := raw.ServerID.present && raw.LocalDaemon.present && raw.ConnectedDaemon.present &&
		raw.Home.present && raw.Listen.present && raw.Relay.present && raw.Hostname.present &&
		raw.PID.present && raw.StartedAt.present && raw.Owner.present && raw.LogPath.present &&
		raw.DaemonNode.present && raw.CLINode.present && raw.CLIVersion.present &&
		raw.DaemonVersion.present && raw.DesktopManaged.present && raw.Providers.present
	if !present {
		return fmt.Errorf("%w: отсутствует обязательное поле status", ErrUnexpectedJSON)
	}
	for index, provider := range raw.Providers.value {
		if !provider.Label.present || !provider.Path.present || !provider.Version.present {
			return fmt.Errorf("%w: provider %d не содержит обязательное поле", ErrUnexpectedJSON, index+1)
		}
	}
	return nil
}

func parseServerID(value *string) (string, error) {
	if value == nil || !validIdentifierValue(*value) {
		return "", ErrInvalidServerID
	}
	return *value, nil
}

func parseLocalDaemonState(value string) (localDaemonState, error) {
	switch value {
	case "running":
		return localDaemonRunning, nil
	case "stopped":
		return localDaemonStopped, nil
	case "stale_pid":
		return localDaemonStalePID, nil
	case "unresponsive":
		return localDaemonUnresponsive, nil
	default:
		return 0, fmt.Errorf("%w: localDaemon", ErrUnexpectedJSON)
	}
}

func parseConnectionState(value string) (daemonConnectionState, error) {
	switch value {
	case "reachable":
		return daemonReachable, nil
	case "unreachable":
		return daemonUnreachable, nil
	case "not_probed":
		return daemonNotProbed, nil
	case "auth_required":
		return daemonAuthRequired, nil
	case "auth_failed":
		return daemonAuthFailed, nil
	default:
		return 0, fmt.Errorf("%w: connectedDaemon", ErrUnexpectedJSON)
	}
}

func validateStatusDetails(raw rawStatusJSON) error {
	for name, value := range map[string]string{
		"home": raw.Home.value, "listen": raw.Listen.value, "relay": raw.Relay.value,
		"logPath": raw.LogPath.value, "daemonNode": raw.DaemonNode.value,
		"cliNode": raw.CLINode.value, "cliVersion": raw.CLIVersion.value,
	} {
		if !validOpaqueValue(value) {
			return fmt.Errorf("%w: поле %s", ErrUnexpectedJSON, name)
		}
	}
	if !validPaseoVersion(raw.CLIVersion.value) {
		return fmt.Errorf("%w: поле cliVersion", ErrUnexpectedJSON)
	}
	if raw.DaemonVersion.value != nil && !validPaseoVersion(*raw.DaemonVersion.value) {
		return fmt.Errorf("%w: поле daemonVersion", ErrUnexpectedJSON)
	}
	if raw.Hostname.value != nil && !validOpaqueValue(*raw.Hostname.value) {
		return fmt.Errorf("%w: поле hostname", ErrUnexpectedJSON)
	}
	if raw.Owner.value != nil && !validOpaqueValue(*raw.Owner.value) {
		return fmt.Errorf("%w: поле owner", ErrUnexpectedJSON)
	}
	if raw.Hostname.value != nil && raw.Owner.value != nil &&
		!strings.HasSuffix(*raw.Owner.value, "@"+*raw.Hostname.value) {
		return fmt.Errorf("%w: поля owner и hostname противоречат друг другу", ErrUnexpectedJSON)
	}
	if raw.StartedAt.value != nil {
		if _, err := time.Parse(time.RFC3339Nano, *raw.StartedAt.value); err != nil {
			return fmt.Errorf("%w: поле startedAt", ErrUnexpectedJSON)
		}
	}
	if raw.PID.value != nil && *raw.PID.value <= 0 {
		return fmt.Errorf("%w: поле pid", ErrUnexpectedJSON)
	}
	for index, provider := range raw.Providers.value {
		if !validOpaqueValue(provider.Label.value) {
			return fmt.Errorf("%w: label provider %d", ErrUnexpectedJSON, index+1)
		}
		if provider.Source.present && provider.Source.value != "daemon" {
			return fmt.Errorf("%w: source provider %d", ErrUnexpectedJSON, index+1)
		}
	}
	return nil
}

func validPaseoVersion(value string) bool {
	return len(value) <= maxPaseoVersionLength && paseoVersionPattern.MatchString(value)
}

func validOpaqueValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validIdentifierValue(value string) bool {
	return validOpaqueValue(value) && strings.IndexFunc(value, unicode.IsSpace) < 0
}

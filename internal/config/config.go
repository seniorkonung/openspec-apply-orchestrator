package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const (
	FileName           = "openspec-apply-orchestrator.json"
	maximumConfigBytes = 1 << 20
)

var (
	ErrInvalidRoot    = errors.New("некорректный корень рабочего репозитория")
	ErrConfigNotFound = errors.New("конфигурация оркестратора не найдена")
	ErrReadConfig     = errors.New("не удалось прочитать конфигурацию оркестратора")
	ErrConfigTooLarge = errors.New("конфигурация оркестратора превысила предел размера")
	ErrInvalidJSON    = errors.New("конфигурация оркестратора содержит некорректный JSON")
	ErrUnknownField   = errors.New("неизвестное поле конфигурации")
	ErrMissingField   = errors.New("отсутствует обязательное поле конфигурации")
	ErrInvalidValue   = errors.New("некорректное значение конфигурации")
	ErrDuplicateField = errors.New("поле конфигурации повторяется")
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type FieldError struct {
	Path string
	Kind error
}

func (err *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", err.Kind, err.Path)
}

func (err *FieldError) Unwrap() error {
	return err.Kind
}

type RepositoryRoot struct {
	path string
}

func NewRepositoryRoot(path string) (RepositoryRoot, error) {
	canonical, err := canonicalExistingDirectory(path)
	if err != nil {
		return RepositoryRoot{}, fmt.Errorf("%w: %v", ErrInvalidRoot, err)
	}
	return RepositoryRoot{path: canonical}, nil
}

func (root RepositoryRoot) String() string {
	return root.path
}

type UntrustedAgentSettings struct {
	provider     string
	model        string
	reasoning    string
	hasReasoning bool
}

func (settings UntrustedAgentSettings) Provider() string {
	return settings.provider
}

func (settings UntrustedAgentSettings) Model() string {
	return settings.model
}

func (settings UntrustedAgentSettings) Reasoning() (string, bool) {
	return settings.reasoning, settings.hasReasoning
}

type InterventionChannelType string

const InterventionChannelTypeNtfy InterventionChannelType = "ntfy"

type NtfyPriority string

const (
	NtfyPriorityMin     NtfyPriority = "min"
	NtfyPriorityLow     NtfyPriority = "low"
	NtfyPriorityDefault NtfyPriority = "default"
	NtfyPriorityHigh    NtfyPriority = "high"
	NtfyPriorityMax     NtfyPriority = "max"
)

func (priority NtfyPriority) valid() bool {
	switch priority {
	case NtfyPriorityMin, NtfyPriorityLow, NtfyPriorityDefault, NtfyPriorityHigh, NtfyPriorityMax:
		return true
	default:
		return false
	}
}

type InterventionChannel struct {
	channelType InterventionChannelType
	url         string
	tokenEnv    string
	hasTokenEnv bool
	priority    NtfyPriority
}

func (channel InterventionChannel) Type() InterventionChannelType {
	return channel.channelType
}

func (channel InterventionChannel) URL() string {
	return channel.url
}

func (channel InterventionChannel) TokenEnvironment() (string, bool) {
	return channel.tokenEnv, channel.hasTokenEnv
}

func (channel InterventionChannel) Priority() NtfyPriority {
	return channel.priority
}

type Config struct {
	version           int
	commitPreparation UntrustedAgentSettings
	intervention      InterventionChannel
}

type Snapshot struct {
	document  string
	readError error
}

func (config Config) Version() int {
	return config.version
}

func (config Config) CommitPreparation() UntrustedAgentSettings {
	return config.commitPreparation
}

func (config Config) InterventionChannel() InterventionChannel {
	return config.intervention
}

func Read(root RepositoryRoot) (Config, error) {
	return ReadSnapshot(root).config()
}

func ReadSnapshot(root RepositoryRoot) Snapshot {
	document, err := readDocument(root)
	return Snapshot{document: string(document), readError: err}
}

func (snapshot Snapshot) CommitPreparation() (UntrustedAgentSettings, error) {
	_, root, err := snapshot.root()
	if err != nil {
		return UntrustedAgentSettings{}, err
	}
	return decodeCommitPreparation(root)
}

func (snapshot Snapshot) InterventionChannel() (InterventionChannel, error) {
	_, root, err := snapshot.root()
	if err != nil {
		return InterventionChannel{}, err
	}
	return decodeInterventionFromRoot(root)
}

func (snapshot Snapshot) config() (Config, error) {
	version, root, err := snapshot.root()
	if err != nil {
		return Config{}, err
	}
	commitPreparation, err := decodeCommitPreparation(root)
	if err != nil {
		return Config{}, err
	}
	intervention, err := decodeInterventionFromRoot(root)
	if err != nil {
		return Config{}, err
	}
	return Config{
		version:           version,
		commitPreparation: commitPreparation,
		intervention:      intervention,
	}, nil
}

func (snapshot Snapshot) root() (int, map[string]json.RawMessage, error) {
	if snapshot.readError != nil {
		return 0, nil, snapshot.readError
	}
	document := []byte(snapshot.document)
	if err := validateJSONDocument(document); err != nil {
		return 0, nil, err
	}
	root, err := decodeObject(document, "")
	if err != nil {
		return 0, nil, err
	}
	if err := validateObjectFields(root, "", []string{"version", "sessions", "notifications"}, []string{"version"}); err != nil {
		return 0, nil, err
	}
	version, err := decodeVersion(root["version"])
	if err != nil {
		return 0, nil, err
	}
	return version, root, nil
}

func readDocument(root RepositoryRoot) ([]byte, error) {
	validatedRoot, err := NewRepositoryRoot(root.path)
	if err != nil || validatedRoot != root {
		return nil, ErrInvalidRoot
	}
	path, err := resolveConfigPath(root.path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrConfigNotFound
		}
		return nil, fmt.Errorf("%w: %v", ErrReadConfig, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: получить сведения о файле: %v", ErrReadConfig, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: путь конфигурации не является обычным файлом", ErrReadConfig)
	}
	document, err := io.ReadAll(io.LimitReader(file, maximumConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReadConfig, err)
	}
	if len(document) > maximumConfigBytes {
		return nil, ErrConfigTooLarge
	}
	return document, nil
}

func resolveConfigPath(root string) (string, error) {
	path := filepath.Join(root, FileName)
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrConfigNotFound
		}
		return "", fmt.Errorf("%w: разрешить путь конфигурации: %v", ErrReadConfig, err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: получить абсолютный путь конфигурации: %v", ErrReadConfig, err)
	}
	canonical = filepath.Clean(canonical)
	if !pathContains(root, canonical) {
		return "", fmt.Errorf("%w: файл конфигурации находится вне корня репозитория", ErrReadConfig)
	}
	return canonical, nil
}

func decodeCommitPreparation(root map[string]json.RawMessage) (UntrustedAgentSettings, error) {
	rawSessions, present := root["sessions"]
	if !present {
		return UntrustedAgentSettings{}, fieldError("sessions", ErrMissingField)
	}
	sessions, err := decodeObject(rawSessions, "sessions")
	if err != nil {
		return UntrustedAgentSettings{}, err
	}
	if err := validateObjectFields(sessions, "sessions", []string{"commit-preparation"}, []string{"commit-preparation"}); err != nil {
		return UntrustedAgentSettings{}, err
	}
	return decodeAgentSettings(sessions["commit-preparation"])
}

func decodeInterventionFromRoot(root map[string]json.RawMessage) (InterventionChannel, error) {
	rawNotifications, present := root["notifications"]
	if !present {
		return InterventionChannel{}, fieldError("notifications.intervention", ErrMissingField)
	}
	return decodeInterventionChannel(rawNotifications)
}

func decodeVersion(raw []byte) (int, error) {
	value, err := decodeInteger(raw, "version")
	if err != nil || value != 1 {
		return 0, fieldError("version", ErrInvalidValue)
	}
	return value, nil
}

func decodeAgentSettings(raw []byte) (UntrustedAgentSettings, error) {
	const path = "sessions.commit-preparation"
	object, err := decodeObject(raw, path)
	if err != nil {
		return UntrustedAgentSettings{}, err
	}
	if err := validateObjectFields(object, path, []string{"provider", "model", "reasoning"}, []string{"provider", "model"}); err != nil {
		return UntrustedAgentSettings{}, err
	}
	provider, err := decodeIdentifier(object["provider"], path+".provider")
	if err != nil {
		return UntrustedAgentSettings{}, err
	}
	model, err := decodeIdentifier(object["model"], path+".model")
	if err != nil {
		return UntrustedAgentSettings{}, err
	}
	settings := UntrustedAgentSettings{provider: provider, model: model}
	if rawReasoning, present := object["reasoning"]; present {
		reasoning, err := decodeIdentifier(rawReasoning, path+".reasoning")
		if err != nil {
			return UntrustedAgentSettings{}, err
		}
		settings.reasoning = reasoning
		settings.hasReasoning = true
	}
	return settings, nil
}

func decodeInterventionChannel(raw []byte) (InterventionChannel, error) {
	const notificationsPath = "notifications"
	notifications, err := decodeObject(raw, notificationsPath)
	if err != nil {
		return InterventionChannel{}, err
	}
	if err := validateObjectFields(notifications, notificationsPath, []string{"intervention"}, []string{"intervention"}); err != nil {
		return InterventionChannel{}, err
	}

	const channelPath = notificationsPath + ".intervention"
	channel, err := decodeObject(notifications["intervention"], channelPath)
	if err != nil {
		return InterventionChannel{}, err
	}
	if err := validateObjectFields(channel, channelPath, []string{"type", "url", "tokenEnv", "priority"}, []string{"type", "url"}); err != nil {
		return InterventionChannel{}, err
	}
	channelType, err := decodeString(channel["type"], channelPath+".type")
	if err != nil || InterventionChannelType(channelType) != InterventionChannelTypeNtfy {
		return InterventionChannel{}, fieldError(channelPath+".type", ErrInvalidValue)
	}
	address, err := decodeString(channel["url"], channelPath+".url")
	if err != nil || !validNotificationURL(address) {
		return InterventionChannel{}, fieldError(channelPath+".url", ErrInvalidValue)
	}
	intervention := InterventionChannel{
		channelType: InterventionChannelTypeNtfy,
		url:         address,
		priority:    NtfyPriorityDefault,
	}
	if rawTokenEnvironment, present := channel["tokenEnv"]; present {
		tokenEnvironment, err := decodeString(rawTokenEnvironment, channelPath+".tokenEnv")
		if err != nil || !environmentNamePattern.MatchString(tokenEnvironment) {
			return InterventionChannel{}, fieldError(channelPath+".tokenEnv", ErrInvalidValue)
		}
		intervention.tokenEnv = tokenEnvironment
		intervention.hasTokenEnv = true
	}
	if rawPriority, present := channel["priority"]; present {
		priorityValue, err := decodeString(rawPriority, channelPath+".priority")
		priority := NtfyPriority(priorityValue)
		if err != nil || !priority.valid() {
			return InterventionChannel{}, fieldError(channelPath+".priority", ErrInvalidValue)
		}
		intervention.priority = priority
	}
	return intervention, nil
}

func validNotificationURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Path == "" || parsed.Path == "/" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		hostname := parsed.Hostname()
		if hostname == "localhost" {
			return true
		}
		address := net.ParseIP(hostname)
		return address != nil && address.IsLoopback()
	default:
		return false
	}
}

func decodeIdentifier(raw []byte, path string) (string, error) {
	value, err := decodeString(raw, path)
	if err != nil || value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fieldError(path, ErrInvalidValue)
	}
	return value, nil
}

func canonicalExistingDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("путь пуст")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("получить абсолютный путь: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("канонизировать путь: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("прочитать путь: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("путь не является каталогом")
	}
	return filepath.Clean(canonical), nil
}

func pathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

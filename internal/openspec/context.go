package openspec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	ErrInvalidSelection    = errors.New("некорректный выбор OpenSpec change")
	ErrInvalidClient       = errors.New("некорректный клиент OpenSpec")
	ErrEmptyOutput         = errors.New("команда OpenSpec вернула пустой вывод")
	ErrTruncatedJSON       = errors.New("команда OpenSpec вернула обрезанный JSON")
	ErrUnexpectedJSON      = errors.New("команда OpenSpec вернула неожиданный JSON")
	ErrChangeUnavailable   = errors.New("выбранный активный OpenSpec change недоступен")
	ErrInconsistentContext = errors.New("ответы OpenSpec содержат несогласованный контекст")
)

var storeIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Selection struct {
	changeName string
	storeID    string
}

func NewSelection(changeName, storeID string) (Selection, error) {
	if err := validateChangeName(changeName); err != nil {
		return Selection{}, err
	}
	if storeID != "" && !storeIDPattern.MatchString(storeID) {
		return Selection{}, fmt.Errorf("%w: идентификатор store", ErrInvalidSelection)
	}
	return Selection{changeName: changeName, storeID: storeID}, nil
}

func validateChangeName(value string) error {
	invalid := value == "" || value == "." || value == ".." || value == "archive" ||
		strings.HasPrefix(value, ".") || strings.ContainsAny(value, `/\`) ||
		strings.IndexByte(value, 0) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0
	if invalid {
		return fmt.Errorf("%w: имя change", ErrInvalidSelection)
	}
	return nil
}

type rootSource uint8

const (
	rootFromStore rootSource = iota + 1
	rootFromDeclaration
	rootFromGlobalDefault
	rootFromNearest
	rootImplicit
)

type ChangeContext struct {
	name             string
	schemaName       string
	planningHomeRoot string
	changesDirectory string
	changeRoot       string
	allowedEditRoots []string
	storeID          string
}

func (resolved ChangeContext) Name() string {
	return resolved.name
}

func (resolved ChangeContext) SchemaName() string {
	return resolved.schemaName
}

func (resolved ChangeContext) PlanningHomeRoot() string {
	return resolved.planningHomeRoot
}

func (resolved ChangeContext) ChangesDirectory() string {
	return resolved.changesDirectory
}

func (resolved ChangeContext) ChangeRoot() string {
	return resolved.changeRoot
}

func (resolved ChangeContext) AllowedEditRoots() []string {
	result := make([]string, len(resolved.allowedEditRoots))
	copy(result, resolved.allowedEditRoots)
	return result
}

func (resolved ChangeContext) StoreID() (string, bool) {
	return resolved.storeID, resolved.storeID != ""
}

type Client struct {
	runner *runner
}

func NewClient(workingDirectory string) (*Client, error) {
	runner, err := newRunner(workingDirectory, defaultRunnerConfig())
	if err != nil {
		return nil, err
	}
	return newClient(runner), nil
}

func newClient(runner *runner) *Client {
	return &Client{runner: runner}
}

func (client *Client) ResolveChange(ctx context.Context, selection Selection) (ChangeContext, error) {
	if client == nil || client.runner == nil {
		return ChangeContext{}, ErrInvalidClient
	}
	if err := validateSelection(selection); err != nil {
		return ChangeContext{}, err
	}

	contextArguments := []string{"context", "--json"}
	if selection.storeID != "" {
		contextArguments = append(contextArguments, "--store", selection.storeID)
	}
	contextResult, err := client.runner.run(ctx, command{name: "context", args: contextArguments})
	if err != nil {
		return ChangeContext{}, fmt.Errorf("прочитать рабочий контекст OpenSpec: %w", err)
	}
	contextDocument, err := decodeContext(contextResult.stdout)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("проверить рабочий контекст OpenSpec: %w", err)
	}

	statusArguments := []string{"status", "--change", selection.changeName, "--json"}
	if selection.storeID != "" {
		statusArguments = append(statusArguments, "--store", selection.storeID)
	}
	statusResult, err := client.runner.run(ctx, command{name: "status", args: statusArguments})
	if err != nil {
		if errors.Is(err, ErrCommandExit) && failureHasCode(statusResult.stdout, "change_error") {
			return ChangeContext{}, ErrChangeUnavailable
		}
		return ChangeContext{}, fmt.Errorf("прочитать статус OpenSpec change: %w", err)
	}
	statusDocument, err := decodeStatus(statusResult.stdout)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("проверить статус OpenSpec change: %w", err)
	}

	return buildChangeContext(selection, contextDocument, statusDocument)
}

func validateSelection(selection Selection) error {
	validated, err := NewSelection(selection.changeName, selection.storeID)
	if err != nil {
		return err
	}
	if validated != selection {
		return ErrInvalidSelection
	}
	return nil
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

type rawDiagnostic struct {
	Severity requiredValue[string] `json:"severity"`
	Code     requiredValue[string] `json:"code"`
	Message  requiredValue[string] `json:"message"`
	Target   optionalValue[string] `json:"target"`
	Fix      optionalValue[string] `json:"fix"`
}

type rawContextRoot struct {
	Path    requiredValue[string] `json:"path"`
	Source  requiredValue[string] `json:"source"`
	StoreID optionalValue[string] `json:"store_id"`
	Role    requiredValue[string] `json:"role"`
}

type rawContextMember struct {
	Role   requiredValue[string]          `json:"role"`
	ID     requiredValue[string]          `json:"id"`
	Path   optionalValue[string]          `json:"path"`
	Remote optionalValue[string]          `json:"remote"`
	Fetch  optionalValue[string]          `json:"fetch"`
	Status requiredValue[[]rawDiagnostic] `json:"status"`
}

type rawContextDocument struct {
	Root    requiredValue[rawContextRoot]     `json:"root"`
	Members requiredValue[[]rawContextMember] `json:"members"`
	Status  requiredValue[[]rawDiagnostic]    `json:"status"`
}

type rawStatusRoot struct {
	Path    requiredValue[string] `json:"path"`
	Source  requiredValue[string] `json:"source"`
	StoreID optionalValue[string] `json:"store_id"`
}

type rawPlanningHome struct {
	Kind          requiredValue[string] `json:"kind"`
	Root          requiredValue[string] `json:"root"`
	ChangesDir    requiredValue[string] `json:"changesDir"`
	DefaultSchema requiredValue[string] `json:"defaultSchema"`
}

type rawArtifactPath struct {
	OutputPath          requiredValue[string]   `json:"outputPath"`
	ResolvedOutputPath  requiredValue[string]   `json:"resolvedOutputPath"`
	ExistingOutputPaths requiredValue[[]string] `json:"existingOutputPaths"`
}

type rawLinkedContext struct {
	Name requiredValue[string] `json:"name"`
}

type rawActionContext struct {
	Mode                          requiredValue[string]             `json:"mode"`
	SourceOfTruth                 requiredValue[string]             `json:"sourceOfTruth"`
	PlanningArtifacts             requiredValue[[]string]           `json:"planningArtifacts"`
	LinkedContext                 requiredValue[[]rawLinkedContext] `json:"linkedContext"`
	AllowedEditRoots              requiredValue[[]string]           `json:"allowedEditRoots"`
	RequiresAffectedAreaSelection requiredValue[bool]               `json:"requiresAffectedAreaSelection"`
	Constraints                   requiredValue[[]string]           `json:"constraints"`
}

type rawArtifactStatus struct {
	ID          requiredValue[string]   `json:"id"`
	OutputPath  requiredValue[string]   `json:"outputPath"`
	Status      requiredValue[string]   `json:"status"`
	Requires    requiredValue[[]string] `json:"requires"`
	MissingDeps optionalValue[[]string] `json:"missingDeps"`
}

type rawStatusDocument struct {
	ChangeName         requiredValue[string]                     `json:"changeName"`
	SchemaName         requiredValue[string]                     `json:"schemaName"`
	PlanningHome       requiredValue[rawPlanningHome]            `json:"planningHome"`
	ChangeRoot         requiredValue[string]                     `json:"changeRoot"`
	ArtifactPaths      requiredValue[map[string]rawArtifactPath] `json:"artifactPaths"`
	IsPlanningComplete requiredValue[bool]                       `json:"isPlanningComplete"`
	IsComplete         requiredValue[bool]                       `json:"isComplete"`
	ApplyRequires      requiredValue[[]string]                   `json:"applyRequires"`
	NextSteps          requiredValue[[]string]                   `json:"nextSteps"`
	ActionContext      requiredValue[rawActionContext]           `json:"actionContext"`
	Artifacts          requiredValue[[]rawArtifactStatus]        `json:"artifacts"`
	Root               requiredValue[rawStatusRoot]              `json:"root"`
}

type rawFailureDocument struct {
	Status requiredValue[[]rawDiagnostic] `json:"status"`
}

func decodeContext(output []byte) (rawContextDocument, error) {
	var document rawContextDocument
	if err := decodeStrictJSON(output, &document); err != nil {
		return rawContextDocument{}, err
	}
	if !document.Root.present || !document.Members.present || !document.Status.present {
		return rawContextDocument{}, ErrUnexpectedJSON
	}
	if err := validateContextRoot(document.Root.value); err != nil {
		return rawContextDocument{}, err
	}
	for _, member := range document.Members.value {
		if !member.Role.present || member.Role.value != "referenced_store" || !member.ID.present ||
			!validOpaqueValue(member.ID.value) || !member.Status.present {
			return rawContextDocument{}, ErrUnexpectedJSON
		}
		if err := validateDiagnostics(member.Status.value); err != nil {
			return rawContextDocument{}, err
		}
	}
	if err := validateDiagnostics(document.Status.value); err != nil {
		return rawContextDocument{}, err
	}
	return document, nil
}

func validateContextRoot(root rawContextRoot) error {
	if !root.Path.present || !root.Source.present || !root.Role.present || root.Role.value != "openspec_root" {
		return ErrUnexpectedJSON
	}
	if _, err := parseRootSource(root.Source.value, root.StoreID); err != nil {
		return err
	}
	return nil
}

func decodeStatus(output []byte) (rawStatusDocument, error) {
	var document rawStatusDocument
	if err := decodeStrictJSON(output, &document); err != nil {
		return rawStatusDocument{}, err
	}
	if !document.ChangeName.present || !document.SchemaName.present || !document.PlanningHome.present ||
		!document.ChangeRoot.present || !document.ArtifactPaths.present || !document.IsPlanningComplete.present ||
		!document.IsComplete.present || !document.ApplyRequires.present || !document.NextSteps.present ||
		!document.ActionContext.present || !document.Artifacts.present || !document.Root.present {
		return rawStatusDocument{}, ErrUnexpectedJSON
	}
	if err := validateStatusShape(document); err != nil {
		return rawStatusDocument{}, err
	}
	return document, nil
}

func validateStatusShape(document rawStatusDocument) error {
	planningHome := document.PlanningHome.value
	if !planningHome.Kind.present || planningHome.Kind.value != "repo" || !planningHome.Root.present ||
		!planningHome.ChangesDir.present || !planningHome.DefaultSchema.present ||
		!validOpaqueValue(planningHome.DefaultSchema.value) {
		return ErrUnexpectedJSON
	}
	if !validOpaqueValue(document.ChangeName.value) || !validOpaqueValue(document.SchemaName.value) {
		return ErrUnexpectedJSON
	}
	if !document.Root.value.Path.present || !document.Root.value.Source.present {
		return ErrUnexpectedJSON
	}
	if _, err := parseRootSource(document.Root.value.Source.value, document.Root.value.StoreID); err != nil {
		return err
	}

	for id, artifactPath := range document.ArtifactPaths.value {
		if !validOpaqueValue(id) || !artifactPath.OutputPath.present || !artifactPath.ResolvedOutputPath.present ||
			!artifactPath.ExistingOutputPaths.present || !validOpaqueValue(artifactPath.OutputPath.value) ||
			!validOpaqueValue(artifactPath.ResolvedOutputPath.value) {
			return ErrUnexpectedJSON
		}
		for _, path := range artifactPath.ExistingOutputPaths.value {
			if !validOpaqueValue(path) {
				return ErrUnexpectedJSON
			}
		}
	}
	for _, artifact := range document.Artifacts.value {
		if !artifact.ID.present || !artifact.OutputPath.present || !artifact.Status.present || !artifact.Requires.present ||
			!validOpaqueValue(artifact.ID.value) || !validOpaqueValue(artifact.OutputPath.value) ||
			!oneOf(artifact.Status.value, "done", "skipped", "ready", "blocked") {
			return ErrUnexpectedJSON
		}
	}

	action := document.ActionContext.value
	if !action.Mode.present || action.Mode.value != "repo-local" || !action.SourceOfTruth.present ||
		action.SourceOfTruth.value != "repo" || !action.PlanningArtifacts.present || !action.LinkedContext.present ||
		!action.AllowedEditRoots.present || len(action.AllowedEditRoots.value) == 0 ||
		!action.RequiresAffectedAreaSelection.present || action.RequiresAffectedAreaSelection.value ||
		!action.Constraints.present {
		return ErrUnexpectedJSON
	}
	for _, linked := range action.LinkedContext.value {
		if !linked.Name.present || !validOpaqueValue(linked.Name.value) {
			return ErrUnexpectedJSON
		}
	}
	return nil
}

func failureHasCode(output []byte, expected string) bool {
	var document rawFailureDocument
	if decodeStrictJSON(output, &document) != nil || !document.Status.present {
		return false
	}
	if validateDiagnostics(document.Status.value) != nil {
		return false
	}
	for _, diagnostic := range document.Status.value {
		if diagnostic.Severity.value == "error" && diagnostic.Code.value == expected {
			return true
		}
	}
	return false
}

func buildChangeContext(
	selection Selection,
	contextDocument rawContextDocument,
	statusDocument rawStatusDocument,
) (ChangeContext, error) {
	contextRoot, err := canonicalExistingDirectory(contextDocument.Root.value.Path.value)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("%w: корень context", ErrInconsistentContext)
	}
	statusRoot, err := canonicalExistingDirectory(statusDocument.Root.value.Path.value)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("%w: корень status", ErrInconsistentContext)
	}
	planningHomeRoot, err := canonicalExistingDirectory(statusDocument.PlanningHome.value.Root.value)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("%w: planning home", ErrInconsistentContext)
	}
	changesDirectory, err := canonicalExistingDirectory(statusDocument.PlanningHome.value.ChangesDir.value)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("%w: каталог changes", ErrInconsistentContext)
	}
	changeRoot, err := canonicalExistingDirectory(statusDocument.ChangeRoot.value)
	if err != nil {
		return ChangeContext{}, fmt.Errorf("%w: корень change", ErrInconsistentContext)
	}

	contextSource, err := parseRootSource(
		contextDocument.Root.value.Source.value,
		contextDocument.Root.value.StoreID,
	)
	if err != nil {
		return ChangeContext{}, err
	}
	statusSource, err := parseRootSource(statusDocument.Root.value.Source.value, statusDocument.Root.value.StoreID)
	if err != nil {
		return ChangeContext{}, err
	}
	contextStoreID := optionalString(contextDocument.Root.value.StoreID)
	statusStoreID := optionalString(statusDocument.Root.value.StoreID)

	if contextRoot != statusRoot || contextRoot != planningHomeRoot || contextSource != statusSource ||
		contextStoreID != statusStoreID || statusDocument.ChangeName.value != selection.changeName ||
		filepath.Dir(changeRoot) != changesDirectory || filepath.Base(changeRoot) != selection.changeName ||
		!pathWithin(planningHomeRoot, changesDirectory) {
		return ChangeContext{}, ErrInconsistentContext
	}
	if selection.storeID != "" &&
		(contextSource != rootFromStore || contextStoreID != selection.storeID) {
		return ChangeContext{}, ErrInconsistentContext
	}

	allowedRoots := make([]string, 0, len(statusDocument.ActionContext.value.AllowedEditRoots.value))
	seenRoots := make(map[string]struct{}, len(statusDocument.ActionContext.value.AllowedEditRoots.value))
	for _, rawRoot := range statusDocument.ActionContext.value.AllowedEditRoots.value {
		root, err := canonicalExistingDirectory(rawRoot)
		if err != nil {
			return ChangeContext{}, fmt.Errorf("%w: разрешённый корень", ErrInconsistentContext)
		}
		if _, exists := seenRoots[root]; exists {
			return ChangeContext{}, fmt.Errorf("%w: повтор разрешённого корня", ErrInconsistentContext)
		}
		seenRoots[root] = struct{}{}
		allowedRoots = append(allowedRoots, root)
	}
	if _, allowed := seenRoots[planningHomeRoot]; !allowed {
		return ChangeContext{}, fmt.Errorf("%w: planning home отсутствует в области действия", ErrInconsistentContext)
	}

	return ChangeContext{
		name:             selection.changeName,
		schemaName:       statusDocument.SchemaName.value,
		planningHomeRoot: planningHomeRoot,
		changesDirectory: changesDirectory,
		changeRoot:       changeRoot,
		allowedEditRoots: allowedRoots,
		storeID:          contextStoreID,
	}, nil
}

func parseRootSource(value string, storeID optionalValue[string]) (rootSource, error) {
	if storeID.present && !storeIDPattern.MatchString(storeID.value) {
		return 0, ErrUnexpectedJSON
	}
	switch value {
	case "store":
		if !storeID.present {
			return 0, ErrUnexpectedJSON
		}
		return rootFromStore, nil
	case "declared":
		if !storeID.present {
			return 0, ErrUnexpectedJSON
		}
		return rootFromDeclaration, nil
	case "global_default":
		if !storeID.present {
			return 0, ErrUnexpectedJSON
		}
		return rootFromGlobalDefault, nil
	case "nearest":
		if storeID.present {
			return 0, ErrUnexpectedJSON
		}
		return rootFromNearest, nil
	case "implicit":
		if storeID.present {
			return 0, ErrUnexpectedJSON
		}
		return rootImplicit, nil
	default:
		return 0, ErrUnexpectedJSON
	}
}

func optionalString(value optionalValue[string]) string {
	if !value.present {
		return ""
	}
	return value.value
}

func validateDiagnostics(diagnostics []rawDiagnostic) error {
	for _, diagnostic := range diagnostics {
		if !diagnostic.Severity.present || !oneOf(diagnostic.Severity.value, "error", "warning", "info") ||
			!diagnostic.Code.present || !validOpaqueValue(diagnostic.Code.value) ||
			!diagnostic.Message.present || !validDiagnosticText(diagnostic.Message.value) {
			return ErrUnexpectedJSON
		}
		if diagnostic.Target.present && !validDiagnosticText(diagnostic.Target.value) {
			return ErrUnexpectedJSON
		}
		if diagnostic.Fix.present && !validDiagnosticText(diagnostic.Fix.value) {
			return ErrUnexpectedJSON
		}
	}
	return nil
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

func validOpaqueValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validDiagnosticText(value string) bool {
	return strings.TrimSpace(value) != ""
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

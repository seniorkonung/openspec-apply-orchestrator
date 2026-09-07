//go:build paseo_integration

package testpaseo

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const providerVersion = "oa-test-provider 1.0.0"

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func RunProvider(input io.Reader, output io.Writer, args []string) error {
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(output, providerVersion)
		return err
	}
	if len(args) != 0 {
		return fmt.Errorf("неизвестные аргументы тестового провайдера: %q", args)
	}

	provider := &providerServer{output: output}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return fmt.Errorf("прочитать JSON-RPC: %w", err)
		}
		if message.Method == "" {
			continue
		}
		if err := provider.handle(message); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("прочитать stdin: %w", err)
	}
	return nil
}

type providerServer struct {
	output  io.Writer
	session int
}

func (provider *providerServer) handle(message rpcMessage) error {
	switch message.Method {
	case "initialize":
		return provider.respond(message.ID, map[string]any{
			"protocolVersion": 1,
			"agentCapabilities": map[string]any{
				"loadSession": false,
			},
			"agentInfo": map[string]string{
				"name": "openspec-apply-integration", "version": "1.0.0",
			},
		})
	case "session/new":
		provider.session++
		return provider.respond(message.ID, map[string]string{
			"sessionId": fmt.Sprintf("oa-integration-%d-%d", os.Getpid(), provider.session),
		})
	case "session/prompt":
		sessionID, prompt, err := decodePrompt(message.Params)
		if err != nil {
			return err
		}
		if err := recordPrompt(prompt); err != nil {
			return err
		}
		return provider.runPrompt(message.ID, sessionID)
	case "session/cancel":
		return nil
	default:
		return provider.respondError(message.ID, -32601, "метод не поддерживается тестовым провайдером")
	}
}

func (provider *providerServer) runPrompt(id json.RawMessage, sessionID string) error {
	permissionRequested := false
	for {
		behavior, err := readBehavior()
		if err != nil {
			return err
		}
		switch behavior {
		case BehaviorWorking:
			time.Sleep(50 * time.Millisecond)
		case BehaviorFinish:
			return provider.respond(id, map[string]string{"stopReason": "end_turn"})
		case BehaviorPermission:
			if !permissionRequested {
				permissionRequested = true
				if err := provider.requestPermission(sessionID); err != nil {
					return err
				}
			}
			time.Sleep(50 * time.Millisecond)
		case BehaviorError:
			return provider.respondError(id, -32000, "управляемая ошибка тестового провайдера")
		default:
			return fmt.Errorf("неизвестное поведение тестового провайдера: %q", behavior)
		}
	}
}

func (provider *providerServer) requestPermission(sessionID string) error {
	return json.NewEncoder(provider.output).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      1000,
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": sessionID,
			"toolCall": map[string]any{
				"toolCallId": "oa-integration-tool",
				"title":      "Проверка ожидания разрешения",
				"kind":       "edit",
				"status":     "pending",
			},
			"options": []map[string]string{{
				"kind": "allow_once", "name": "Разрешить", "optionId": "allow",
			}},
		},
	})
}

func readBehavior() (Behavior, error) {
	behavior, err := os.ReadFile(os.Getenv(controlEnvironment))
	if err != nil {
		return "", fmt.Errorf("прочитать управление провайдером: %w", err)
	}
	return Behavior(strings.TrimSpace(string(behavior))), nil
}

func (provider *providerServer) respond(id json.RawMessage, result any) error {
	return json.NewEncoder(provider.output).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "result": result,
	})
}

func (provider *providerServer) respondError(id json.RawMessage, code int, message string) error {
	return json.NewEncoder(provider.output).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	})
}

func decodePrompt(raw json.RawMessage) (string, string, error) {
	var params struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", "", fmt.Errorf("прочитать поручение: %w", err)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		return "", "", errors.New("поручение не содержит ID сессии")
	}
	var prompt strings.Builder
	for _, block := range params.Prompt {
		if block.Type == "text" {
			prompt.WriteString(block.Text)
		}
	}
	if prompt.Len() == 0 {
		return "", "", errors.New("поручение не содержит текста")
	}
	return params.SessionID, prompt.String(), nil
}

func recordPrompt(prompt string) error {
	path := os.Getenv(recordEnvironment)
	if path == "" {
		return errors.New("не задан путь журнала тестового провайдера")
	}
	record, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return fmt.Errorf("собрать запись поручения: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("открыть журнал тестового провайдера: %w", err)
	}
	defer file.Close()
	record = append(record, '\n')
	if _, err := file.Write(record); err != nil {
		return fmt.Errorf("записать поручение: %w", err)
	}
	return nil
}

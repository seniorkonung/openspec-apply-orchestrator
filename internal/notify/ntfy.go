package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
)

const (
	defaultNtfyDeliveryTimeout = 10 * time.Second
	maximumNtfyResponseBytes   = 64 << 10
)

var ErrInvalidNtfyChannel = errors.New("некорректный канал ntfy")

type ntfyDependencies struct {
	transport         http.RoundTripper
	lookupEnvironment func(string) (string, bool)
	timeout           time.Duration
	maximumResponse   int64
}

type ntfyDeliverer struct {
	address           string
	tokenEnvironment  string
	hasToken          bool
	lookupEnvironment func(string) (string, bool)
	client            http.Client
	maximumResponse   int64
}

// NewNtfy создаёт адаптер для проверенного канала из снимка конфигурации запуска.
func NewNtfy(channel config.InterventionChannel) (Deliverer, error) {
	return newNtfy(channel, ntfyDependencies{
		transport:         http.DefaultTransport,
		lookupEnvironment: os.LookupEnv,
		timeout:           defaultNtfyDeliveryTimeout,
		maximumResponse:   maximumNtfyResponseBytes,
	})
}

func newNtfy(channel config.InterventionChannel, dependencies ntfyDependencies) (*ntfyDeliverer, error) {
	if channel.Type() != config.InterventionChannelTypeNtfy || channel.URL() == "" ||
		dependencies.transport == nil || dependencies.lookupEnvironment == nil ||
		dependencies.timeout <= 0 || dependencies.maximumResponse <= 0 {
		return nil, ErrInvalidNtfyChannel
	}
	tokenEnvironment, hasToken := channel.TokenEnvironment()
	return &ntfyDeliverer{
		address:           channel.URL(),
		tokenEnvironment:  tokenEnvironment,
		hasToken:          hasToken,
		lookupEnvironment: dependencies.lookupEnvironment,
		client: http.Client{
			Transport: dependencies.transport,
			Timeout:   dependencies.timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		maximumResponse: dependencies.maximumResponse,
	}, nil
}

func (deliverer *ntfyDeliverer) Deliver(ctx context.Context, event Intervention) *DeliveryError {
	if deliverer == nil || ctx == nil || event == nil {
		return NewDeliveryError()
	}

	token := ""
	if deliverer.hasToken {
		var present bool
		token, present = deliverer.lookupEnvironment(deliverer.tokenEnvironment)
		if !present || token == "" {
			return NewDeliveryError()
		}
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		deliverer.address,
		strings.NewReader(ntfyMessage(event)),
	)
	if err != nil {
		return NewDeliveryError()
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.Header.Set("Click", event.SessionLink().String())
	if deliverer.hasToken {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := deliverer.client.Do(request)
	if err != nil {
		return NewDeliveryError()
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, deliverer.maximumResponse+1))
	if err != nil || int64(len(body)) > deliverer.maximumResponse ||
		response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return NewDeliveryError()
	}
	return nil
}

func ntfyMessage(event Intervention) string {
	return fmt.Sprintf(
		"OpenSpec change %s требует участия: %s Сессия Paseo: %s",
		event.Change(),
		event.Message(),
		event.SessionID().String(),
	)
}

var _ Deliverer = (*ntfyDeliverer)(nil)

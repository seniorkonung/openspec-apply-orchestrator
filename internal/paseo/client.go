package paseo

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

type ServerID struct {
	value string
}

func (id ServerID) String() string {
	return id.value
}

type CompatibleEnvironment struct {
	value paseocli.CompatibleEnvironment
}

func (environment CompatibleEnvironment) ServerID() ServerID {
	return ServerID{value: environment.value.ServerID()}
}

type Client struct {
	adapter       *paseocli.Adapter
	expectedOwner string
}

func NewClient() (*Client, error) {
	adapter, err := paseocli.New()
	if err != nil {
		return nil, err
	}
	owner, err := currentDaemonOwner()
	if err != nil {
		return nil, err
	}
	return newClient(adapter, owner), nil
}

func newClient(adapter *paseocli.Adapter, expectedOwner string) *Client {
	return &Client{adapter: adapter, expectedOwner: expectedOwner}
}

func (client *Client) CheckCompatibility(ctx context.Context) (CompatibleEnvironment, error) {
	environment, err := client.adapter.CheckEnvironment(ctx, client.expectedOwner)
	if err != nil {
		return CompatibleEnvironment{}, err
	}
	return CompatibleEnvironment{value: environment}, nil
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

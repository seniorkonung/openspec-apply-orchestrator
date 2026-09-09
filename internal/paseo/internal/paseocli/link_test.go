package paseocli

import "testing"

func TestСовместимаяСредаСтроитЭкранированнуюСсылкуСессии(t *testing.T) {
	environment := CompatibleEnvironment{
		serverID: "srv_test123",
		contract: ActiveContract(),
	}

	if got := environment.SessionLink("session/one"); got != "paseo://h/srv_test123/agent/session%2Fone" {
		t.Fatalf("неожиданная ссылка сессии: %q", got)
	}
}

package paseocli

import "net/url"

// SessionLink строит ссылку активного контракта только для проверенной среды
// и допустимого полного ID сессии.
func (environment CompatibleEnvironment) SessionLink(sessionID string) string {
	if !environment.IsCompatible() || !validIdentifierValue(sessionID) {
		return ""
	}
	return "paseo://h/" + url.PathEscape(environment.serverID) + "/agent/" + url.PathEscape(sessionID)
}

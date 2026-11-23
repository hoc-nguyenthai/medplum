package domain

import (
	"strings"

	"trivita-fhir/pkg/fhir"
)

// CLIClientID là id của client dựng sẵn cho công cụ dòng lệnh (oauth/clients.ts).
const CLIClientID = "medplum-cli"

// StandardClients trả các client dựng sẵn: client CLI (nếu bật) và các client cấu hình thêm. Client dựng sẵn được tra
// trước khi tìm trong kho.
func StandardClients(cliEnabled bool, extra []fhir.ClientApplication) []fhir.ClientApplication {
	var out []fhir.ClientApplication
	if cliEnabled {
		yes := true
		out = append(out, fhir.ClientApplication{
			ResourceType: "ClientApplication", ID: CLIClientID, Name: "Medplum CLI",
			RedirectUris: []string{"http://localhost:9615"}, PkceOptional: &yes,
		})
	}
	return append(out, extra...)
}

// RedirectURIs gộp `redirectUri` (cũ) và `redirectUris`.
func RedirectURIs(c *fhir.ClientApplication) []string {
	var uris []string
	if c.RedirectUri != "" {
		uris = append(uris, c.RedirectUri)
	}
	return append(uris, c.RedirectUris...)
}

// MatchRedirectURI so khớp CHÍNH XÁC (không khớp một phần) và trả URI đã đăng ký, hoặc "" nếu không khớp.
func MatchRedirectURI(c *fhir.ClientApplication, requested string) string {
	for _, u := range RedirectURIs(c) {
		if u == requested {
			return u
		}
	}
	return ""
}

// IncludeRefreshToken: có kèm refresh token khi cờ `remember` (cũ), scope có `offline` (cũ) hoặc `offline_access`, hoặc client
// khai grantType `refresh_token` (oauth/utils.ts:1001).
func IncludeRefreshToken(req LoginRequest, client *fhir.ClientApplication) bool {
	if req.Remember {
		return true
	}
	terms := strings.Split(req.Scope, " ")
	has := func(s string) bool {
		for _, t := range terms {
			if t == s {
				return true
			}
		}
		return false
	}
	if has("offline") {
		return true
	}
	if client != nil {
		for _, g := range client.GrantType {
			if g == "refresh_token" {
				return true
			}
		}
	}
	return has("offline_access")
}

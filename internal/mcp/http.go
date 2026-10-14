package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/api"
)

// Prefix is where the server is conventionally mounted.
const Prefix = "/mcp"

// principalKey is the key of the verified principal in auth.TokenInfo.Extra.
const principalKey = "trica.principal"

// Handler returns the streamable HTTP handler to mount (under Prefix, for
// instance). Every request carries "Authorization: Bearer <credential>" and is
// authenticated afresh by the Authorizer: the handler is stateless (no MCP
// session survives a request), so a revoked credential stops working at once
// and nothing about a caller is remembered between calls.
func (s *Server) Handler() http.Handler {
	h := gosdk.NewStreamableHTTPHandler(s.serverFor, &gosdk.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		Logger:                     s.log,
		DisableLocalhostProtection: s.cfg.DisableLocalhostProtection,
	})
	// auth.RequireBearerToken refuses a request without a Bearer credential
	// (401) and puts what the verifier returns in the request context, from
	// where tools read it (req.Extra.TokenInfo).
	guarded := auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("WWW-Authenticate", "Bearer")
		guarded.ServeHTTP(w, r)
	})
}

// verify asks the Authorizer who the request is from. The bearer string is not
// interpreted here: the Authorizer reads the whole request, as it does for the
// REST API.
func (s *Server) verify(_ context.Context, _ string, r *http.Request) (*auth.TokenInfo, error) {
	p, err := s.auth.Principal(r)
	if err != nil {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: p.Subject, Extra: map[string]any{principalKey: p}}, nil
}

// serverFor picks the MCP server for a request: the one with the write tool
// only when the credential holds the propose action.
func (s *Server) serverFor(r *http.Request) *gosdk.Server {
	ti := auth.TokenInfoFromContext(r.Context())
	if ti == nil {
		return nil
	}
	p, ok := ti.Extra[principalKey].(api.Principal)
	if !ok {
		return nil
	}
	if s.auth.Allow(p, api.ActionPropose, "", "") {
		return s.readWrite
	}
	return s.readOnly
}

// httpPrincipal reads the verified principal of an HTTP tool call.
func httpPrincipal(req *gosdk.CallToolRequest) (api.Principal, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return api.Principal{}, errors.New("no verified credential")
	}
	p, ok := req.Extra.TokenInfo.Extra[principalKey].(api.Principal)
	if !ok {
		return api.Principal{}, errors.New("no verified credential")
	}
	return p, nil
}

// maxArgsAudited bounds the arguments kept in an audit record.
const maxArgsAudited = 400

// summarizeArgs is what the audit trail keeps of a call's arguments: the
// arguments themselves, cut, except for propose_change, whose batch may hold
// values nobody asked the audit log to keep: only its size is recorded.
func summarizeArgs(tool string, args any) any {
	if tool == "propose_change" {
		if in, ok := args.(ProposeInput); ok {
			return map[string]any{"objects": len(in.Objects), "relations": len(in.Relations), "unrelations": len(in.Unrelations)}
		}
		return nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	if len(raw) > maxArgsAudited {
		return string(raw[:maxArgsAudited]) + "..."
	}
	return string(raw)
}

// Package wire is the contract between the transport client
// (internal/transport/uplink) and the transport server (internal/transport):
// the endpoints and the JSON bodies of enrolment and renewal
// (docs/adr/0008-transport.md). It depends on nothing, so that the agent,
// which links only the client, does not link the server's PostgreSQL
// registry (#64).
package wire

// Endpoints.
const (
	PathEnrol   = "/v1/enrol"
	PathRenew   = "/v1/renew"
	PathBatches = "/v1/batches"
)

// EnrolRequest trades an enrolment token and a CSR for a client certificate.
// Binding is the binding the client asks for: required with a class token
// (the token grants a prefix, the client names itself below it, ADR 0035);
// with a one-time token, empty or the token's own binding.
type EnrolRequest struct {
	Token   string `json:"token"`
	CSR     string `json:"csr"`
	Binding string `json:"binding,omitempty"`
}

// RenewRequest asks for a new certificate of the same binding; the caller
// authenticates with the certificate it holds.
type RenewRequest struct {
	CSR string `json:"csr"`
}

// CertResponse answers both: the issued certificate and the CA, in PEM.
type CertResponse struct {
	Certificate string `json:"certificate"`
	CA          string `json:"ca"`
}

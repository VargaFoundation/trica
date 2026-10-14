package api

// Me is who the caller is and what it may do. The UI reads it to decide which
// controls to offer; the server still checks every call, so a stale or
// forged answer cannot grant anything.
type Me struct {
	Subject string        `json:"subject"`
	Kind    PrincipalKind `json:"kind"`
	// Source is the source a token is bound to; empty for a user.
	Source string `json:"source,omitempty"`
	// Actions are the actions the caller holds globally (the first gate of
	// every endpoint). A grant limited to some types or sites can still be
	// refused on a particular object.
	Actions []string `json:"actions"`
}

func (s *Server) hMe(c *call) error {
	out := Me{Subject: c.p.Subject, Kind: c.p.Kind, Actions: []string{}}
	if c.p.Kind == KindToken {
		out.Source = c.p.Source
	}
	for _, a := range []string{ActionRead, ActionPropose, ActionApprove, ActionAdmin} {
		if s.auth.Allow(c.p, a, "", "") {
			out.Actions = append(out.Actions, a)
		}
	}
	return c.json(200, out)
}

package pki

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// bindingPrefix is the opaque part of the URI SAN that carries a binding:
// urn:trica:source:<binding>.
const bindingPrefix = "trica:source:"

// maxBindingLen bounds a binding; it ends up in a certificate and in logs.
const maxBindingLen = 128

// bindingRE: two or more segments separated by ":", each made of letters,
// digits, ".", "_" or "-". Two segments at least ("agent:host-01"): a one
// segment binding ("agent") would be a wildcard over every agent.
var bindingRE = regexp.MustCompile(`^[A-Za-z0-9._-]+(:[A-Za-z0-9._-]+)+$`)

// ErrNoBinding is returned when a certificate carries no source binding.
var ErrNoBinding = errors.New("pki: certificate carries no source binding")

// ValidBinding checks that s is usable as a source binding.
func ValidBinding(s string) error {
	if len(s) > maxBindingLen {
		return fmt.Errorf("pki: binding longer than %d bytes", maxBindingLen)
	}
	if !bindingRE.MatchString(s) {
		return fmt.Errorf("pki: invalid binding %q (want kind:name, e.g. agent:host-01)", s)
	}
	return nil
}

// Allows reports whether a client bound to binding may report as source: the
// source is the binding itself, or extends it after a ":" separator. A client
// bound to "agent:host-01" may report as "agent:host-01" and
// "agent:host-01:docker", never as "agent:host-010" or "agent:host-02".
func Allows(binding, source string) bool {
	if binding == "" {
		return false
	}
	return source == binding || strings.HasPrefix(source, binding+":")
}

func bindingURI(binding string) *url.URL {
	return &url.URL{Scheme: "urn", Opaque: bindingPrefix + binding}
}

// BindingFromCert returns the source binding carried by the URI SAN of cert.
// The CN is informative only (it repeats the binding for humans) and is never
// used to authorise anything.
func BindingFromCert(cert *x509.Certificate) (string, error) {
	var found string
	for _, u := range cert.URIs {
		if u.Scheme != "urn" || !strings.HasPrefix(u.Opaque, bindingPrefix) {
			continue
		}
		if found != "" {
			return "", errors.New("pki: certificate carries several source bindings")
		}
		found = strings.TrimPrefix(u.Opaque, bindingPrefix)
	}
	if found == "" {
		return "", ErrNoBinding
	}
	if err := ValidBinding(found); err != nil {
		return "", err
	}
	return found, nil
}

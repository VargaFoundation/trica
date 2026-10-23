package theme

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FetchTimeout bounds one download.
const FetchTimeout = 30 * time.Second

// ErrDigest means the bytes served are not the ones the configuration pins.
var ErrDigest = errors.New("theme: the downloaded archive does not match the configured sha256")

// ValidateURL refuses what a theme must not be fetched from: anything but
// https, and an address that carries credentials.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("not a URL: %w", err)
	case u.Scheme != "https":
		return errors.New("the URL must be https")
	case u.Host == "":
		return errors.New("the URL has no host")
	case u.User != nil:
		return errors.New("the URL must not carry credentials")
	}
	return nil
}

// Fetch downloads an archive from an https URL and checks it against the
// pinned digest before anything reads it: a server that answers with other
// bytes (a compromised bucket, a proxy page) changes nothing. client may be
// nil; redirects to anything but https are refused.
func Fetch(ctx context.Context, client *http.Client, rawURL, wantSHA string) ([]byte, error) {
	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}
	c := http.Client{Timeout: FetchTimeout}
	if client != nil {
		c = *client
		if c.Timeout == 0 {
			c.Timeout = FetchTimeout
		}
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("redirect to a URL that is not https")
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		// The error text of net/http repeats the URL; the operator knows it.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: the server answered %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if len(b) > MaxArchiveBytes {
		return nil, refuse("the download is over the %d MiB limit", MaxArchiveBytes>>20)
	}
	sum := sha256.Sum256(b)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), wantSHA) {
		return nil, ErrDigest
	}
	return b, nil
}

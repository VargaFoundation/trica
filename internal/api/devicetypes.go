package api

import (
	"net/http"
	"strconv"
	"strings"
)

// DeviceImage is one face illustration of a device model.
type DeviceImage struct {
	ContentType string
	// ETag is a strong validator, quoted.
	ETag string
	Data []byte
	// Layout says where the parts sit on the image, when the library has it
	// (ADR 0047).
	Layout *FaceLayout
}

// FaceLayout is where the parts sit on a face image, in its own units: the
// image is Width x Height of them (millimetres for the project's drawings).
type FaceLayout struct {
	Width, Height float64
	// Origin is trica for the project's drawings, operator for a layout from
	// devicetypes.extra_dir.
	Origin string
	Spots  []FaceSpot
}

// DeviceTypeImages finds the face images of the device-type library
// (internal/devicetypes, ADR 0042). face is "front" or "rear".
type DeviceTypeImages interface {
	DeviceTypeImage(slug, face string) (DeviceImage, bool)
}

// ImageMaxAge is how long a browser may reuse an image without asking again.
// The library changes with a new binary or a restart with another
// devicetypes.extra_dir, so an hour is short enough; after that the ETag
// makes the question cheap.
const ImageMaxAge = 3600

// imagePath is the API path of the image of a face of a model (relative to
// the API root, as the UI calls it).
func imagePath(slug, face string) string { return "/device-types/" + slug + "/" + face }

// faceImage is the API path of the image, when the library has one.
func (s *Server) faceImage(slug, face string) string {
	if s.images == nil || slug == "" {
		return ""
	}
	if _, ok := s.images.DeviceTypeImage(slug, face); !ok {
		return ""
	}
	return Prefix + imagePath(slug, face)
}

// hDeviceTypeImage serves the image of one face of a model. The bytes are
// the library's (or the operator's directory's), not inventory data: any
// caller who may read gets them. They are cached privately (the request
// carries a credential) and revalidated by ETag.
func (s *Server) hDeviceTypeImage(c *call) error {
	slug, face := c.r.PathValue("slug"), c.r.PathValue("face")
	if face != "front" && face != "rear" {
		return badRequest("face must be front or rear")
	}
	if s.images == nil {
		return notFound("no image for %s", slug)
	}
	img, ok := s.images.DeviceTypeImage(slug, face)
	if !ok {
		return notFound("no %s image for %s", face, slug)
	}
	h := c.w.Header()
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(ImageMaxAge))
	h.Set("ETag", img.ETag)
	// Nothing in an image is to be run; a browser that opens it directly
	// gets a document that can do nothing.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if etagMatches(c.r.Header.Get("If-None-Match"), img.ETag) {
		c.w.WriteHeader(http.StatusNotModified)
		return nil
	}
	h.Set("Content-Type", img.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(img.Data)))
	c.w.WriteHeader(http.StatusOK)
	_, _ = c.w.Write(img.Data)
	return nil
}

// etagMatches reports whether an If-None-Match header names etag (weak
// comparison, RFC 9110 section 13.1.2).
func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}

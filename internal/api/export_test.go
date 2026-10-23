package api

import "context"

// RouteKeys lists the routes the server serves as "METHOD /path" (the path
// is relative to Prefix), for tests that compare the table with the document.
func (s *Server) RouteKeys() []string {
	keys := make([]string, 0, len(s.routes))
	for _, rt := range s.routes {
		keys = append(keys, rt.Method+" "+rt.Path)
	}
	return keys
}

// Bucket exposes bucket to the tests.
var Bucket = bucket

// SeriesAttributes exposes seriesAttributes to the tests.
var SeriesAttributes = seriesAttributes

// SiteIDs exposes the SQL rights walk to the map's AuthSite equivalence test.
func (s *Server) SiteIDs(ctx context.Context, ids []string) (map[string]string, error) {
	return s.siteIDs(ctx, ids)
}

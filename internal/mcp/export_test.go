package mcp

// VersionKey exposes versionKey to the external tests, which compare it with the SQL function the search uses.
var VersionKey = versionKey

// SetForecastScan lowers how many objects forecast_threshold reads per page
// and in all, so that the tests can cross the bound with a handful of drives.
// Call it before the server is used.
func SetForecastScan(s *Server, page, bound int) { s.scanPage, s.scanMax = page, bound }

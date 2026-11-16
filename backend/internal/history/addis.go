package history

import "time"

// addisLocation renders receipt timestamps in Ethiopia's civil time (PRD §12.1,
// mirrors internal/limit). Ethiopia has no DST, so the FixedZone fallback is
// exact when a host ships no zoneinfo.
var addisLocation = mustLoadLocation("Africa/Addis_Ababa")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("EAT", 3*60*60)
	}
	return loc
}

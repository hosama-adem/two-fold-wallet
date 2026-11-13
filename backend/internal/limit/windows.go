package limit

import "time"

// addisLocation is Ethiopia's civil calendar for daily/monthly windows (PRD §12.1).
var addisLocation = mustLoadLocation("Africa/Addis_Ababa")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Tests and Alpine images ship the zoneinfo; fall back rather than panic
		// at import time if a host is missing it.
		return time.FixedZone("EAT", 3*60*60)
	}
	return loc
}

// dayStartAddis returns 00:00:00 on the Addis Ababa calendar day containing at.
func dayStartAddis(at time.Time) time.Time {
	local := at.In(addisLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, addisLocation)
}

// monthStartAddis returns 00:00:00 on the first Addis Ababa day of the month containing at.
func monthStartAddis(at time.Time) time.Time {
	local := at.In(addisLocation)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, addisLocation)
}

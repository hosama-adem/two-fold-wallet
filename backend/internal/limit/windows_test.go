package limit

import (
	"testing"
	"time"
)

func TestAddisWindows(t *testing.T) {
	t.Parallel()

	// 2026-11-13 01:30 UTC = 04:30 Addis (UTC+3) — same calendar day.
	at := time.Date(2026, 11, 13, 1, 30, 0, 0, time.UTC)
	day := dayStartAddis(at)
	if day.Year() != 2026 || day.Month() != time.November || day.Day() != 13 {
		t.Fatalf("day=%v", day)
	}
	if day.Hour() != 0 || day.Location().String() != "Africa/Addis_Ababa" && day.Location().String() != "EAT" {
		// FixedZone fallback names itself EAT; tzdata names Africa/Addis_Ababa.
		if day.Hour() != 0 {
			t.Fatalf("day hour=%d loc=%s", day.Hour(), day.Location())
		}
	}

	month := monthStartAddis(at)
	if month.Day() != 1 || month.Month() != time.November {
		t.Fatalf("month=%v", month)
	}

	// Just before midnight Addis: 20:30 UTC = 23:30 Addis on the 12th.
	eve := time.Date(2026, 11, 12, 20, 30, 0, 0, time.UTC)
	if d := dayStartAddis(eve); d.Day() != 12 {
		t.Fatalf("eve day=%v want 12", d)
	}
	// 21:00 UTC = 00:00 Addis on the 13th.
	midnight := time.Date(2026, 11, 12, 21, 0, 0, 0, time.UTC)
	if d := dayStartAddis(midnight); d.Day() != 13 {
		t.Fatalf("midnight day=%v want 13", d)
	}
}

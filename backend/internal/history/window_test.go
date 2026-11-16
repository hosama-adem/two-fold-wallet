package history

import (
	"strings"
	"testing"
	"time"
)

func TestResolveExportWindow(t *testing.T) {
	from, to, err := resolveExportWindow("", "")
	if err != nil {
		t.Fatalf("default window: %v", err)
	}
	span := to.Sub(from)
	if span < 29*24*time.Hour || span > 31*24*time.Hour {
		t.Fatalf("default span = %s, want about 30 days", span)
	}

	start := time.Date(2025, 1, 15, 8, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	from, to, err = resolveExportWindow(start.Format(time.RFC3339), end.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("exact 12 months: %v", err)
	}
	if !from.Equal(start) || !to.Equal(end) {
		t.Fatalf("window = %s .. %s", from, to)
	}

	over := end.Add(time.Second)
	_, _, err = resolveExportWindow(start.Format(time.RFC3339), over.Format(time.RFC3339))
	if err == nil || !strings.Contains(err.Error(), "12 months") {
		t.Fatalf("over-wide window err = %v", err)
	}

	_, _, err = resolveExportWindow(end.Format(time.RFC3339), start.Format(time.RFC3339))
	if err == nil || !strings.Contains(err.Error(), "after from") {
		t.Fatalf("inverted window err = %v", err)
	}

	_, _, err = resolveExportWindow("yesterday", "")
	if err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("bad from err = %v", err)
	}
}

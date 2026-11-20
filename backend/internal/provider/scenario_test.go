package provider

import "testing"

func TestScenarioForAmount(t *testing.T) {
	cases := []struct {
		amount int64
		want   Scenario
	}{
		{10000, ScenarioSucceed},
		{1013, ScenarioDecline},
		{77, ScenarioDelay},
		{188, ScenarioDuplicate},
		{99, ScenarioSilent},
		{199, ScenarioSilent},
		{12, ScenarioSucceed},
		{-1013, ScenarioDecline},
	}
	for _, c := range cases {
		if got := ScenarioForAmount(c.amount); got != c.want {
			t.Fatalf("amount=%d scenario=%q want %q", c.amount, got, c.want)
		}
	}
}

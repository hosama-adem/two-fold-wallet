package provider

// Scenario is how the simulator should behave for one initiation.
type Scenario string

const (
	ScenarioSucceed   Scenario = "succeed"
	ScenarioDecline   Scenario = "decline"
	ScenarioDelay     Scenario = "delay"
	ScenarioDuplicate Scenario = "duplicate"
	ScenarioSilent    Scenario = "silent"
)

// ScenarioForAmount maps amount_minor's last two digits to a failure mode.
// Reserved suffixes: 13 decline, 77 delay, 88 duplicate, 99 silent.
// Everything else (including 00) succeeds.
func ScenarioForAmount(amountMinor int64) Scenario {
	if amountMinor < 0 {
		amountMinor = -amountMinor
	}
	switch amountMinor % 100 {
	case 13:
		return ScenarioDecline
	case 77:
		return ScenarioDelay
	case 88:
		return ScenarioDuplicate
	case 99:
		return ScenarioSilent
	default:
		return ScenarioSucceed
	}
}

// Package fee evaluates fee rules, previews, and expected-fee checks.
//
// Fees are integer-exact: basis points, round half up, then clamp to min/max
// in minor units. No floating point. Rules live in fee_rules (FR-FEE).
package fee

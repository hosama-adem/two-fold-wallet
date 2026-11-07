// Package pin verifies transaction PINs with abuse-resistant lockouts (FR-ID-7).
//
// Money-moving handlers should call Service.Verify — login/session stay usable
// while PIN lockout only blocks movement that requires the PIN.
package pin

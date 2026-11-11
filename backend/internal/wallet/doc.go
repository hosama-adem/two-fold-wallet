// Package wallet is the customer-facing account: one ETB liability,
// a freeze policy, and an available balance.
//
// Ledger balance is what the books say Twofold owes the customer.
// Available balance is that figure minus pending withdrawal holds.
// The difference is what they can spend right now.
//
// Freeze modes are enforced by Gate.Allows in the handler that posts money.
// Reading a balance is not a movement.
package wallet

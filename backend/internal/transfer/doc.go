// Package transfer moves money between two wallets in one database transaction.
//
// POST /v1/transfers locks both wallet rows in sorted id order, checks freeze
// and available balance, posts a balanced p2p journal, and records the
// transaction as pending → completed with a human-friendly TF- reference.
package transfer

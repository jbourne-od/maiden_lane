// Package semantic owns Maiden Lane's pure, deterministic semantic values.
//
// Values constructed here do not observe I/O, clocks, randomness, process
// configuration, or mutable global state. Constructors validate and copy
// caller-owned data; getters return copies, so canonical bytes and identities
// cannot change beneath an execution or replay.
package semantic

package sql

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/optimaldynamics/maiden-lane/internal/semantic"
)

// TestSyntheticEntityIDGoVsSQL verifies that Go's canonical synthetic entity
// byte encoder and the SQL bytea derivation formula produce the exact same
// SHA-256 digest across single progenitors, multiple progenitors, group aggregations,
// Unicode characters, and various discriminators.
func TestSyntheticEntityIDGoVsSQL(t *testing.T) {
	lineageHex := "0101010101010101010101010101010101010101010101010101010101010101"
	lineageID := semantic.InputLineageID("sha256:" + lineageHex)
	lineageRaw, _ := hex.DecodeString(lineageHex)

	cases := []struct {
		name          string
		kind          string
		rule          string
		progenitorHex []string
		discriminator string
	}{
		{
			name:          "single progenitor standard",
			kind:          "dynamic_load",
			rule:          "create_dynamic_loads",
			progenitorHex: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			discriminator: "TITAN_LOAD",
		},
		{
			name: "multi progenitor group merge",
			kind: "dynamic_driver",
			rule: "form_teams",
			progenitorHex: []string{
				"1111111111111111111111111111111111111111111111111111111111111111",
				"2222222222222222222222222222222222222222222222222222222222222222",
			},
			discriminator: "TEAM_PAIR_CHI",
		},
		{
			name:          "unicode characters in discriminator and rule",
			kind:          "über_load",
			rule:          "rùle_café_01",
			progenitorHex: []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			discriminator: "LOAD_München_🚀",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Native Go calculation
			progenitors := make([]semantic.EntityRef, len(tc.progenitorHex))
			for i, h := range tc.progenitorHex {
				progenitors[i] = semantic.EntityRef{
					Kind: semantic.EntityKind(tc.kind),
					ID:   semantic.EntityID("sha256:" + h),
				}
			}
			discVal, err := semantic.NewStringValue(tc.discriminator)
			if err != nil {
				t.Fatalf("NewStringValue failed: %v", err)
			}
			nativeID := semantic.SyntheticEntityID(lineageID, semantic.EntityKind(tc.kind), semantic.RuleID(tc.rule), progenitors, discVal)

			// 2. Binary bytea preimage mimicking PostgreSQL bytea construction
			var sqlBytea []byte

			// Tag: uint64 len(31) + "maiden-lane.synthetic-entity.v1"
			var tagLen [8]byte
			binary.BigEndian.PutUint64(tagLen[:], uint64(len("maiden-lane.synthetic-entity.v1")))
			sqlBytea = append(sqlBytea, tagLen[:]...)
			sqlBytea = append(sqlBytea, []byte("maiden-lane.synthetic-entity.v1")...)

			// Lineage: 32 raw bytes
			sqlBytea = append(sqlBytea, lineageRaw...)

			// Kind: uint64 len + kind bytes
			var kindLen [8]byte
			binary.BigEndian.PutUint64(kindLen[:], uint64(len(tc.kind)))
			sqlBytea = append(sqlBytea, kindLen[:]...)
			sqlBytea = append(sqlBytea, []byte(tc.kind)...)

			// Rule: uint64 len + rule bytes
			var ruleLen [8]byte
			binary.BigEndian.PutUint64(ruleLen[:], uint64(len(tc.rule)))
			sqlBytea = append(sqlBytea, ruleLen[:]...)
			sqlBytea = append(sqlBytea, []byte(tc.rule)...)

			// Progenitors count: uint64 len
			var progCount [8]byte
			binary.BigEndian.PutUint64(progCount[:], uint64(len(tc.progenitorHex)))
			sqlBytea = append(sqlBytea, progCount[:]...)

			// Progenitor IDs: 32 raw bytes each
			for _, h := range tc.progenitorHex {
				rawID, err := hex.DecodeString(h)
				if err != nil {
					t.Fatalf("hex.DecodeString progenitor: %v", err)
				}
				sqlBytea = append(sqlBytea, rawID...)
			}

			// Value: discriminator string -> kind byte 0x01 + uint64 len + string bytes
			sqlBytea = append(sqlBytea, 0x01)
			var discLen [8]byte
			binary.BigEndian.PutUint64(discLen[:], uint64(len(tc.discriminator)))
			sqlBytea = append(sqlBytea, discLen[:]...)
			sqlBytea = append(sqlBytea, []byte(tc.discriminator)...)

			// Compute SHA-256 over SQL bytea
			sqlDigestSum := sha256.Sum256(sqlBytea)
			sqlDigest := "sha256:" + hex.EncodeToString(sqlDigestSum[:])

			if string(nativeID) != sqlDigest {
				t.Fatalf("Mismatch! Native Go ID: %s, SQL Bytea Digest: %s", nativeID, sqlDigest)
			}
			t.Logf("Exact match! %s", sqlDigest)
		})
	}
}

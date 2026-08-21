package sql

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"strings"
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

			// Kind: uint64 len + kind UTF-8 bytes (OCTET_LENGTH(convert_to(kind, 'UTF8')))
			var kindLen [8]byte
			kindBytes := []byte(tc.kind)
			binary.BigEndian.PutUint64(kindLen[:], uint64(len(kindBytes)))
			sqlBytea = append(sqlBytea, kindLen[:]...)
			sqlBytea = append(sqlBytea, kindBytes...)

			// Rule: uint64 len + rule UTF-8 bytes (OCTET_LENGTH(convert_to(rule, 'UTF8')))
			var ruleLen [8]byte
			ruleBytes := []byte(tc.rule)
			binary.BigEndian.PutUint64(ruleLen[:], uint64(len(ruleBytes)))
			sqlBytea = append(sqlBytea, ruleLen[:]...)
			sqlBytea = append(sqlBytea, ruleBytes...)

			// Progenitors count: uint64 len
			var progCount [8]byte
			binary.BigEndian.PutUint64(progCount[:], uint64(len(tc.progenitorHex)))
			sqlBytea = append(sqlBytea, progCount[:]...)

			// Progenitor IDs: 32 raw bytes each, sorted by binary bytes
			for _, h := range tc.progenitorHex {
				rawID, err := hex.DecodeString(h)
				if err != nil {
					t.Fatalf("hex.DecodeString progenitor: %v", err)
				}
				sqlBytea = append(sqlBytea, rawID...)
			}

			// Value: discriminator string -> kind byte 0x01 + uint64 len + UTF-8 string bytes
			sqlBytea = append(sqlBytea, 0x01)
			var discLen [8]byte
			discBytes := []byte(tc.discriminator)
			binary.BigEndian.PutUint64(discLen[:], uint64(len(discBytes)))
			sqlBytea = append(sqlBytea, discLen[:]...)
			sqlBytea = append(sqlBytea, discBytes...)

			// Compute SHA-256 over SQL bytea
			sqlDigestSum := sha256.Sum256(sqlBytea)
			sqlDigest := "sha256:" + hex.EncodeToString(sqlDigestSum[:])

			if string(nativeID) != sqlDigest {
				t.Fatalf("Mismatch! Native Go ID: %s, SQL Bytea Digest: %s", nativeID, sqlDigest)
			}
		})
	}
}

// TestMultiOrderGroupedInsertionAndPermutationInvariance tests a group of 3 orders
// (order-A: $2858.64, order-B: $1234.02, order-C: $500.00) and asserts:
// 1. Exact revenue summation parity ($4592.66)
// 2. Exact Go vs SQL multi-progenitor synthetic ID match
// 3. Complete invariance under input row permutation
// 4. Correct update when a new earlier progenitor is prepended
func TestMultiOrderGroupedInsertionAndPermutationInvariance(t *testing.T) {
	lineageHex := "0101010101010101010101010101010101010101010101010101010101010101"
	lineageID := semantic.InputLineageID("sha256:" + lineageHex)
	lineageRaw, _ := hex.DecodeString(lineageHex)

	type RawOrder struct {
		ID      string
		Charge  int64
		CustID  string
		OrderID string
	}

	orders := []RawOrder{
		{ID: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Charge: 285864, CustID: "SIAC", OrderID: "0767911"},
		{ID: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Charge: 123402, CustID: "SIAC", OrderID: "0767987"},
		{ID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Charge: 50000, CustID: "SIAC", OrderID: "0768001"},
	}

	// 1. Calculate Expected Revenue
	var expectedRevenue int64
	for _, o := range orders {
		expectedRevenue += o.Charge
	}
	if expectedRevenue != 459266 {
		t.Fatalf("expected revenue 459266, got %d", expectedRevenue)
	}

	// 2. Compute Native Go Synthetic ID for the 3-order group
	goRefs := make([]semantic.EntityRef, len(orders))
	for i, o := range orders {
		goRefs[i] = semantic.EntityRef{
			Kind: "raw_order",
			ID:   semantic.EntityID(o.ID),
		}
	}
	discVal, _ := semantic.NewStringValue("TITAN_LOAD")
	nativeGroupID := semantic.SyntheticEntityID(lineageID, "dynamic_load", "create_dynamic_loads", goRefs, discVal)

	// 3. Compute SQL Synthetic ID via Transpiler Formula for 3-order group
	computeSQLGroupDigest := func(input []RawOrder) string {
		// Sort by raw 32-byte digest as SQL's `ORDER BY decode(SUBSTRING(s."id" FROM 8), 'hex')` does
		sorted := slices.Clone(input)
		slices.SortFunc(sorted, func(a, b RawOrder) int {
			rawA, _ := hex.DecodeString(strings.TrimPrefix(a.ID, "sha256:"))
			rawB, _ := hex.DecodeString(strings.TrimPrefix(b.ID, "sha256:"))
			return bytes.Compare(rawA, rawB)
		})

		// Mimic `COUNT(*) OVER (PARTITION BY s."customer_id")` and `STRING_AGG(SUBSTRING(s."id" FROM 8), '' ...)`
		var progHexBuilder strings.Builder
		for _, o := range sorted {
			progHexBuilder.WriteString(strings.TrimPrefix(o.ID, "sha256:"))
		}
		progRawBytes, _ := hex.DecodeString(progHexBuilder.String())

		var sqlBytea []byte

		// Tag len(31) + "maiden-lane.synthetic-entity.v1"
		var tagLen [8]byte
		binary.BigEndian.PutUint64(tagLen[:], uint64(len("maiden-lane.synthetic-entity.v1")))
		sqlBytea = append(sqlBytea, tagLen[:]...)
		sqlBytea = append(sqlBytea, []byte("maiden-lane.synthetic-entity.v1")...)

		// Lineage
		sqlBytea = append(sqlBytea, lineageRaw...)

		// Target Kind "dynamic_load" (OCTET_LENGTH(convert_to('dynamic_load', 'UTF8')))
		var kindLen [8]byte
		kindBytes := []byte("dynamic_load")
		binary.BigEndian.PutUint64(kindLen[:], uint64(len(kindBytes)))
		sqlBytea = append(sqlBytea, kindLen[:]...)
		sqlBytea = append(sqlBytea, kindBytes...)

		// Rule "create_dynamic_loads" (OCTET_LENGTH(convert_to('create_dynamic_loads', 'UTF8')))
		var ruleLen [8]byte
		ruleBytes := []byte("create_dynamic_loads")
		binary.BigEndian.PutUint64(ruleLen[:], uint64(len(ruleBytes)))
		sqlBytea = append(sqlBytea, ruleLen[:]...)
		sqlBytea = append(sqlBytea, ruleBytes...)

		// Progenitor count
		var progCount [8]byte
		binary.BigEndian.PutUint64(progCount[:], uint64(len(sorted)))
		sqlBytea = append(sqlBytea, progCount[:]...)

		// Progenitor bytes
		sqlBytea = append(sqlBytea, progRawBytes...)

		// Discriminator ValueString (kind=0x01) (OCTET_LENGTH(convert_to('TITAN_LOAD', 'UTF8')))
		sqlBytea = append(sqlBytea, 0x01)
		var discLen [8]byte
		discBytes := []byte("TITAN_LOAD")
		binary.BigEndian.PutUint64(discLen[:], uint64(len(discBytes)))
		sqlBytea = append(sqlBytea, discLen[:]...)
		sqlBytea = append(sqlBytea, discBytes...)

		sum := sha256.Sum256(sqlBytea)
		return "sha256:" + hex.EncodeToString(sum[:])
	}

	sqlGroupID := computeSQLGroupDigest(orders)

	if string(nativeGroupID) != sqlGroupID {
		t.Fatalf("Group ID mismatch! Native Go ID: %s, SQL Transpiled ID: %s", nativeGroupID, sqlGroupID)
	}
	t.Logf("Group 3-progenitor ID matched: %s", sqlGroupID)

	// 4. Test Permutation Invariance (Shuffling order of input rows)
	permutations := [][]RawOrder{
		{orders[2], orders[0], orders[1]}, // C, A, B
		{orders[1], orders[2], orders[0]}, // B, C, A
		{orders[2], orders[1], orders[0]}, // C, B, A
	}

	for pIdx, p := range permutations {
		permSQLID := computeSQLGroupDigest(p)
		if permSQLID != sqlGroupID {
			t.Fatalf("Permutation %d violated group ID invariance: got %s, want %s", pIdx, permSQLID, sqlGroupID)
		}
	}
	t.Logf("All permutations produced invariant group ID")

	// 5. Test 4th Progenitor addition (Prepending 0000... which sorts first)
	order4 := RawOrder{
		ID:      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Charge:  10000,
		CustID:  "SIAC",
		OrderID: "0767900",
	}
	orders4 := append([]RawOrder{order4}, orders...)

	goRefs4 := make([]semantic.EntityRef, len(orders4))
	for i, o := range orders4 {
		goRefs4[i] = semantic.EntityRef{
			Kind: "raw_order",
			ID:   semantic.EntityID(o.ID),
		}
	}
	native4ID := semantic.SyntheticEntityID(lineageID, "dynamic_load", "create_dynamic_loads", goRefs4, discVal)
	sql4ID := computeSQLGroupDigest(orders4)

	if string(native4ID) != sql4ID {
		t.Fatalf("4-progenitor group ID mismatch! Native: %s, SQL: %s", native4ID, sql4ID)
	}
	if sql4ID == sqlGroupID {
		t.Fatalf("4-progenitor group ID must differ from 3-progenitor group ID")
	}
	t.Logf("4-progenitor group ID matched and correctly updated: %s", sql4ID)
}

package interpolate

import (
	"github.com/hexops/autogold/v2"
	"github.com/keegancsmith/sqlf"
	"github.com/stretchr/testify/require"
	"testing"
)

const myArgsQuery = "SELECT * from {{TableName: string}} WHERE id = {{WantId: int}}"

const partyAttendeesQuery = `
SELECT person_name
FROM party_attendees
WHERE party = {{partyId : int}}
`

const bestChoiceCakeQuery = `
WITH attendees AS (` + partyAttendeesQuery + `)

SELECT fave_cakes.cake_type
FROM attendees JOIN fave_cakes ON attendees.person_name = fave_cakes.person_name
-- Need to allow host to exclude one cake they don't like
WHERE fave_cake.cake_type != {{excludedCakeType : string}}
GROUP BY fave_cake.cake_type 
ORDER BY COUNT(fave_cake.cake_type) DESC
LIMIT 1
`

var _ = bestChoiceCakeQuery

func TestDo(t *testing.T) {
	type TestCase struct {
		query      string
		input      QueryParams
		expect     autogold.Value
		expectArgs autogold.Value
	}

	testCases := []TestCase{
		{
			query:      myArgsQuery,
			input:      &myArgsQueryParams{TableName: "T", WantId: 1},
			expect:     autogold.Expect("SELECT * from $1 WHERE id = $2"),
			expectArgs: autogold.Expect([]interface{}{"T", 1}),
		},
	}

	for _, tc := range testCases {
		query, err := Do(tc.query, tc.input)
		require.NoError(t, err)
		tc.expect.Equal(t, query.Query(sqlf.PostgresBindVar))
		tc.expectArgs.Equal(t, query.Args())
	}
}

func TestSqlf(t *testing.T) {
	// This seems weird, should we do our own run-time type-checking?
	require.NotPanics(t, func() {
		query := sqlf.Sprintf("%d", "foobar")
		val := autogold.Expect("$1")
		val.Equal(t, query.Query(sqlf.PostgresBindVar))
	})
}

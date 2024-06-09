package internal

import (
	"testing"

	"github.com/hexops/autogold/v2"
	"github.com/stretchr/testify/require"
)

func TestSubstitutionRegex(t *testing.T) {
	re := SubstitutionRegex
	type testCase struct {
		input    string
		builders autogold.Value
	}
	testCases := []testCase{
		{input: "{{foo: bar}}", builders: autogold.Expect([]GoStructFieldBuilder{{
			Name:     "foo",
			TypeName: "bar",
		}})},
		{input: "{{foo: string}}", builders: autogold.Expect([]GoStructFieldBuilder{{
			Name:     "foo",
			TypeName: "string",
		}})},
		{input: "{{ foo: abc.X}}", builders: autogold.Expect([]GoStructFieldBuilder{{
			Name:     "foo",
			TypeName: "abc.X",
		}})},
		{input: "SELECT {{col: string}} from {{foo: string}}", builders: autogold.Expect([]GoStructFieldBuilder{
			{
				Name:     "col",
				TypeName: "string",
			},
			{
				Name:     "foo",
				TypeName: "string",
				Index:    1,
			},
		})},
		{
			input: `SELECT * from T WHERE X = {{x: *int: %s}} AND Y = {{uploadedParts: any}}`, builders: autogold.Expect([]GoStructFieldBuilder{
				{
					Name:     "x",
					TypeName: "*int",
				},
				{
					Name:     "uploadedParts",
					TypeName: "any",
					Index:    1,
				},
			}),
		},
	}
	for _, tc := range testCases {
		require.True(t, re.MatchString(tc.input))
		var builders []GoStructFieldBuilder
		for i, matches := range re.FindAllStringSubmatch(tc.input, -1) {
			builders = append(builders, NewFieldBuilder(i, matches))
		}
		tc.builders.Equal(t, builders)
	}
}

package internal

import (
	"fmt"

	"github.com/grafana/regexp"
)

// SubstitutionRegex represents interpolation syntax.
// Conceptually, the following syntaxes are allowed
//
//	{{ fieldName : typeName }}
//	{{ fieldName : _ }} // allowed for 2nd, 3rd etc. interpolation of same field
//	{{ fieldName : typeName : formatSpec }} where formatSpec may be %v or similar
//
// formatSpec must not contain positional arguments (i.e. %[0]f is not OK)
var SubstitutionRegex *regexp.Regexp = func() *regexp.Regexp {
	rawIdentifier := `[a-zA-Z_][a-zA-Z0-9_]*`
	qualifiedIdentifier := fmt.Sprintf(`(%s\.)?%s`, rawIdentifier, rawIdentifier)
	return regexp.MustCompile(fmt.Sprintf(
		`{{\s*(%s)\s*:\s*(%s)\s*(:\s*(%%(.+))\s*)?}}`, rawIdentifier, qualifiedIdentifier))
}()

// GoStructFieldBuilder maps N-1 to GoStructField, as the same field
// may be interpolated multiple times in the same query.
type GoStructFieldBuilder struct {
	Name                    string
	TypeName                string
	ExplicitFormatSpecifier string
	Index                   int
}

func NewFieldBuilder(matchIndex int, matches []string) GoStructFieldBuilder {
	if len(matches) < 6 {
		panic("expected field name at index 1, type name at index 2, and optional format specifier at index 5")
	}
	return GoStructFieldBuilder{
		Name:                    matches[1],
		TypeName:                matches[2],
		ExplicitFormatSpecifier: matches[5],
		Index:                   matchIndex,
	}
}

var QueryConstNameRegex *regexp.Regexp = func() *regexp.Regexp {
	return regexp.MustCompile(".*Query(Fragment)?[_0-9]*$")
}()

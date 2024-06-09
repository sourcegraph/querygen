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
//
// formatSpec must not contain positional arguments (i.e. %[0]f is not OK)
var SubstitutionRegex *regexp.Regexp = func() *regexp.Regexp {
	rawIdentifier := `[a-zA-Z_][a-zA-Z0-9_]*`
	// Q: Should we simplify this to parse everything?
	typeName := fmt.Sprintf(`(\*?%s\.)?[a-zA-Z0-9_*\[\] ]+`, rawIdentifier)
	return regexp.MustCompile(fmt.Sprintf(
		`{{\s*(%s)\s*:\s*(%s)\s*(:\s*(%%(.+?))\s*)?}}`, rawIdentifier, typeName))
}()

// GoStructFieldBuilder maps N-1 to GoStructField, as the same field
// may be interpolated multiple times in the same query.
type GoStructFieldBuilder struct {
	Name     string
	TypeName string
	Index    int
}

func NewFieldBuilder(matchIndex int, matches []string) GoStructFieldBuilder {
	if len(matches) < 3 {
		panic("expected field name at index 1, type name at index 2")
	}
	return GoStructFieldBuilder{
		Name:     matches[1],
		TypeName: matches[2],
		Index:    matchIndex,
	}
}

var QueryConstNameRegex *regexp.Regexp = func() *regexp.Regexp {
	return regexp.MustCompile(".*Query(Fragment)?[_0-9]*$")
}()

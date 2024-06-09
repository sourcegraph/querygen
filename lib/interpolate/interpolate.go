package interpolate

import (
	"fmt"

	"github.com/keegancsmith/sqlf"

	"github.com/sourcegraph/querygen/internal"
)

type QueryVars interface {
	// FormatArgs returns 1 element per interpolation
	FormatArgs() []any
}

type QueryDoesntUseInterpolationError struct{}

var _ error = &QueryDoesntUseInterpolationError{}

func (e *QueryDoesntUseInterpolationError) Error() string {
	return "query doesn't use interpolation"
}

// Do creates a sqlf.Query from the given query string and QueryVars.
//
// If the query doesn't use interpolation, returns nil, &QueryDoesntUseInterpolationError{}.
func Do(query string, q QueryVars) (*sqlf.Query, error) {
	modifiedQuery := internal.SubstitutionRegex.ReplaceAllString(query, "%s")
	// Just doing a length check instead of a content check is OK
	// because a query with one or more interpolations must be longer
	// than the query with replacements.
	if len(modifiedQuery) == len(query) {
		return nil, &QueryDoesntUseInterpolationError{}
	}
	return sqlf.Sprintf(modifiedQuery, q.FormatArgs()...), nil
}

// MustDo creates a sqlf.Query from the given query string and QueryVars.
//
// Panics if the query doesn't use interpolation.
func MustDo(query string, q QueryVars) *sqlf.Query {
	result, err := Do(query, q)
	if err != nil {
		panic(fmt.Sprintf("%s: %25s", err.Error(), query))
	}
	return result
}

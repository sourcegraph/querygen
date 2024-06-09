package interpolate

import (
	"fmt"

	"github.com/keegancsmith/sqlf"
	"github.com/sourcegraph/querygen/internal"
)

type QueryVars interface {
	// FormatSpecifiers returns 1 element per interpolation
	FormatSpecifiers() []string
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
	formatSpecs := q.FormatSpecifiers()
	matchIndex := 0
	modifiedQuery := internal.SubstitutionRegex.ReplaceAllStringFunc(query, func(match string) string {
		replacement := formatSpecs[matchIndex]
		matchIndex += 1
		return replacement
	})
	if matchIndex == 0 {
		return nil, &QueryDoesntUseInterpolationError{}
	}
	return sqlf.Sprintf(modifiedQuery, q.FormatArgs()...), nil
}

// MustDo creates a sqlf.Query from the given query string and QueryVars.
//
// Panics if the query doesn't use interpolation.
func MustDo(query string, q QueryVars) (*sqlf.Query, error) {
	formatSpecs := q.FormatSpecifiers()
	matchIndex := 0
	modifiedQuery := internal.SubstitutionRegex.ReplaceAllStringFunc(query, func(match string) string {
		replacement := formatSpecs[matchIndex]
		matchIndex += 1
		return replacement
	})
	if matchIndex == 0 {
		err := &QueryDoesntUseInterpolationError{}
		panic(fmt.Sprintf("%s: %25s", err.Error(), query))
	}
	return sqlf.Sprintf(modifiedQuery, q.FormatArgs()...), nil
}

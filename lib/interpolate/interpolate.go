package interpolate

import (
	"github.com/keegancsmith/sqlf"

	"github.com/sourcegraph/querygen/internal"
)

type QueryParams interface {
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

func Do(query string, q QueryParams) (*sqlf.Query, error) {
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

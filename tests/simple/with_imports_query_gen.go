// Code generated by querygen.
// You may only edit import statements.
package simple

import (
	"github.com/sourcegraph/querygen/lib/interpolate"
	_ "math" // Added by hand
)

type myQueryVars struct {
	abc string
}

var _ interpolate.QueryVars = &myQueryVars{}

func (qp *myQueryVars) FormatArgs() []any {
	return []any{qp.abc}
}

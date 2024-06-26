// Code generated by querygen.
// You may only edit import statements.
package interpolate

type myArgsQueryVars struct {
	TableName string
	WantId    int
}

var _ QueryVars = &myArgsQueryVars{}

func (qp *myArgsQueryVars) FormatArgs() []any {
	return []any{qp.TableName, qp.WantId}
}

type partyAttendeesQueryVars struct {
	partyId int
}

var _ QueryVars = &partyAttendeesQueryVars{}

func (qp *partyAttendeesQueryVars) FormatArgs() []any {
	return []any{qp.partyId}
}

type bestChoiceCakeQueryVars struct {
	partyId          int
	excludedCakeType string
}

var _ QueryVars = &bestChoiceCakeQueryVars{}

func (qp *bestChoiceCakeQueryVars) FormatArgs() []any {
	return []any{qp.partyId, qp.excludedCakeType}
}

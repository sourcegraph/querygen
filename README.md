# querygen: CLI + tiny helper library for more readable SQL queries

## Usage

Write a SQL query in your Go code using interpolation-like syntax:

```go
package cakes // cakes.go

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
```

Run the `querygen` CLI on the package.

This will generate a file next to the original file:

```go
package cakes // cakes_query_gen.go

import (
	"github.com/sourcegraph/querygen/lib/interpolate"
)

type partyAttendeesQueryParams struct {
	partyId int
}
var _ interpolate.QueryParams = &partyAttendeesQueryParams{}
// methods omitted...

type bestChoiceCakeQueryParams struct {
	partyId          int
	excludedCakeType string
}
var _ interpolate.QueryParams = &bestChoiceCakeQueryParams{}

// methods omitted...
```

You can use these structs with `interpolate.Do(myQuery, &myQueryParams{...})` function
to generate a [`*sqlf.Query`](https://sourcegraph.com/search?q=context:global+repo:%5Egithub%5C.com/keegancsmith/sqlf%24%40master+file:sqlf.go+type:symbol+Query&patternType=keyword&sm=0)
which can then be executed. The `interpolate.Do` function replaces the `sqlf.Sprintf`
function.

For more complex usage, see [Reference.md](docs/Reference.md).

## Contributing

See [Development.md](docs/Development.md) for build instructions etc.

At the moment, this is made primarily for Sourcegraph's internal use.
So any form of support will be on a best-effort basis.
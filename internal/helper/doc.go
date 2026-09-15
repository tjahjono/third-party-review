// Package helper holds the shared, layer-neutral functions the rest of the
// application leans on: the sentinel errors every layer maps outcomes to, the
// validation-error collection forms render, the database connection, and the
// small pure computations (risk banding, header normalisation, text
// excerpting) that more than one layer needs.
//
// The rule for what belongs here is "used by more than one layer, and owned by
// none of them". Anything a single layer owns lives in that layer instead:
// validation rules are in internal/service, display labels are template
// functions in the handler package.
//
// Nothing here may import a repository, service or delivery package, so this
// package can be imported from anywhere without creating a cycle.
package helper

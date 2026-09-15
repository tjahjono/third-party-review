// Package repository declares the persistence contracts the service layer
// depends on - step 1 of the dependency template, one interface per model.
//
// The interfaces live at the layer root rather than beside their
// implementation so that the direction of dependency stays delivery -> service
// -> repository -> database: a service imports this package and never
// this package, and swapping or stubbing the storage engine
// touches nothing above it.
//
// Concrete implementations are in the subpackages (currently postgres/), each
// asserting conformance at compile time.
package repository

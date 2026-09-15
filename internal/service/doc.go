// Package service declares the business-logic contracts the delivery layer
// depends on, plus the validation rules that guard every write.
//
// The contracts sit at the layer root rather than beside their implementation
// so delivery imports this package and never internal/service/assessment: the
// handler holds interfaces, the subpackages implement them, and neither
// imports the other. They are split by concern rather than exposed as one
// large interface, because the vendor screens need nothing from the review
// pipeline and a test for them should not have to satisfy it.
//
// Validation lives here for the same reason it no longer hangs off the model
// types: it is a service-layer rule about what may be written, not a property
// of the data, and only this layer enforces it.
package service

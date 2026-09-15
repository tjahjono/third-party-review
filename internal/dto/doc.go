// Package dto holds the shapes that cross a layer boundary but are not rows in
// the database: query filters, the ingestion preview a user confirms before
// anything is written, the provider-neutral AI request and response envelopes,
// and the small result types the service contracts return.
//
// They live outside internal/model so that package can be read as the schema,
// and outside the service packages so a contract can name one without the
// delivery layer importing an implementation.
//
// Unlike model, types here may carry behaviour: they are working shapes, and
// an accessor the templates call is part of what makes them usable.
package dto

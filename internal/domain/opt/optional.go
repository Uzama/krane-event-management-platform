// Package opt is the leaf-adjacent presence-tracking type PATCH semantics
// need: CLAUDE.md's rule is that an absent field and an explicit null are
// different requests, and a plain *T cannot tell them apart (encoding/json
// leaves it nil either way). Optional[T] can, by only ever being touched by
// UnmarshalJSON when the key was present at all.
package opt

import "encoding/json"

// Optional[T] wraps a request field that may be absent, explicitly null, or
// explicitly set. Set is false only when the JSON key never appeared.
type Optional[T any] struct {
	Value T
	Set   bool
}

// Of builds a present, set Optional -- for tests and for services
// constructing a patch from something other than a decoded request body.
func Of[T any](v T) Optional[T] {
	return Optional[T]{Value: v, Set: true}
}

// UnmarshalJSON is only invoked by encoding/json when the field's key was
// present in the object -- an absent key never calls this, which is exactly
// what makes Set distinguish absent from explicit null. T = *U for a
// nullable column: "null" unmarshals into a nil *U with Set still true.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

// MarshalJSON lets Optional[T] round-trip through encoding/json in tests
// and any response DTO that reuses it.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.Value)
}

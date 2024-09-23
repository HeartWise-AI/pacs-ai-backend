package assert

import (
	"github.com/stretchr/testify/assert"
)

// Use assert.ElementsMatch for comparing slices, but with a bool result.
type dummyt struct{}

func (t dummyt) Errorf(string, ...interface{}) {}

// ElementsMatch is a custom assertion function that uses assert.ElementsMatch but returns a boolean result.
// https://stackoverflow.com/a/66062073
func ElementsMatch(listA, listB interface{}) bool {
	return assert.ElementsMatch(dummyt{}, listA, listB)
}

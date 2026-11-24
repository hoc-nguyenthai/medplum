package binarystore_test

import (
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/storetest"
)

func TestFSRangeContract(t *testing.T) { storetest.RunRange(t, newContractFS) }
func TestMemoryRangeContract(t *testing.T) {
	storetest.RunRange(t, func(testing.TB) binarystore.Store { return binarystore.NewMemory() })
}

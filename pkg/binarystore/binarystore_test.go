package binarystore_test

import (
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/storetest"
)

func newContractFS(t testing.TB) binarystore.Store {
	fs, err := binarystore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

func TestFSContract(t *testing.T) { storetest.Run(t, newContractFS) }

func TestFSListerContract(t *testing.T) { storetest.RunLister(t, newContractFS) }

func TestMemoryListerContract(t *testing.T) {
	storetest.RunLister(t, func(testing.TB) binarystore.Store { return binarystore.NewMemory() })
}

func TestMemoryContract(t *testing.T) {
	storetest.Run(t, func(testing.TB) binarystore.Store { return binarystore.NewMemory() })
}

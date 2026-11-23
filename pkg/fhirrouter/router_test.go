package fhirrouter_test

import (
	"testing"

	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirrouter/repotest"
)

func TestMemoryRepository(t *testing.T) {
	repotest.Run(t, func(*testing.T) fhirrouter.Repository { return fhirrouter.NewMemoryRepository() })
}

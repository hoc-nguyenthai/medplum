package ws_test

import "os"

func envAddr() string { return os.Getenv("FHIRCAST_TEST_REDIS_ADDR") }

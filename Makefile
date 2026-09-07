.PHONY: test-bdd

test-bdd:
	cd tools/cli && CGO_ENABLED=0 go test -v -run TestFeatures ./internal/evalengine/... ./internal/spawn/...

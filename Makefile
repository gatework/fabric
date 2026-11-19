STANDARDS_DIR ?=
include standard.mk

.PHONY: fuzz corpus

CHECK_EXTRA += fuzz

# Short fuzz passes over every parser and decoder.
fuzz:
	$(GO) test -run '^$$' -fuzz '^FuzzParseConfig$$' -fuzztime=10s ./vendors/nxos
	$(GO) test -run '^$$' -fuzz '^FuzzObserve$$' -fuzztime=10s ./vendors/nxos
	$(GO) test -run '^$$' -fuzz '^FuzzDecodeIntent$$' -fuzztime=10s .

# Runs the closed loops on the private capture corpus named by
# FABRIC_CORPUS (one directory per switch). Tests print aggregates only.
corpus:
	@test -n "$(FABRIC_CORPUS)" || { echo "set FABRIC_CORPUS to a snapshot directory"; exit 1; }
	$(GO) test -count=1 -run Corpus -v ./internal/corpus | grep -E '^(=== RUN|--- |\s+corpus_test|ok|FAIL)'

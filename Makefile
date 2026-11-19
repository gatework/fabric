STANDARDS_DIR ?=
include standard.mk

.PHONY: fuzz corpus bench synth

CHECK_EXTRA += fuzz

# Short fuzz passes over every parser and decoder.
fuzz:
	$(GO) test -run '^$$' -fuzz '^FuzzParseConfig$$' -fuzztime=10s ./vendors/nxos
	$(GO) test -run '^$$' -fuzz '^FuzzObserve$$' -fuzztime=10s ./vendors/nxos
	$(GO) test -run '^$$' -fuzz '^FuzzDecodeIntent$$' -fuzztime=10s .
	$(GO) test -run '^$$' -fuzz '^FuzzSubnet$$' -fuzztime=10s ./numbering

# Runs the closed loops on the private capture corpus named by
# FABRIC_CORPUS (one directory per switch). Tests print aggregates only.
corpus:
	@test -n "$(FABRIC_CORPUS)" || { echo "set FABRIC_CORPUS to a snapshot directory"; exit 1; }
	$(GO) test -count=1 -run Corpus -v ./internal/corpus | grep -E '^(=== RUN|--- |\s+corpus_test|ok|FAIL)'

# Fleet-scale benchmarks on a synthetic 1000-switch fabric; compare with
# bench/baseline.txt using benchstat.
bench:
	$(GO) test -run '^$$' -bench . -benchtime 3x -count 1 ./internal/synth | tee bench/current.txt

# Writes the synthetic fabric (snapshot + intent) to SYNTH_OUT.
synth:
	@test -n "$(SYNTH_OUT)" || { echo "set SYNTH_OUT to a directory"; exit 1; }
	FABRIC_SYNTH_OUT=$(SYNTH_OUT) $(GO) test -count=1 -run WriteFleet ./internal/synth

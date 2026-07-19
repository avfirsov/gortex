package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The full-coverage census attestation is strictly one-shot: consumed by the
// next global-pass run and reset, so a cold batch's attestation can never
// leak into a later incremental batch.
func TestArmBatchCensusEligibleIsOneShot(t *testing.T) {
	mi := &MultiIndexer{}
	assert.False(t, mi.takeBatchCensusEligible(), "unarmed by default")

	mi.ArmBatchCensusEligible()
	assert.True(t, mi.takeBatchCensusEligible(), "armed attestation is consumed once")
	assert.False(t, mi.takeBatchCensusEligible(), "consumption must reset the attestation")

	var nilIndexer *MultiIndexer
	nilIndexer.ArmBatchCensusEligible()
	assert.False(t, nilIndexer.takeBatchCensusEligible(), "nil receiver is inert")
}

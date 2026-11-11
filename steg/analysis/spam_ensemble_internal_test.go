package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSPAMEnsembleDetectsLSBMatching is the end-to-end smoke test of the
// detector: 200 synthetic photo-like planes (one scene per seed), each with a
// cover and a ±1 LSB-matching stego version at 100% rate. Planes 0–99 (both
// versions) train the ensemble with default options; planes 100–199 test it,
// so no scene appears on both sides. The chi-square and RS detectors cannot
// see this embedding at all; SPAM + FLD ensemble must do clearly better than
// guessing.
//
// Calibration (P_E averaged over the two classes): test error 0.000 on
// 128×128 planes and 0.105 on 64×64, insensitive to the ridge over
// 1e-8 … 1e-1. The bound 0.2 is loose; it is the "clearly below 0.5" the
// detector exists to deliver, not a performance target. Runtime ≈ 0.2 s.
func TestSPAMEnsembleDetectsLSBMatching(t *testing.T) {
	if testing.Short() {
		t.Skip("trains a 51-learner ensemble on 686-dim features")
	}
	const n, w, h = 200, 128, 128
	cover := make([][]float64, n)
	stego := make([][]float64, n)
	for i := range n {
		plane := photoPlane(w, h, uint64(i)+1)
		var err error
		cover[i], err = SPAM(plane, w, h)
		require.NoError(t, err)
		stego[i], err = SPAM(lsbMatch(plane, 1, uint64(i)+1), w, h)
		require.NoError(t, err)
	}
	e, err := TrainEnsemble(cover[:n/2], stego[:n/2], EnsembleOptions{Seed: 1})
	require.NoError(t, err)

	trainPE := testError(e, cover[:n/2], stego[:n/2])
	pe := testError(e, cover[n/2:], stego[n/2:])
	t.Logf("SPAM + FLD ensemble, ±1 LSB matching at 100%%, %d×%d: train P_E %.3f, test P_E %.3f", w, h, trainPE, pe)
	assert.Less(t, pe, 0.2)
}

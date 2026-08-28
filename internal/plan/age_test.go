package plan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAge(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30d", 720 * time.Hour},
		{"7d", 168 * time.Hour},
		{"4w", 672 * time.Hour},
		{"720h", 720 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"1d12h", 36 * time.Hour},
		{"0", 0},
	} {
		got, err := ParseAge(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
}

func TestParseAgeRejects(t *testing.T) {
	for _, in := range []string{"", "-5d", "bogus", "d", "30D", "30x"} {
		_, err := ParseAge(in)
		assert.Error(t, err, in)
	}
}

// GoDuration must emit hours: buildx rejects until=7d and accepts until=168h.
func TestGoDuration(t *testing.T) {
	assert.Equal(t, "168h", GoDuration(7*24*time.Hour))
	assert.Equal(t, "720h", GoDuration(30*24*time.Hour))
}

func TestHumanAge(t *testing.T) {
	assert.Equal(t, "30m", HumanAge(30*time.Minute))
	assert.Equal(t, "5h", HumanAge(5*time.Hour))
	assert.Equal(t, "47d", HumanAge(47*24*time.Hour))
}

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSizeUnmarshal(t *testing.T) {
	tests := []struct {
		input    string
		expected Size
	}{
		{"100MB", 100 * Megabyte},
		{"1GB", 1 * Gigabyte},
		{"512KB", 512 * Kilobyte},
		{"1TB", 1 * Terabyte},
		{"1024", 1024},
		{"100M", 100 * Megabyte},
		{"1G", 1 * Gigabyte},
		{"1.5GB", Size(1.5 * float64(Gigabyte))},
	}

	for _, tt := range tests {
		var s Size
		err := s.UnmarshalText([]byte(tt.input))
		require.NoError(t, err, "input: %s", tt.input)
		assert.Equal(t, tt.expected, s, "input: %s", tt.input)
	}
}

func TestSizeUnmarshalInvalid(t *testing.T) {
	var s Size
	err := s.UnmarshalText([]byte("notasize"))
	assert.Error(t, err)
}

func TestSizeString(t *testing.T) {
	assert.Equal(t, "100MB", Size(100*Megabyte).String())
	assert.Equal(t, "1GB", Size(1*Gigabyte).String())
	assert.Equal(t, "512KB", Size(512*Kilobyte).String())
}

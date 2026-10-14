package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Size is a byte size (e.g. "100MB", "1GB") supporting yaml unmarshalling.
type Size uint64

const (
	Byte     = 1
	Kilobyte = 1024 * Byte
	Megabyte = 1024 * Kilobyte
	Gigabyte = 1024 * Megabyte
	Terabyte = 1024 * Gigabyte
)

// Bytes returns the size in bytes.
func (s Size) Bytes() uint64 {
	return uint64(s)
}

// String implements `fmt.Stringer`.
func (s Size) String() string {
	switch {
	case s >= Terabyte && s%Terabyte == 0:
		return fmt.Sprintf("%dTB", s/Terabyte)
	case s >= Gigabyte && s%Gigabyte == 0:
		return fmt.Sprintf("%dGB", s/Gigabyte)
	case s >= Megabyte && s%Megabyte == 0:
		return fmt.Sprintf("%dMB", s/Megabyte)
	case s >= Kilobyte && s%Kilobyte == 0:
		return fmt.Sprintf("%dKB", s/Kilobyte)
	default:
		return fmt.Sprintf("%dB", s)
	}
}

// UnmarshalText implements `encoding.TextUnmarshaler`.
func (s *Size) UnmarshalText(data []byte) error {
	input := strings.TrimSpace(strings.ToUpper(string(data)))

	multipliers := map[string]uint64{
		"TB": Terabyte,
		"GB": Gigabyte,
		"MB": Megabyte,
		"KB": Kilobyte,
		"B":  Byte,
		"T":  Terabyte,
		"G":  Gigabyte,
		"M":  Megabyte,
		"K":  Kilobyte,
	}

	for suffix, mult := range multipliers {
		if strings.HasSuffix(input, suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(input, suffix))

			var num float64

			var err error
			if num, err = strconv.ParseFloat(numStr, 64); err != nil {
				continue
			}

			*s = Size(num * float64(mult))

			return nil
		}
	}

	// plain number: bytes
	if num, err := strconv.ParseFloat(input, 64); err == nil {
		*s = Size(num)

		return nil
	}

	return fmt.Errorf("invalid size '%s': use e.g. 100MB, 1GB, 512KB", string(data))
}

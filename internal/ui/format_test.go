package ui

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		0:                      "0B",
		512:                    "512B",
		1024:                   "1.00K",
		1536:                   "1.50K",
		1024 * 1024:            "1.00M",
		1024 * 1024 * 1024:     "1.00G",
		10 * 1024 * 1024:       "10.0M",
		100 * 1024 * 1024:      "100M",
		8 * 1024 * 1024 * 1024: "8.00G",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatCPUTime(t *testing.T) {
	cases := map[float64]string{
		0:    "0:00.00",
		5.5:  "0:05.50",
		65:   "1:05.00",
		3661: "61:01.00",
	}
	for in, want := range cases {
		if got := formatCPUTime(in); got != want {
			t.Errorf("formatCPUTime(%v) = %q, want %q", in, got, want)
		}
	}
}

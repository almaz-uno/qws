package config

import "testing"

func TestParsePadding(t *testing.T) {
	tests := []struct {
		name      string
		padding   string
		dimension int
		want      int
	}{
		// Percentage tests
		{
			name:      "5% of 1000",
			padding:   "5%",
			dimension: 1000,
			want:      50,
		},
		{
			name:      "10% of 1920",
			padding:   "10%",
			dimension: 1920,
			want:      192,
		},
		{
			name:      "2.5% of 1000",
			padding:   "2.5%",
			dimension: 1000,
			want:      25,
		},
		{
			name:      "percentage with spaces",
			padding:   " 5% ",
			dimension: 1000,
			want:      50,
		},
		// Pixel tests
		{
			name:      "50 pixels",
			padding:   "50px",
			dimension: 1000,
			want:      50,
		},
		{
			name:      "100 pixels",
			padding:   "100px",
			dimension: 1920,
			want:      100,
		},
		{
			name:      "pixels with spaces",
			padding:   " 75px ",
			dimension: 1000,
			want:      75,
		},
		// Plain number tests
		{
			name:      "plain number as pixels",
			padding:   "60",
			dimension: 1000,
			want:      60,
		},
		{
			name:      "plain number with spaces",
			padding:   " 80 ",
			dimension: 1000,
			want:      80,
		},
		// Error cases
		{
			name:      "invalid format",
			padding:   "invalid",
			dimension: 1000,
			want:      0,
		},
		{
			name:      "empty string",
			padding:   "",
			dimension: 1000,
			want:      0,
		},
		{
			name:      "invalid percentage",
			padding:   "abc%",
			dimension: 1000,
			want:      0,
		},
		{
			name:      "invalid pixels",
			padding:   "abcpx",
			dimension: 1000,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePadding(tt.padding, tt.dimension)
			if got != tt.want {
				t.Errorf("ParsePadding(%q, %d) = %d, want %d", tt.padding, tt.dimension, got, tt.want)
			}
		})
	}
}

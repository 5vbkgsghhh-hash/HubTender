package repository

import "testing"

func TestNextAdditionalPositionNumber(t *testing.T) {
	tests := []struct {
		name   string
		parent float64
		last   *float64
		want   float64
	}{
		{"first", 5, nil, 5.1},
		{"second", 5, fptr(5.1), 5.2},
		{"ninth", 5, fptr(5.8), 5.9},
		// раньше после x.9 получалось (x+1).0 — номер следующей позиции
		{"after .9", 5, fptr(5.9), 5.91},
		{"after .91", 5, fptr(5.91), 5.92},
		{"after .99", 5, fptr(5.99), 5.991},
		{"after .991", 5, fptr(5.991), 5.992},
		{"prod case 1546", 1546, fptr(1546.9), 1546.91},
		{"legacy fractional parent", 4.1, nil, 4.2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nextAdditionalPositionNumber(tt.parent, tt.last)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("nextAdditionalPositionNumber(%v, %v) = %v, want %v", tt.parent, tt.last, got, tt.want)
			}
			if got >= tt.parent+1 && tt.last != nil {
				t.Fatalf("number %v reached the next position", got)
			}
		})
	}
}

func TestNormalizeAdditionalName(t *testing.T) {
	if normalizeAdditionalName("  Устройство   ПЕРЕМЫЧЕК ") != normalizeAdditionalName("устройство перемычек") {
		t.Fatal("names differing only in case/spaces must match")
	}
	if normalizeAdditionalName("Перемычки") == normalizeAdditionalName("Перемычки 2") {
		t.Fatal("different names must not match")
	}
}

package calc

import (
	"math"
	"testing"
)

func TestLinkedMaterialQuantity(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name       string
		work       float64
		conv, cons *float64
		want       float64
		invalid    bool
	}{
		{"defaults", 10, nil, nil, 10, false},
		{"conversion and consumption", 10, ptr(.25), ptr(2), 5, false},
		{"negative work", -1, nil, nil, 0, true},
		{"zero conversion", 10, ptr(0), nil, 0, true},
		{"NaN consumption", 10, nil, ptr(math.NaN()), 0, true},
		{"overflow", math.MaxFloat64, ptr(2), nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CalculateLinkedMaterialQuantity(tc.work, tc.conv, tc.cons)
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("quantity=%v err=%v want=%v invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
